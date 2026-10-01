package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/btrvodka/redigate/internal/service"
)

const (
	sseHeartbeat    = 15 * time.Second
	sseWriteTimeout = 10 * time.Second

	CodeTooManyStreams = "TOO_MANY_STREAMS"
)

// streamHandler starts a stream; it must stop and close the channel when the request context is done.
type streamHandler func(r *http.Request) (<-chan service.Event, error)

// stream registers an authenticated endpoint serving server-sent events.
//
// Events: "<type>" with JSON data produced by the handler, "error" for recoverable
// errors and the final "end" with {"reason": "duration" | "closed" | "canceled"}.
// Comments (": ping") are sent as heartbeats.
func (s *Server) stream(mux *http.ServeMux, pattern string, h streamHandler) {
	s.handle(mux, pattern, s.protected(func(w http.ResponseWriter, r *http.Request) {
		ctx, release, err := s.acquireStream(r)
		if err != nil {
			s.writeError(w, r, err)

			return
		}
		defer release()

		events, err := h(r.WithContext(ctx))
		if err != nil {
			s.writeError(w, r, err)

			return
		}

		// Drain the channel so producers can finish if writing to the client fails.
		defer func() {
			release()

			for range events {
			}
		}()

		s.serveEvents(ctx, w, events)
	}))
}

// acquireStream takes a stream slot and limits the request by the stream duration.
// Long-running endpoints (SSE, export, import) share the MAX_STREAMS slots.
func (s *Server) acquireStream(r *http.Request) (context.Context, func(), error) {
	select {
	case s.streams <- struct{}{}:
	default:
		return nil, nil, newAPIError(http.StatusTooManyRequests, CodeTooManyStreams,
			"too many open streams, the limit is %d", cap(s.streams))
	}

	duration, err := s.streamDuration(r)
	if err != nil {
		<-s.streams

		return nil, nil, err
	}

	ctx, cancel := context.WithTimeout(r.Context(), duration)

	var once sync.Once

	return ctx, func() {
		once.Do(func() {
			cancel()
			<-s.streams
		})
	}, nil
}

func (s *Server) streamDuration(r *http.Request) (time.Duration, error) {
	limit := s.cfg.Limits.MaxStreamDuration

	raw := r.URL.Query().Get("duration")
	if raw == "" {
		return limit, nil
	}

	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0, badRequest("invalid duration %q, expected a duration like 30s", raw)
	}

	if duration > limit {
		return 0, badRequest("duration %s exceeds the limit of %s", duration, limit)
	}

	return duration, nil
}

func (s *Server) serveEvents(ctx context.Context, w http.ResponseWriter, events <-chan service.Event) {
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	// Disables response buffering in nginx.
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sse := &sseWriter{w: w, rc: http.NewResponseController(w)}
	if sse.comment("connected") != nil {
		return
	}

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				_ = sse.event("end", map[string]string{"reason": endReason(ctx)})

				return
			}

			if sse.event(event.Type, event.Data) != nil {
				return
			}
		case <-heartbeat.C:
			if sse.comment("ping") != nil {
				return
			}
		}
	}
}

func endReason(ctx context.Context) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "duration"
	case ctx.Err() != nil:
		return "canceled"
	default:
		return "closed"
	}
}

type sseWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
	id int64
}

func (s *sseWriter) event(name string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		payload, _ = json.Marshal(map[string]string{"error": err.Error()})
		name = "error"
	}

	s.id++

	return s.write("id: " + strconv.FormatInt(s.id, 10) + "\nevent: " + name + "\ndata: " + string(payload) + "\n\n")
}

func (s *sseWriter) comment(text string) error {
	return s.write(": " + text + "\n\n")
}

// write sends a chunk and flushes it. A client that does not read for
// sseWriteTimeout is disconnected.
func (s *sseWriter) write(chunk string) error {
	_ = s.rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))

	if _, err := s.w.Write([]byte(chunk)); err != nil {
		return fmt.Errorf("write event: %w", err)
	}

	if err := s.rc.Flush(); err != nil {
		return fmt.Errorf("flush event: %w", err)
	}

	return nil
}
