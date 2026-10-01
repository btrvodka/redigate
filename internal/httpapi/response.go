package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

const CodeOK = "OK"

// Response is the envelope of every JSON response.
type Response struct {
	// Code is "OK" on success or an error code.
	Code        string `json:"error_code"        example:"OK"`
	Description string `json:"error_description" example:""`
	RequestID   string `json:"request_id"        example:"6f1c2a9e0b7d4c3f8a5e1d2b3c4f5a6b"`
	Result      any    `json:"result,omitempty"`
}

// ErrorResponse is the envelope of a failed request.
type ErrorResponse struct {
	Code        string `json:"error_code"        example:"BAD_REQUEST"`
	Description string `json:"error_description" example:"key is required"`
	RequestID   string `json:"request_id"        example:"6f1c2a9e0b7d4c3f8a5e1d2b3c4f5a6b"`
}

// jsonHandler is a handler returning a result to encode or an error to map.
type jsonHandler func(r *http.Request) (any, error)

func (s *Server) writeResult(w http.ResponseWriter, r *http.Request, result any) {
	writeJSON(w, r, http.StatusOK, Response{
		Code:      CodeOK,
		RequestID: requestIDFromContext(r.Context()),
		Result:    result,
	})
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	apiErr := toAPIError(err)

	level := slog.LevelDebug
	if apiErr.Status >= http.StatusInternalServerError {
		level = slog.LevelWarn
	}

	s.log.Log(r.Context(), level, "request failed",
		slog.String("request_id", requestIDFromContext(r.Context())),
		slog.String("code", apiErr.Code),
		slog.Any("error", err),
	)

	if apiErr.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="redigate"`)
	}

	writeJSON(w, r, apiErr.Status, ErrorResponse{
		Code:        apiErr.Code,
		Description: apiErr.Message,
		RequestID:   requestIDFromContext(r.Context()),
	})
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	if r.URL.Query().Has("pretty") {
		enc.SetIndent("", "  ")
	}

	_ = enc.Encode(body)
}
