//go:build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

type sseEvent struct {
	Name string
	Data map[string]any
}

// openStream starts an SSE request and returns a channel of its events.
func (e *env) openStream(t *testing.T, path string) <-chan sseEvent {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer "+fullToken)

	resp, err := e.srv.Client().Do(req) //nolint:bodyclose // closed by the reader goroutine
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, resp.StatusCode)
	}

	events := make(chan sseEvent, 1000)

	go func() {
		defer resp.Body.Close()
		defer close(events)

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
				events <- event
				event = sseEvent{}
			}
		}
	}()

	return events
}

// waitEvent returns the first event with the name matching the predicate.
func waitEvent(t *testing.T, events <-chan sseEvent, name string, match func(map[string]any) bool) sseEvent {
	t.Helper()

	timeout := time.After(10 * time.Second)

	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("stream closed while waiting for %s", name)
			}

			if event.Name == name && (match == nil || match(event.Data)) {
				return event
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", name)
		}
	}
}

func streams(t *testing.T, e *env) {
	t.Helper()

	t.Run("pubsub", func(t *testing.T) {
		// Shard channels "a" and "b" belong to different cluster slots.
		events := e.openStream(t, "/api/v1/pubsub/subscribe?channel=news&pattern=ev.*&shard_channel=a&shard_channel=b")

		for range 4 {
			waitEvent(t, events, "subscription", nil)
		}

		value(t, e, http.MethodPost, "/api/v1/pubsub/publish", `{"channel": "news", "message": "1"}`)
		value(t, e, http.MethodPost, "/api/v1/pubsub/publish", `{"channel": "ev.x", "message": "2"}`)
		value(t, e, http.MethodPost, "/api/v1/pubsub/publish", `{"channel": "a", "message": "3", "sharded": true}`)
		value(t, e, http.MethodPost, "/api/v1/pubsub/publish", `{"channel": "b", "message": "4", "sharded": true}`)

		got := make(map[string]any)
		for range 4 {
			msg := waitEvent(t, events, "message", nil)
			got[msg.Data["channel"].(string)] = msg.Data["payload"]
		}

		if len(got) != 4 || got["news"] != "1" || got["ev.x"] != "2" || got["a"] != "3" || got["b"] != "4" {
			t.Errorf("messages = %v", got)
		}

		channels := get[map[string]any](t, e, http.MethodGet, "/api/v1/pubsub/channels?sharded", "")
		if len(channels["channels"].([]any)) != 2 {
			t.Errorf("shard channels = %v", channels)
		}
	})

	t.Run("monitor", func(t *testing.T) {
		events := e.openStream(t, "/api/v1/monitor")

		// Writes to keys of every slot owner must be seen on the masters.
		eventually(t, 10*time.Second, "monitored command", func() bool {
			value(t, e, http.MethodPost, "/api/v1/command", "SET monitored 1")

			select {
			case event := <-events:
				args, _ := event.Data["args"].([]any)

				return event.Name == "command" && len(args) > 1 && args[1] == "monitored"
			case <-time.After(500 * time.Millisecond):
				return false
			}
		})
	})

	t.Run("stream tail", func(t *testing.T) {
		value(t, e, http.MethodPost, "/api/v1/streams/add", `{"key": "tail1", "id": "1-0", "fields": [{"field": "f", "value": "old"}]}`)

		events := e.openStream(t, "/api/v1/streams/tail?key=tail1&key=tail2")
		time.Sleep(300 * time.Millisecond)

		value(t, e, http.MethodPost, "/api/v1/streams/add", `{"key": "tail1", "fields": [{"field": "f", "value": "new1"}]}`)
		value(t, e, http.MethodPost, "/api/v1/streams/add", `{"key": "tail2", "fields": [{"field": "f", "value": "new2"}]}`)

		seen := make(map[string]any)
		for range 2 {
			entry := waitEvent(t, events, "entry", nil)
			seen[entry.Data["stream"].(string)] = entry.Data["fields"].(map[string]any)["f"]
		}

		if seen["tail1"] != "new1" || seen["tail2"] != "new2" {
			t.Errorf("tail entries = %v", seen)
		}
	})
}
