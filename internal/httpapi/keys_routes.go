package httpapi

import (
	"net/http"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/service"
)

func (s *Server) keyRoutes(mux *http.ServeMux) {
	//	@Summary		List keys
	//	@Description	SCAN over the database; in cluster masters are scanned one after another. Repeat with the returned cursor until it is "0". With meta every key is an object with type and TTL.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			match	query		string	false	"Glob pattern"
	//	@Param			type	query		string	false	"Type filter"
	//	@Param			count	query		int		false	"Page size hint"
	//	@Param			cursor	query		string	false	"Cursor from the previous page"
	//	@Param			meta	query		bool	false	"Add type and TTL"
	//	@Param			db		query		int		false	"Database"
	//	@Success		200		{object}	Response{result=service.ScanResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys [get]
	s.api(mux, "GET /api/v1/keys", func(r *http.Request) (any, error) {
		q := newQuery(r)
		opts := service.ScanOptions{
			Match:    q.str("match", ""),
			Type:     q.str("type", ""),
			Count:    int(q.int("count", 0)),
			Cursor:   q.str("cursor", ""),
			DB:       q.db(),
			Encoding: q.encoding(),
			WithMeta: q.bool("meta"),
		}

		if q.err != nil {
			return nil, q.err
		}

		return s.svc.ScanKeys(r.Context(), opts)
	})

	//	@Summary		Read a key
	//	@Description	Reads the value of any type with its metadata. Collections are limited to MAX_RESPONSE_ITEMS elements. Returns 404 for a missing key.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.KeyValue}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/value [get]
	s.api(mux, "GET /api/v1/keys/value", func(r *http.Request) (any, error) {
		q := newQuery(r)
		key, db, enc := q.key("key"), q.db(), q.encoding()

		if q.err != nil {
			return nil, q.err
		}

		return s.svc.KeyValue(r.Context(), key, db, enc)
	})
	//	@Summary		Key metadata
	//	@Description	Type, TTL, length, encoding, memory usage, idle time and frequency.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.KeyMeta}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/meta [get]
	s.api(mux, "GET /api/v1/keys/meta", func(r *http.Request) (any, error) {
		q := newQuery(r)
		key, db, enc := q.key("key"), q.db(), q.encoding()

		if q.err != nil {
			return nil, q.err
		}

		return s.svc.KeyMeta(r.Context(), key, db, enc)
	})

	// Per-key commands work across cluster slots: ?key=a&key=b.
	multi := func(cmd string) jsonHandler {
		return func(r *http.Request) (any, error) {
			q := newQuery(r)
			keys := q.keys("key")

			if q.err != nil {
				return nil, q.err
			}

			opts, err := execOptions(r, execParams{})
			if err != nil {
				return nil, err
			}

			return s.svc.MultiKeyEncoded(r.Context(), cmd, keys, nil, opts)
		}
	}

	//	@Summary		Check keys
	//	@Description	EXISTS per key; keys may belong to different cluster slots.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			key		query		[]string	true	"Keys, repeat for several"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.MultiKeyResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/exists [get]
	s.api(mux, "GET /api/v1/keys/exists", multi("exists"))
	//	@Summary		Delete keys
	//	@Description	DEL (or UNLINK with unlink) per key; keys may belong to different cluster slots.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			key		query		[]string	true	"Keys, repeat for several"	collectionFormat(multi)
	//	@Param			unlink	query		bool		false	"Use UNLINK"
	//	@Success		200		{object}	Response{result=service.MultiKeyResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys [delete]
	s.api(mux, "DELETE /api/v1/keys", func(r *http.Request) (any, error) {
		if newQuery(r).bool("unlink") {
			return multi("unlink")(r)
		}

		return multi("del")(r)
	})
	//	@Summary		Touch keys
	//	@Description	TOUCH per key.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			key		query		[]string	true	"Keys, repeat for several"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.MultiKeyResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/touch [post]
	s.api(mux, "POST /api/v1/keys/touch", multi("touch"))

	//	@Summary		Delete keys by pattern
	//	@Description	Unlinks every key matching the pattern on every master. When the request is about to time out it stops and returns a cursor to continue from.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			body	KeysDeleteRequest	true	"Request body"
	//	@Param			db		query		int					false	"Database"
	//	@Success		200		{object}	Response{result=service.DeleteByPatternResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/delete [post]
	s.api(mux, "POST /api/v1/keys/delete", func(r *http.Request) (any, error) {
		var body KeysDeleteRequest

		if err := bindJSON(r, &body); err != nil {
			return nil, err
		}

		q := newQuery(r)
		opts := service.ScanOptions{
			Match: body.Match, Type: body.Type, Cursor: body.Cursor, DB: q.db(), ReadOnly: readOnlyRequest(r),
		}

		if q.err != nil {
			return nil, q.err
		}

		return s.svc.DeleteByPattern(r.Context(), opts, body.DryRun)
	})

	//	@Summary		Random key
	//	@Description	RANDOMKEY.
	//	@Tags			keys
	//	@Produce		json
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/random [get]
	s.api(mux, "GET /api/v1/keys/random", func(r *http.Request) (any, error) {
		return s.call(r, nil, "randomkey")
	})
	//	@Summary		Key type
	//	@Description	TYPE.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/type [get]
	s.api(mux, "GET /api/v1/keys/type", func(r *http.Request) (any, error) {
		return s.oneKey(r, "type")
	})
	//	@Summary		Key TTL
	//	@Description	PTTL in milliseconds: -1 without TTL, -2 for a missing key.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/ttl [get]
	s.api(mux, "GET /api/v1/keys/ttl", func(r *http.Request) (any, error) {
		return s.oneKey(r, "pttl")
	})

	//	@Summary		Set TTL
	//	@Description	PEXPIRE with ttl_ms or PEXPIREAT with expire_at_ms; condition is NX, XX, GT or LT.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			body	KeyTTLRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/ttl [put]
	s.api(mux, "PUT /api/v1/keys/ttl", func(r *http.Request) (any, error) {
		var body KeyTTLRequest

		if err := bindJSON(r, &body); err != nil {
			return nil, err
		}

		if err := required([]string{"key"}, body.Key); err != nil {
			return nil, err
		}

		var args []any

		switch {
		case body.TTLMS != nil && body.ExpireAtMS == nil:
			args = cmd("pexpire", body.Key, *body.TTLMS)
		case body.ExpireAtMS != nil && body.TTLMS == nil:
			args = cmd("pexpireat", body.Key, *body.ExpireAtMS)
		default:
			return nil, badRequest("exactly one of ttl_ms and expire_at_ms is required")
		}

		if body.Condition != "" {
			args = append(args, body.Condition)
		}

		return s.call(r, nil, args...)
	})
	//	@Summary		Remove TTL
	//	@Description	PERSIST.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/ttl [delete]
	s.api(mux, "DELETE /api/v1/keys/ttl", func(r *http.Request) (any, error) {
		return s.oneKey(r, "persist")
	})

	//	@Summary		Rename a key
	//	@Description	RENAME or RENAMENX; in cluster keys must hash to the same slot.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			body	KeyRenameRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/rename [post]
	s.api(mux, "POST /api/v1/keys/rename", func(r *http.Request) (any, error) {
		var body KeyRenameRequest

		if err := bindJSON(r, &body); err != nil {
			return nil, err
		}

		if err := required([]string{"key", "new_key"}, body.Key, body.NewKey); err != nil {
			return nil, err
		}

		name := "rename"
		if body.NX {
			name = "renamenx"
		}

		return s.call(r, nil, cmd(name, body.Key, body.NewKey)...)
	})

	//	@Summary		Copy a key
	//	@Description	COPY.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			body	KeyCopyRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/copy [post]
	s.api(mux, "POST /api/v1/keys/copy", func(r *http.Request) (any, error) {
		var body KeyCopyRequest

		if err := bindJSON(r, &body); err != nil {
			return nil, err
		}

		if err := required([]string{"key", "destination"}, body.Key, body.Destination); err != nil {
			return nil, err
		}

		args := cmd("copy", body.Key, body.Destination)
		if body.DestinationDB != nil {
			args = append(args, "db", *body.DestinationDB)
		}

		return s.call(r, nil, cmd(args, flag(body.Replace, "replace"))...)
	})

	//	@Summary		Serialize a key
	//	@Description	DUMP, always base64-encoded. Restore it with POST /keys/restore.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/dump [get]
	s.api(mux, "GET /api/v1/keys/dump", func(r *http.Request) (any, error) {
		q := newQuery(r)
		key := q.key("key")

		if q.err != nil {
			return nil, q.err
		}

		// DUMP output is binary, always return it base64-encoded.
		return s.callWith(r, func(opts *service.ExecOptions) { opts.Encoding = codec.EncodingBase64 }, "dump", key)
	})

	//	@Summary		Restore a key
	//	@Description	RESTORE a value from GET /keys/dump.
	//	@Tags			keys
	//	@Produce		json
	//	@Param			body	KeyRestoreRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/keys/restore [post]
	s.api(mux, "POST /api/v1/keys/restore", func(r *http.Request) (any, error) {
		var body KeyRestoreRequest

		if err := bindJSON(r, &body); err != nil {
			return nil, err
		}

		if err := required([]string{"key", "dump"}, body.Key, body.Dump); err != nil {
			return nil, err
		}

		args := cmd("restore", body.Key, body.TTLMS, body.Dump, flag(body.Replace, "replace"), flag(body.AbsTTL, "absttl"))
		if body.IdleSeconds != nil {
			args = append(args, "idletime", *body.IdleSeconds)
		}

		if body.Frequency != nil {
			args = append(args, "freq", *body.Frequency)
		}

		return s.call(r, nil, args...)
	})
}

