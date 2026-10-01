package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/httpapi"
)

type sseEvent struct {
	Name string
	Data map[string]any
}

// sseStream reads server-sent events of a request in the background.
type sseStream struct {
	status int
	events chan sseEvent
	cancel context.CancelFunc
}

func openStream(t *testing.T, srv *httptest.Server, path, token string) *sseStream {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := srv.Client().Do(req) //nolint:bodyclose // closed by the reader goroutine
	if err != nil {
		t.Fatal(err)
	}

	stream := &sseStream{status: resp.StatusCode, events: make(chan sseEvent, 100), cancel: cancel}

	go func() {
		defer resp.Body.Close()
		defer close(stream.events)

		scanner := bufio.NewScanner(resp.Body)

		var event sseEvent

		for scanner.Scan() {
			line := scanner.Text()

			switch {
			case strings.HasPrefix(line, "event: "):
				event.Name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event.Data)
			case line == "" && event.Name != "":
				stream.events <- event
				event = sseEvent{}
			}
		}
	}()

	return stream
}

// next returns the next event with the given name, skipping others.
func (s *sseStream) next(t *testing.T, name string) sseEvent {
	t.Helper()

	timeout := time.After(5 * time.Second)

	for {
		select {
		case event, ok := <-s.events:
			if !ok {
				t.Fatalf("stream closed while waiting for %q", name)
			}

			if event.Name == name {
				return event
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %q", name)
		}
	}
}

func TestPubSubStream(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, config.Auth{})

	stream := openStream(t, srv, "/api/v1/pubsub/subscribe?channel=news&pattern=ev.*", "")
	if stream.status != http.StatusOK {
		t.Fatalf("status %d", stream.status)
	}

	stream.next(t, "subscription")
	stream.next(t, "subscription")

	if got := value(t, srv, http.MethodPost, "/api/v1/pubsub/publish", `{"channel": "news", "message": "hello"}`); got != float64(1) {
		t.Errorf("receivers = %v", got)
	}

	value(t, srv, http.MethodPost, "/api/v1/pubsub/publish", `{"channel": "ev.1", "message": {"base64": "/w=="}}`)

	if msg := stream.next(t, "message"); msg.Data["channel"] != "news" || msg.Data["payload"] != "hello" {
		t.Errorf("message = %v", msg.Data)
	}

	msg := stream.next(t, "message")
	if msg.Data["pattern"] != "ev.*" || msg.Data["payload"].(map[string]any)["base64"] != "/w==" {
		t.Errorf("pattern message = %v", msg.Data)
	}

	numsub := result[map[string]any](t, srv, http.MethodGet, "/api/v1/pubsub/numsub?channel=news", "")
	if numsub["counts"].(map[string]any)["news"] != float64(1) {
		t.Errorf("numsub = %v", numsub)
	}
}

func TestStreamTail(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, config.Auth{})

	value(t, srv, http.MethodPost, "/api/v1/streams/add", `{"key": "st", "id": "1-0", "fields": [{"field": "old", "value": "1"}]}`)

	// Without ids the tail starts after the last entry.
	stream := openStream(t, srv, "/api/v1/streams/tail?key=st", "")

	time.Sleep(200 * time.Millisecond)

	value(t, srv, http.MethodPost, "/api/v1/streams/add", `{"key": "st", "id": "2-0", "fields": [{"field": "new", "value": "2"}]}`)

	entry := stream.next(t, "entry")
	if entry.Data["id"] != "2-0" || entry.Data["fields"].(map[string]any)["new"] != "2" {
		t.Errorf("entry = %v", entry.Data)
	}

	// From the beginning.
	all := openStream(t, srv, "/api/v1/streams/tail?key=st&id=0-0", "")
	if first := all.next(t, "entry"); first.Data["id"] != "1-0" {
		t.Errorf("first entry = %v", first.Data)
	}
}

func TestStreamLimits(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, config.Auth{Token: "full", ReadOnlyToken: "ro"})

	// The stream ends after the requested duration.
	short := openStream(t, srv, "/api/v1/pubsub/subscribe?channel=c&duration=300ms", "ro")
	if end := short.next(t, "end"); end.Data["reason"] != "duration" {
		t.Errorf("end = %v", end.Data)
	}

	for _, tt := range []struct {
		path, token, code string
		status            int
	}{
		{"/api/v1/pubsub/subscribe?channel=c&duration=forever", "ro", httpapi.CodeBadRequest, http.StatusBadRequest},
		{"/api/v1/pubsub/subscribe?channel=c&duration=100h", "ro", httpapi.CodeBadRequest, http.StatusBadRequest},
		{"/api/v1/pubsub/subscribe", "ro", httpapi.CodeBadRequest, http.StatusBadRequest},
		{"/api/v1/pubsub/subscribe?channel=c", "", httpapi.CodeUnauthorized, http.StatusUnauthorized},
		{"/api/v1/monitor", "ro", httpapi.CodeForbidden, http.StatusForbidden},
		{"/api/v1/streams/tail?key=s&group=g&consumer=c", "ro", httpapi.CodeForbidden, http.StatusForbidden},
	} {
		if status, env := do(t, srv, http.MethodGet, tt.path, tt.token); status != tt.status || env.Code != tt.code {
			t.Errorf("%s: %d %s (%s)", tt.path, status, env.Code, env.Description)
		}
	}
}

func TestTooManyStreams(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServerWith(t, config.Auth{}, func(cfg *config.Config) { cfg.Limits.MaxStreams = 1 })

	first := openStream(t, srv, "/api/v1/pubsub/subscribe?channel=c", "")
	first.next(t, "subscription")

	if status, env := do(t, srv, http.MethodGet, "/api/v1/pubsub/subscribe?channel=c", ""); status != http.StatusTooManyRequests {
		t.Errorf("second stream: %d %+v", status, env)
	}

	// The slot is released when the client disconnects.
	first.cancel()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if second := openStream(t, srv, "/api/v1/pubsub/subscribe?channel=c", ""); second.status == http.StatusOK {
			return
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Error("stream slot was not released")
}
