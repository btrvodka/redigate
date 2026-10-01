package httpapi

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/btrvodka/redigate/internal/service"
)

const exportFlushEvery = 100

func (s *Server) transferRoutes(mux *http.ServeMux) {
	//	@Summary		Export keys
	//	@Description	Streams keys as NDJSON, one key per line (service.ExportRecord). format=dump keeps exact values with DUMP; format=value is portable between Redis and Valkey versions for core types. The last line is {"summary": {"keys": n, "complete": bool}}.
	//	@Tags			keys
	//	@Produce		application/x-ndjson
	//	@Param			match		query		string	false	"Glob pattern"
	//	@Param			type		query		string	false	"Type filter"
	//	@Param			format		query		string	false	"dump or value"
	//	@Param			db			query		int		false	"Database"
	//	@Param			duration	query		string	false	"Maximum duration"
	//	@Success		200			{file}		file	"NDJSON, one service.ExportRecord per line"
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/export [get]
	s.handle(mux, "GET /api/v1/keys/export", s.protected(func(w http.ResponseWriter, r *http.Request) {
		ctx, release, err := s.acquireStream(r)
		if err != nil {
			s.writeError(w, r, err)

			return
		}
		defer release()

		q := newQuery(r)
		opts := service.ExportOptions{
			Match:    q.str("match", ""),
			Type:     q.str("type", ""),
			Format:   q.str("format", service.FormatDump),
			DB:       q.db(),
			ReadOnly: readOnlyRequest(r),
		}

		if q.err != nil {
			s.writeError(w, r, q.err)

			return
		}

		buf := bufio.NewWriter(w)
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		flush := exportFlusher(buf, http.NewResponseController(w))
		started := false

		count, err := s.svc.Export(ctx, opts, func(record *service.ExportRecord) error {
			if !started {
				started = true

				w.Header().Set("Content-Type", "application/x-ndjson")
				w.Header().Set("Content-Disposition", `attachment; filename="redigate-export.ndjson"`)
				w.WriteHeader(http.StatusOK)
			}

			if err := enc.Encode(record); err != nil {
				return err //nolint:wrapcheck // client is gone
			}

			flush()

			return nil
		})

		if !started {
			if err != nil {
				s.writeError(w, r, err)

				return
			}

			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
		}

		if err != nil {
			_ = enc.Encode(map[string]string{"error": err.Error()})
		}

		_ = enc.Encode(map[string]any{"summary": map[string]any{"keys": count, "complete": err == nil}})
		_ = buf.Flush()
	}))

	//	@Summary		Import keys
	//	@Description	Restores keys from NDJSON produced by export. The body may be up to MAX_IMPORT_BYTES.
	//	@Tags			keys
	//	@Accept			application/x-ndjson
	//	@Produce		json
	//	@Param			request	body		string	true	"NDJSON"
	//	@Param			replace	query		bool	false	"Overwrite existing keys"
	//	@Param			db		query		int		false	"Database"
	//	@Success		200		{object}	Response{result=service.ImportResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/import [post]
	s.handle(mux, "POST /api/v1/keys/import", s.protectedLimit(func(w http.ResponseWriter, r *http.Request) {
		ctx, release, err := s.acquireStream(r)
		if err != nil {
			s.writeError(w, r, err)

			return
		}
		defer release()

		q := newQuery(r)
		opts := service.ImportOptions{Replace: q.bool("replace"), DB: q.db(), ReadOnly: readOnlyRequest(r)}

		if q.err != nil {
			s.writeError(w, r, q.err)

			return
		}

		result, err := s.svc.Import(ctx, r.Body, opts)
		if err != nil {
			s.writeError(w, r, bodyError(err))

			return
		}

		s.writeResult(w, r, result)
	}, s.cfg.Limits.MaxImportBytes))
}

// exportFlusher flushes the response every exportFlushEvery records.
func exportFlusher(buf *bufio.Writer, rc *http.ResponseController) func() {
	n := 0

	return func() {
		if n++; n%exportFlushEvery == 0 {
			_ = buf.Flush()
			_ = rc.Flush()
		}
	}
}

// bodyError maps a body size violation to 413.
func bodyError(err error) error {
	if maxErr, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return newAPIError(http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body exceeds %d bytes", maxErr.Limit)
	}

	return err
}