// oneKey runs a command with a single key from ?key=.
func (s *Server) oneKey(r *http.Request, name string, extra ...any) (any, error) {
	q := newQuery(r)
	key := q.key("key")

	if q.err != nil {
		return nil, q.err
	}

	return s.call(r, nil, cmd(name, key, extra)...)
}

type KeysDeleteRequest struct {
	// Glob pattern, required.
	Match string `json:"match"`
	// Delete only keys of this type.
	Type string `json:"type"`
	// Cursor of a previous incomplete request.
	Cursor string `json:"cursor"`
	// Count matching keys without deleting them.
	DryRun bool `json:"dry_run"`
}

type KeyTTLRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// TTL in milliseconds.
	TTLMS *int64 `json:"ttl_ms"`
	// Absolute expiration time, Unix milliseconds.
	ExpireAtMS *int64 `json:"expire_at_ms"`
	// Expiration condition.
	Condition string `json:"condition" enums:"NX,XX,GT,LT"`
}

type KeyRenameRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// New key name.
	NewKey codec.Arg `json:"new_key"`
	// RENAMENX: only if the new key does not exist.
	NX bool `json:"nx"`
}

type KeyCopyRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Destination key.
	Destination codec.Arg `json:"destination"`
	// Database of the destination.
	DestinationDB *int `json:"destination_db"`
	// Overwrite existing data.
	Replace bool `json:"replace"`
}

type KeyRestoreRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Serialized value from GET /keys/dump.
	Dump codec.Arg `json:"dump"`
	// TTL in milliseconds, 0 for none.
	TTLMS int64 `json:"ttl_ms"`
	// Overwrite existing data.
	Replace bool `json:"replace"`
	// ttl_ms is an absolute Unix time in milliseconds.
	AbsTTL bool `json:"absttl"`
	// LRU idle time.
	IdleSeconds *int64 `json:"idle_seconds"`
	// LFU frequency.
	Frequency *int64 `json:"frequency"`
}
