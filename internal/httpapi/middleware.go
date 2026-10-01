package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/btrvodka/redigate/internal/auth"
)

type contextKey int

const (
	requestIDKey contextKey = iota
	accessKey
)

const (
	headerRequestID = "X-Request-Id"
	maxRequestIDLen = 128
)

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)

	return id
}

func accessFromContext(ctx context.Context) auth.Access {
	access, _ := ctx.Value(accessKey).(auth.Access)

	return access
}

// statusRecorder captures the response status and keeps http.ResponseController working.
type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}

	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	return r.ResponseWriter.Write(b) //nolint:wrapcheck // transparent wrapper
}

func (r *statusRecorder) Flush() {
	_ = http.NewResponseController(r.ResponseWriter).Flush()
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// withRequestID takes the request id from the header or generates a new one.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if !validRequestID(id) {
			id = newRequestID()
		}

		w.Header().Set(headerRequestID, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}

	for _, c := range id {
		if c < '!' || c > '~' {
			return false
		}
	}

	return true
}

func newRequestID() string {
	b := make([]byte, 16) //nolint:mnd // 128 bits
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// withAccessLog logs requests and records metrics. It must wrap the mux directly,
// so that r.Pattern is set by the mux on the same *http.Request.
func (s *Server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		duration := time.Since(start)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}

		if rec.status == 0 {
			rec.status = http.StatusOK
		}

		s.metrics.ObserveHTTPRequest(route, rec.status, duration)
		s.log.InfoContext(r.Context(), "http request",
			slog.String("request_id", requestIDFromContext(r.Context())),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("route", route),
			slog.Int("status", rec.status),
			slog.Duration("duration", duration),
			slog.String("remote_addr", r.RemoteAddr),
		)
	})
}

func (s *Server) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { //nolint:contextcheck // the request context is used via r
			rec := recover()
			if rec == nil {
				return
			}

			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}

			s.log.ErrorContext(r.Context(), "panic recovered",
				slog.Any("panic", rec),
				slog.String("stack", string(debug.Stack())),
			)
			s.writeError(w, r, newAPIError(http.StatusInternalServerError, CodeInternal, "internal error"))
		}()

		next.ServeHTTP(w, r)
	})
}

// authenticate resolves the access level from the bearer token.
func (s *Server) authenticate(r *http.Request) (auth.Access, error) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")

	access, err := auth.Resolve(s.cfg.Auth, strings.TrimSpace(token))
	switch {
	case errors.Is(err, auth.ErrTokenRequired):
		return auth.None, newAPIError(http.StatusUnauthorized, CodeUnauthorized, "bearer token is required")
	case err != nil:
		return auth.None, newAPIError(http.StatusUnauthorized, CodeUnauthorized, "invalid token")
	}

	return access, nil
}
