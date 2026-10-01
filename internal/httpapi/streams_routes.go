package httpapi

import (
	"cmp"
	"net/http"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/service"
)

// trimArgs builds MAXLEN/MINID trimming options of XADD and XTRIM.
func trimArgs(maxLen *int64, minID string, approx bool, limit int64) ([]any, error) {
	if maxLen != nil && minID != "" {
		return nil, badRequest("maxlen and minid are mutually exclusive")
	}

	var args []any

	switch {
	case maxLen != nil:
		args = append(args, "maxlen")
	case minID != "":
		args = append(args, "minid")
	default:
		return nil, nil
	}

	if approx {
		args = append(args, "~")
	} else {
		args = append(args, "=")
	}

	if maxLen != nil {
		args = append(args, *maxLen)
	} else {
		args = append(args, minID)
	}

	if limit > 0 {
		if !approx {
			return nil, badRequest("limit requires approx")
		}

		args = append(args, "limit", limit)
	}

	return args, nil
}

func (s *Server) streamRoutes(mux *http.ServeMux) {
	//	@Summary		Get stream entries
	//	@Description	XRANGE, or XREVRANGE with rev: [{"id", "fields"}].
	//	@Tags			streams
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			start	query		string	false	"Start id, default -"
	//	@Param			end		query		string	false	"End id, default +"
	//	@Param			count	query		int		false	"Number of entries, default 100"
	//	@Param			rev		query		bool	false	"Reverse order"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams [get]
	s.api(mux, "GET /api/v1/streams", queryRoute(s, func(q *query) ([]any, func(any) any) {
		name, start, end := "xrange", q.str("start", "-"), q.str("end", "+")
		if q.bool("rev") {
			name, start, end = "xrevrange", q.str("end", "+"), q.str("start", "-")
		}

		return cmd(name, q.key("key"), start, end, "count", q.int("count", 100)), service.ShapeStreamEntries //nolint:mnd // default page
	}))
	//	@Summary		Stream length
	//	@Description	XLEN.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/len [get]
	s.api(mux, "GET /api/v1/streams/len", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("xlen", q.key("key")), nil
	}))
	//	@Summary		Stream info
	//	@Description	XINFO STREAM [FULL].
	//	@Tags			streams
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			full	query		bool	false	"Full info"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/info [get]
	s.api(mux, "GET /api/v1/streams/info", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("xinfo", "stream", q.key("key"), flag(q.bool("full"), "full")), service.ShapeMap
	}))
	//	@Summary		Consumer groups
	//	@Description	XINFO GROUPS.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/groups [get]
	s.api(mux, "GET /api/v1/streams/groups", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("xinfo", "groups", q.key("key")), service.ShapeMapList
	}))
	//	@Summary		Consumers of a group
	//	@Description	XINFO CONSUMERS.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			group	query		string	true	"Group"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/consumers [get]
	s.api(mux, "GET /api/v1/streams/consumers", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("xinfo", "consumers", q.key("key"), q.key("group")), service.ShapeMapList
	}))

	//	@Summary		Pending entries
	//	@Description	XPENDING summary; with start, end or count the entries, filtered by consumer and idle_ms.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			key			query		string	true	"Key"
	//	@Param			group		query		string	true	"Group"
	//	@Param			start		query		string	false	"Start id"
	//	@Param			end			query		string	false	"End id"
	//	@Param			count		query		int		false	"Number of entries"
	//	@Param			consumer	query		string	false	"Consumer"
	//	@Param			idle_ms		query		int		false	"Minimum idle time"
	//	@Success		200			{object}	Response{result=service.CommandResult}
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/pending [get]
	s.api(mux, "GET /api/v1/streams/pending", queryRoute(s, func(q *query) ([]any, func(any) any) {
		args := cmd("xpending", q.key("key"), q.key("group"))

		values := q.r.URL.Query()
		if !values.Has("start") && !values.Has("end") && !values.Has("count") {
			return args, nil
		}

		if values.Has("idle_ms") {
			args = append(args, "idle", q.int("idle_ms", 0))
		}

		args = append(args, q.str("start", "-"), q.str("end", "+"), q.int("count", 100)) //nolint:mnd // default page
		if consumer := q.str("consumer", ""); consumer != "" {
			args = append(args, consumer)
		}

		return args, nil
	}))

	//	@Summary		Add an entry
	//	@Description	XADD with optional MAXLEN/MINID trimming.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			body	StreamAddRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/add [post]
	s.api(mux, "POST /api/v1/streams/add", bodyRoute(s, func(_ *http.Request, b *StreamAddRequest) ([]any, func(any) any, error) {
		if len(b.Fields) == 0 {
			return nil, nil, badRequest("fields are required")
		}

		trim, err := trimArgs(b.MaxLen, b.MinID, b.Approx, b.Limit)
		if err != nil {
			return nil, nil, err
		}

		args := cmd("xadd", b.Key, flag(b.NoMkStream, "nomkstream"), trim, cmp.Or(b.ID, "*"))
		for _, f := range b.Fields {
			args = append(args, string(f.Field), string(f.Value))
		}

		return args, nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Delete entries
	//	@Description	XDEL.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			body	StreamIDsRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/delete [post]
	s.api(mux, "POST /api/v1/streams/delete", bodyRoute(s, func(_ *http.Request, b *StreamIDsRequest) ([]any, func(any) any, error) {
		if len(b.IDs) == 0 {
			return nil, nil, badRequest("ids are required")
		}

		return cmd("xdel", b.Key, b.IDs), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Trim a stream
	//	@Description	XTRIM by maxlen or minid.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			body	StreamTrimRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/trim [post]
	s.api(mux, "POST /api/v1/streams/trim", bodyRoute(s, func(_ *http.Request, b *StreamTrimRequest) ([]any, func(any) any, error) {
		trim, err := trimArgs(b.MaxLen, b.MinID, b.Approx, b.Limit)
		if err != nil || trim == nil {
			return nil, nil, cmpErr(err, badRequest("maxlen or minid is required"))
		}

		return cmd("xtrim", b.Key, trim), nil, required([]string{"key"}, b.Key)
	}))

	groupArgs := func(sub string, b *StreamGroupRequest) ([]any, func(any) any, error) {
		if b.Group == "" {
			return nil, nil, badRequest("group is required")
		}

		args := cmd("xgroup", sub, b.Key, b.Group, cmp.Or(b.ID, "$"))
		if sub == "create" {
			args = append(args, flag(b.MkStream, "mkstream")...)
		}

		if b.EntriesRead != nil {
			args = append(args, "entriesread", *b.EntriesRead)
		}

		return args, nil, required([]string{"key"}, b.Key)
	}

	//	@Summary		Create a group
	//	@Description	XGROUP CREATE.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			body	StreamGroupRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/groups [post]
	s.api(mux, "POST /api/v1/streams/groups", bodyRoute(s, func(_ *http.Request, b *StreamGroupRequest) ([]any, func(any) any, error) {
		return groupArgs("create", b)
	}))
	//	@Summary		Move a group
	//	@Description	XGROUP SETID.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			body	StreamGroupRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/groups [put]
	s.api(mux, "PUT /api/v1/streams/groups", bodyRoute(s, func(_ *http.Request, b *StreamGroupRequest) ([]any, func(any) any, error) {
		return groupArgs("setid", b)
	}))
	//	@Summary		Delete a group
	//	@Description	XGROUP DESTROY.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			group	query		string	true	"Group"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/groups [delete]
	s.api(mux, "DELETE /api/v1/streams/groups", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("xgroup", "destroy", q.key("key"), q.key("group")), nil
	}))

	//	@Summary		Create a consumer
	//	@Description	XGROUP CREATECONSUMER.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			body	StreamConsumerRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/consumers [post]
	s.api(mux, "POST /api/v1/streams/consumers", bodyRoute(s, func(_ *http.Request, b *StreamConsumerRequest) ([]any, func(any) any, error) {
		if b.Group == "" || b.Consumer == "" {
			return nil, nil, badRequest("group and consumer are required")
		}

		return cmd("xgroup", "createconsumer", b.Key, b.Group, b.Consumer), nil, required([]string{"key"}, b.Key)
	}))
	//	@Summary		Delete a consumer
	//	@Description	XGROUP DELCONSUMER.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			key			query		string	true	"Key"
	//	@Param			group		query		string	true	"Group"
	//	@Param			consumer	query		string	true	"Consumer"
	//	@Success		200			{object}	Response{result=service.CommandResult}
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/consumers [delete]
	s.api(mux, "DELETE /api/v1/streams/consumers", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("xgroup", "delconsumer", q.key("key"), q.key("group"), q.key("consumer")), nil
	}))

	// XREAD: {"keys", "ids" (default "0", from the start; "$" waits for new entries), "count", "block_ms"}.
	// XREADGROUP: the same with "group", "consumer", "noack" and ids defaulting to ">".
	readArgs := func(b *StreamReadRequest, group bool) ([]any, func(any) any, error) {
		if len(b.Keys) == 0 {
			return nil, nil, badRequest("keys are required")
		}

		def := "0"
		if group {
			def = ">"
		}

		ids := b.IDs
		if len(ids) == 0 {
			for range b.Keys {
				ids = append(ids, def)
			}
		}

		if len(ids) != len(b.Keys) {
			return nil, nil, badRequest("ids must match keys")
		}

		var args []any

		if group {
			if b.Group == "" || b.Consumer == "" {
				return nil, nil, badRequest("group and consumer are required")
			}

			args = cmd("xreadgroup", "group", b.Group, b.Consumer)
		} else {
			args = cmd("xread")
		}

		args = append(args, flag(b.Count > 0, "count", b.Count)...)

		if b.BlockMS != nil {
			args = append(args, "block", *b.BlockMS)
		}

		if group {
			args = append(args, flag(b.NoAck, "noack")...)
		}

		return cmd(args, "streams", b.Keys, ids), service.ShapeStreamRead, nil
	}

	//	@Summary		Read streams
	//	@Description	XREAD: ids default to "0" (from the start), "$" waits for new entries; block_ms waits, pass ?timeout accordingly. Result: [{"stream", "entries"}].
	//	@Tags			streams
	//	@Produce		json
	//	@Param			body	StreamReadRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/read [post]
	s.api(mux, "POST /api/v1/streams/read", bodyRoute(s, func(_ *http.Request, b *StreamReadRequest) ([]any, func(any) any, error) {
		return readArgs(b, false)
	}))
	//	@Summary		Read through a group
	//	@Description	XREADGROUP: ids default to ">" (new entries).
	//	@Tags			streams
	//	@Produce		json
	//	@Param			body	StreamReadRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/read-group [post]
	s.api(mux, "POST /api/v1/streams/read-group", bodyRoute(s, func(_ *http.Request, b *StreamReadRequest) ([]any, func(any) any, error) {
		return readArgs(b, true)
	}))

	//	@Summary		Acknowledge entries
	//	@Description	XACK.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			body	StreamIDsRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/ack [post]
	s.api(mux, "POST /api/v1/streams/ack", bodyRoute(s, func(_ *http.Request, b *StreamIDsRequest) ([]any, func(any) any, error) {
		if b.Group == "" || len(b.IDs) == 0 {
			return nil, nil, badRequest("group and ids are required")
		}

		return cmd("xack", b.Key, b.Group, b.IDs), nil, required([]string{"key"}, b.Key)
	}))

	claimShape := func(justID bool) func(any) any {
		if justID {
			return nil
		}

		return service.ShapeStreamEntries
	}

	//	@Summary		Claim entries
	//	@Description	XCLAIM.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			body	StreamClaimRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/claim [post]
	s.api(mux, "POST /api/v1/streams/claim", bodyRoute(s, func(_ *http.Request, b *StreamClaimRequest) ([]any, func(any) any, error) {
		if b.Group == "" || b.Consumer == "" || len(b.IDs) == 0 {
			return nil, nil, badRequest("group, consumer and ids are required")
		}

		args := cmd("xclaim", b.Key, b.Group, b.Consumer, b.MinIdleMS, b.IDs, flag(b.JustID, "justid"))

		return args, claimShape(b.JustID), required([]string{"key"}, b.Key)
	}))

	//	@Summary		Claim idle entries
	//	@Description	XAUTOCLAIM: {"next", "entries", "deleted"}.
	//	@Tags			streams
	//	@Produce		json
	//	@Param			body	StreamClaimRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/autoclaim [post]
	s.api(mux, "POST /api/v1/streams/autoclaim", bodyRoute(s, func(_ *http.Request, b *StreamClaimRequest) ([]any, func(any) any, error) {
		if b.Group == "" || b.Consumer == "" {
			return nil, nil, badRequest("group and consumer are required")
		}

		args := cmd("xautoclaim", b.Key, b.Group, b.Consumer, b.MinIdleMS, cmp.Or(b.Start, "0-0"))
		args = append(args, flag(b.Count > 0, "count", b.Count)...)
		args = append(args, flag(b.JustID, "justid")...)

		entries := claimShape(b.JustID)

		// [next start id, entries, deleted ids].
		return args, func(reply any) any {
			parts, ok := reply.([]any)
			if !ok || len(parts) < 2 { //nolint:mnd // next id and entries
				return reply
			}

			result := map[any]any{"next": parts[0], "entries": parts[1]}
			if entries != nil {
				result["entries"] = entries(parts[1])
			}

			if len(parts) > 2 { //nolint:mnd // deleted ids since redis 7
				result["deleted"] = parts[2]
			}

			return result
		}, required([]string{"key"}, b.Key)
	}))
}

type StreamAddRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Entry id, * by default.
	ID string `json:"id"`
	// Fields.
	Fields []FieldValue `json:"fields"`
	// Trim to this length.
	MaxLen *int64 `json:"maxlen"`
	// Trim entries with smaller ids.
	MinID string `json:"minid"`
	// Approximate trimming (~), more efficient.
	Approx bool `json:"approx"`
	// Maximum number of entries to evict per trim, requires approx.
	Limit int64 `json:"limit"`
	// Do not create a missing stream.
	NoMkStream bool `json:"nomkstream"`
}

type StreamIDsRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Consumer group.
	Group string `json:"group"`
	// Entry ids.
	IDs []string `json:"ids"`
}

type StreamTrimRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Trim to this length.
	MaxLen *int64 `json:"maxlen"`
	// Trim entries with smaller ids.
	MinID string `json:"minid"`
	// Approximate trimming (~), more efficient.
	Approx bool `json:"approx"`
	// Maximum number of entries to evict per trim, requires approx.
	Limit int64 `json:"limit"`
}

type StreamGroupRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Consumer group.
	Group string `json:"group"`
	// Last delivered id, $ (new entries only) by default, 0 for all.
	ID string `json:"id"`
	// Create a missing stream.
	MkStream bool `json:"mkstream"`
	// Entries read counter of the group, for lag tracking.
	EntriesRead *int64 `json:"entries_read"`
}

type StreamConsumerRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Consumer group.
	Group string `json:"group"`
	// Consumer.
	Consumer string `json:"consumer"`
}

type StreamReadRequest struct {
	// Keys.
	Keys []codec.Arg `json:"keys"`
	// Ids per key: 0 by default (from the start), $ for new entries; > by default with a group.
	IDs []string `json:"ids"`
	// Entries per stream.
	Count int64 `json:"count"`
	// Block for up to this many milliseconds waiting for data; pass ?timeout accordingly.
	BlockMS *int64 `json:"block_ms"`
	// Consumer group.
	Group string `json:"group"`
	// Consumer.
	Consumer string `json:"consumer"`
	// Do not add entries to the pending list.
	NoAck bool `json:"noack"`
}

type StreamClaimRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Consumer group.
	Group string `json:"group"`
	// Consumer.
	Consumer string `json:"consumer"`
	// Claim entries idle for at least this many milliseconds.
	MinIdleMS int64 `json:"min_idle_ms"`
	// Entry ids.
	IDs []string `json:"ids"`
	// XAUTOCLAIM start id, 0-0 by default.
	Start string `json:"start"`
	// Number of elements.
	Count int64 `json:"count"`
	// Return ids only.
	JustID bool `json:"justid"`
}
