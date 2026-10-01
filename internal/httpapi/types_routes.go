package httpapi

import (
	"cmp"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/service"
)

type KeyValue struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Value. A string, a number or {"base64": "..."}.
	Value codec.Arg `json:"value"`
}

type FieldValue struct {
	// Field. A string, a number or {"base64": "..."}.
	Field codec.Arg `json:"field"`
	// Value. A string, a number or {"base64": "..."}.
	Value codec.Arg `json:"value"`
}

type ScoredMember struct {
	// Member. A string, a number or {"base64": "..."}.
	Member codec.Arg `json:"member"`
	// Score: a number or "inf", "-inf".
	Score json.Number `json:"score" swaggertype:"number"`
}

// scanRoute pages through a collection with HSCAN, SSCAN or ZSCAN.
func scanRoute(s *Server, name string, items func(any) any) jsonHandler {
	return queryRoute(s, func(q *query) ([]any, func(any) any) {
		args := cmd(name, q.key("key"), q.str("cursor", "0"), "count", q.int("count", 100)) //nolint:mnd // default page
		if match := q.str("match", ""); match != "" {
			args = append(args, "match", match)
		}

		return args, service.ShapeScanPage(items)
	})
}

// side returns "l" or "r" for list commands.
func side(value string) (string, error) {
	switch strings.ToLower(value) {
	case "", "left":
		return "l", nil
	case "right":
		return "r", nil
	default:
		return "", badRequest("side must be left or right")
	}
}

func (s *Server) stringRoutes(mux *http.ServeMux) {
	//	@Summary		Get a string
	//	@Description	GET.
	//	@Tags			strings
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/strings [get]
	s.api(mux, "GET /api/v1/strings", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("get", q.key("key")), nil
	}))
	//	@Summary		Get several strings
	//	@Description	GET per key; keys may belong to different cluster slots.
	//	@Tags			strings
	//	@Produce		json
	//	@Param			key		query		[]string	true	"Keys, repeat for several"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.MultiKeyResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/strings/mget [get]
	s.api(mux, "GET /api/v1/strings/mget", func(r *http.Request) (any, error) {
		q := newQuery(r)
		keys := q.keys("key")

		if q.err != nil {
			return nil, q.err
		}

		opts, err := execOptions(r, execParams{})
		if err != nil {
			return nil, err
		}

		return s.svc.MultiKeyEncoded(r.Context(), "get", keys, nil, opts)
	})
	//	@Summary		String length
	//	@Description	STRLEN.
	//	@Tags			strings
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/strings/len [get]
	s.api(mux, "GET /api/v1/strings/len", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("strlen", q.key("key")), nil
	}))
	//	@Summary		Get a substring
	//	@Description	GETRANGE.
	//	@Tags			strings
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			start	query		int		false	"Start offset"
	//	@Param			end		query		int		false	"End offset, -1 is the last byte"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/strings/range [get]
	s.api(mux, "GET /api/v1/strings/range", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("getrange", q.key("key"), q.int("start", 0), q.int("end", -1)), nil
	}))

	//	@Summary		Set a string
	//	@Description	SET with optional TTL and NX/XX/KEEPTTL/GET.
	//	@Tags			strings
	//	@Produce		json
	//	@Param			request	body		StringSetRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/strings [put]
	s.api(mux, "PUT /api/v1/strings", bodyRoute(s, func(_ *http.Request, b *StringSetRequest) ([]any, func(any) any, error) {
		if err := required([]string{"key"}, b.Key); err != nil {
			return nil, nil, err
		}

		args := cmd("set", b.Key, b.Value, flag(b.NX, "nx"), flag(b.XX, "xx"), flag(b.KeepTTL, "keepttl"), flag(b.Get, "get"))
		args = append(args, flag(b.TTLMS > 0, "px", b.TTLMS)...)
		args = append(args, flag(b.ExpireAtMS > 0, "pxat", b.ExpireAtMS)...)

		return args, nil, nil
	}))

	//	@Summary		Set several strings
	//	@Description	SET per key in one pipeline; keys may belong to different cluster slots.
	//	@Tags			strings
	//	@Produce		json
	//	@Param			request	body		StringMSetRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.PipelineResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/strings/mset [post]
	s.api(mux, "POST /api/v1/strings/mset", func(r *http.Request) (any, error) {
		var body StringMSetRequest
		if err := bindJSON(r, &body); err != nil {
			return nil, err
		}

		commands := make([]codec.Args, 0, len(body.Items))
		for _, item := range body.Items {
			commands = append(commands, cmd("set", item.Key, item.Value, flag(body.NX, "nx")))
		}

		opts, err := execOptions(r, execParams{})
		if err != nil {
			return nil, err
		}

		return s.svc.Pipeline(r.Context(), commands, service.PipelineOptions{ExecOptions: opts})
	})

	//	@Summary		Increment a number
	//	@Description	INCRBY, or INCRBYFLOAT for a fractional by; by defaults to 1.
	//	@Tags			strings
	//	@Produce		json
	//	@Param			request	body		IncrRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/strings/incr [post]
	s.api(mux, "POST /api/v1/strings/incr", bodyRoute(s, func(_ *http.Request, b *IncrRequest) ([]any, func(any) any, error) {
		by, isFloat := numberArg(b.By)
		if isFloat {
			return cmd("incrbyfloat", b.Key, by), nil, required([]string{"key"}, b.Key)
		}

		return cmd("incrby", b.Key, by), nil, required([]string{"key"}, b.Key)
	}))
	//	@Summary		Append to a string
	//	@Description	APPEND.
	//	@Tags			strings
	//	@Produce		json
	//	@Param			request	body		KeyValue	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/strings/append [post]
	s.api(mux, "POST /api/v1/strings/append", bodyRoute(s, func(_ *http.Request, b *KeyValue) ([]any, func(any) any, error) {
		return cmd("append", b.Key, b.Value), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Overwrite a substring
	//	@Description	SETRANGE.
	//	@Tags			strings
	//	@Produce		json
	//	@Param			request	body		StringSetRangeRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/strings/range [put]
	s.api(mux, "PUT /api/v1/strings/range", bodyRoute(s, func(_ *http.Request, b *StringSetRangeRequest) ([]any, func(any) any, error) {
		return cmd("setrange", b.Key, b.Offset, b.Value), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Get and change TTL or delete
	//	@Description	GETEX with ttl_ms or persist, GETDEL with delete.
	//	@Tags			strings
	//	@Produce		json
	//	@Param			request	body		StringGetExRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/strings/getex [post]
	s.api(mux, "POST /api/v1/strings/getex", bodyRoute(s, func(_ *http.Request, b *StringGetExRequest) ([]any, func(any) any, error) {
		if b.Delete {
			return cmd("getdel", b.Key), nil, required([]string{"key"}, b.Key)
		}

		args := cmd("getex", b.Key, flag(b.Persist, "persist"))
		args = append(args, flag(b.TTLMS > 0, "px", b.TTLMS)...)

		return args, nil, required([]string{"key"}, b.Key)
	}))
}

type StringSetRequest struct {
	KeyValue

	// TTL in milliseconds.
	TTLMS int64 `json:"ttl_ms"`
	// Absolute expiration time, Unix milliseconds.
	ExpireAtMS int64 `json:"expire_at_ms"`
	// Only if the key or the element does not exist.
	NX bool `json:"nx"`
	// Only if the key or the element exists.
	XX bool `json:"xx"`
	// Keep the current TTL.
	KeepTTL bool `json:"keepttl"`
	// Return the previous value.
	Get bool `json:"get"`
}

type StringMSetRequest struct {
	// Items.
	Items []KeyValue `json:"items"`
	// Only if the key or the element does not exist.
	NX bool `json:"nx"`
}

type IncrRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Increment, 1 by default; a fractional value uses INCRBYFLOAT.
	By json.Number `json:"by" swaggertype:"number"`
}

type StringSetRangeRequest struct {
	KeyValue

	// Byte offset.
	Offset int64 `json:"offset"`
}

type StringGetExRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// TTL in milliseconds.
	TTLMS int64 `json:"ttl_ms"`
	// Remove the TTL.
	Persist bool `json:"persist"`
	// Delete the key (GETDEL).
	Delete bool `json:"delete"`
}

func (s *Server) hashRoutes(mux *http.ServeMux) {
	//	@Summary		Get a hash
	//	@Description	HGETALL.
	//	@Tags			hashes
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hashes [get]
	s.api(mux, "GET /api/v1/hashes", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("hgetall", q.key("key")), service.ShapeMap
	}))
	//	@Summary		Scan a hash
	//	@Description	HSCAN page: {"cursor", "items"}.
	//	@Tags			hashes
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			cursor	query		string	false	"Cursor"
	//	@Param			match	query		string	false	"Glob pattern"
	//	@Param			count	query		int		false	"Page size hint"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hashes/scan [get]
	s.api(mux, "GET /api/v1/hashes/scan", scanRoute(s, "hscan", service.ShapeMap))
	//	@Summary		Get hash fields
	//	@Description	HMGET.
	//	@Tags			hashes
	//	@Produce		json
	//	@Param			key		query		string		true	"Key"
	//	@Param			field	query		[]string	true	"Fields"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hashes/fields [get]
	s.api(mux, "GET /api/v1/hashes/fields", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("hmget", q.key("key"), q.keys("field")), nil
	}))
	//	@Summary		Hash length
	//	@Description	HLEN.
	//	@Tags			hashes
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hashes/len [get]
	s.api(mux, "GET /api/v1/hashes/len", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("hlen", q.key("key")), nil
	}))
	//	@Summary		Check a hash field
	//	@Description	HEXISTS.
	//	@Tags			hashes
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			field	query		string	true	"Field"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hashes/exists [get]
	s.api(mux, "GET /api/v1/hashes/exists", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("hexists", q.key("key"), q.key("field")), nil
	}))
	//	@Summary		Delete hash fields
	//	@Description	HDEL.
	//	@Tags			hashes
	//	@Produce		json
	//	@Param			key		query		string		true	"Key"
	//	@Param			field	query		[]string	true	"Fields"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hashes/fields [delete]
	s.api(mux, "DELETE /api/v1/hashes/fields", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("hdel", q.key("key"), q.keys("field")), nil
	}))

	//	@Summary		Set hash fields
	//	@Description	HSET, or HSETNX with nx and a single field.
	//	@Tags			hashes
	//	@Produce		json
	//	@Param			request	body		HashSetRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hashes [put]
	s.api(mux, "PUT /api/v1/hashes", bodyRoute(s, func(_ *http.Request, b *HashSetRequest) ([]any, func(any) any, error) {
		if len(b.Fields) == 0 {
			return nil, nil, badRequest("fields are required")
		}

		if b.NX {
			if len(b.Fields) != 1 {
				return nil, nil, badRequest("nx accepts a single field")
			}

			return cmd("hsetnx", b.Key, b.Fields[0].Field, b.Fields[0].Value), nil, required([]string{"key"}, b.Key)
		}

		args := cmd("hset", b.Key)
		for _, f := range b.Fields {
			args = append(args, string(f.Field), string(f.Value))
		}

		return args, nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Increment a hash field
	//	@Description	HINCRBY, or HINCRBYFLOAT for a fractional by.
	//	@Tags			hashes
	//	@Produce		json
	//	@Param			request	body		HashIncrRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hashes/incr [post]
	s.api(mux, "POST /api/v1/hashes/incr", bodyRoute(s, func(_ *http.Request, b *HashIncrRequest) ([]any, func(any) any, error) {
		name := "hincrby"
		by, isFloat := numberArg(b.By)

		if isFloat {
			name = "hincrbyfloat"
		}

		return cmd(name, b.Key, b.Field, by), nil, required([]string{"key", "field"}, b.Key, b.Field)
	}))

	//	@Summary		Expire hash fields
	//	@Description	HPEXPIRE (redis 7.4+).
	//	@Tags			hashes
	//	@Produce		json
	//	@Param			request	body		HashTTLRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hashes/ttl [put]
	s.api(mux, "PUT /api/v1/hashes/ttl", bodyRoute(s, func(_ *http.Request, b *HashTTLRequest) ([]any, func(any) any, error) {
		if len(b.Fields) == 0 || b.TTLMS <= 0 {
			return nil, nil, badRequest("fields and a positive ttl_ms are required")
		}

		args := cmd("hpexpire", b.Key, b.TTLMS)
		if b.Condition != "" {
			args = append(args, b.Condition)
		}

		return cmd(args, "fields", len(b.Fields), b.Fields), nil, required([]string{"key"}, b.Key)
	}))
	//	@Summary		TTL of hash fields
	//	@Description	HPTTL (redis 7.4+).
	//	@Tags			hashes
	//	@Produce		json
	//	@Param			key		query		string		true	"Key"
	//	@Param			field	query		[]string	true	"Fields"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hashes/ttl [get]
	s.api(mux, "GET /api/v1/hashes/ttl", queryRoute(s, func(q *query) ([]any, func(any) any) {
		fields := q.keys("field")

		return cmd("hpttl", q.key("key"), "fields", len(fields), fields), nil
	}))
}

type HashSetRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Fields.
	Fields []FieldValue `json:"fields"`
	// HSETNX: set a single field only if it does not exist.
	NX bool `json:"nx"`
}

type HashIncrRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Field. A string, a number or {"base64": "..."}.
	Field codec.Arg `json:"field"`
	// Increment, 1 by default; a fractional value uses HINCRBYFLOAT.
	By json.Number `json:"by" swaggertype:"number"`
}

type HashTTLRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Fields.
	Fields []codec.Arg `json:"fields"`
	// TTL in milliseconds.
	TTLMS int64 `json:"ttl_ms"`
	// Expiration condition.
	Condition string `json:"condition" enums:"NX,XX,GT,LT"`
}

func (s *Server) listRoutes(mux *http.ServeMux) {
	//	@Summary		Get list elements
	//	@Description	LRANGE.
	//	@Tags			lists
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			start	query		int		false	"Start index"
	//	@Param			stop	query		int		false	"Stop index, default 99"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/lists [get]
	s.api(mux, "GET /api/v1/lists", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("lrange", q.key("key"), q.int("start", 0), q.int("stop", 99)), nil //nolint:mnd // default page
	}))
	//	@Summary		List length
	//	@Description	LLEN.
	//	@Tags			lists
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/lists/len [get]
	s.api(mux, "GET /api/v1/lists/len", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("llen", q.key("key")), nil
	}))
	//	@Summary		Get an element by index
	//	@Description	LINDEX.
	//	@Tags			lists
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			index	query		int		false	"Index"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/lists/index [get]
	s.api(mux, "GET /api/v1/lists/index", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("lindex", q.key("key"), q.int("index", 0)), nil
	}))
	//	@Summary		Find an element
	//	@Description	LPOS.
	//	@Tags			lists
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			element	query		string	true	"Element"
	//	@Param			rank	query		int		false	"Rank"
	//	@Param			count	query		int		false	"Number of matches"
	//	@Param			maxlen	query		int		false	"Elements to scan"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/lists/pos [get]
	s.api(mux, "GET /api/v1/lists/pos", queryRoute(s, func(q *query) ([]any, func(any) any) {
		args := cmd("lpos", q.key("key"), q.key("element"))
		args = append(args, flag(q.r.URL.Query().Has("rank"), "rank", q.int("rank", 1))...)
		args = append(args, flag(q.r.URL.Query().Has("count"), "count", q.int("count", 0))...)
		args = append(args, flag(q.r.URL.Query().Has("maxlen"), "maxlen", q.int("maxlen", 0))...)

		return args, nil
	}))

	//	@Summary		Push elements
	//	@Description	LPUSH/RPUSH, or LPUSHX/RPUSHX with only_existing.
	//	@Tags			lists
	//	@Produce		json
	//	@Param			request	body		ListPushRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/lists/push [post]
	s.api(mux, "POST /api/v1/lists/push", bodyRoute(s, func(_ *http.Request, b *ListPushRequest) ([]any, func(any) any, error) {
		prefix, err := side(b.Side)
		if err != nil || len(b.Values) == 0 {
			return nil, nil, cmpErr(err, badRequest("values are required"))
		}

		name := prefix + "push"
		if b.OnlyExisting {
			name += "x"
		}

		return cmd(name, b.Key, b.Values), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Pop elements
	//	@Description	LPOP/RPOP; with block_ms BLPOP/BRPOP waits for an element, pass ?timeout accordingly.
	//	@Tags			lists
	//	@Produce		json
	//	@Param			request	body		ListPopRequest	true	"Request body"
	//	@Param			timeout	query		string			false	"Request timeout for blocking pops"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/lists/pop [post]
	s.api(mux, "POST /api/v1/lists/pop", bodyRoute(s, func(_ *http.Request, b *ListPopRequest) ([]any, func(any) any, error) {
		prefix, err := side(b.Side)
		if err != nil {
			return nil, nil, err
		}

		if b.BlockMS > 0 {
			if b.Count > 1 {
				return nil, nil, badRequest("count is not supported with block_ms")
			}

			return cmd("b"+prefix+"pop", b.Key, blockSeconds(b.BlockMS)), nil, required([]string{"key"}, b.Key)
		}

		return cmd(prefix+"pop", b.Key, flag(b.Count > 0, b.Count)), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Set an element by index
	//	@Description	LSET.
	//	@Tags			lists
	//	@Produce		json
	//	@Param			request	body		ListSetRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/lists/index [put]
	s.api(mux, "PUT /api/v1/lists/index", bodyRoute(s, func(_ *http.Request, b *ListSetRequest) ([]any, func(any) any, error) {
		return cmd("lset", b.Key, b.Index, b.Value), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Remove elements
	//	@Description	LREM: count 0 removes all occurrences, negative counts from the tail.
	//	@Tags			lists
	//	@Produce		json
	//	@Param			request	body		ListRemoveRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/lists/remove [post]
	s.api(mux, "POST /api/v1/lists/remove", bodyRoute(s, func(_ *http.Request, b *ListRemoveRequest) ([]any, func(any) any, error) {
		return cmd("lrem", b.Key, b.Count, b.Value), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Trim a list
	//	@Description	LTRIM.
	//	@Tags			lists
	//	@Produce		json
	//	@Param			request	body		ListTrimRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/lists/trim [post]
	s.api(mux, "POST /api/v1/lists/trim", bodyRoute(s, func(_ *http.Request, b *ListTrimRequest) ([]any, func(any) any, error) {
		return cmd("ltrim", b.Key, b.Start, b.Stop), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Insert an element
	//	@Description	LINSERT before or after the pivot.
	//	@Tags			lists
	//	@Produce		json
	//	@Param			request	body		ListInsertRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/lists/insert [post]
	s.api(mux, "POST /api/v1/lists/insert", bodyRoute(s, func(_ *http.Request, b *ListInsertRequest) ([]any, func(any) any, error) {
		position := strings.ToLower(b.Position)
		if position != "before" && position != "after" {
			return nil, nil, badRequest("position must be before or after")
		}

		return cmd("linsert", b.Key, position, b.Pivot, b.Value), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Move an element
	//	@Description	LMOVE, or BLMOVE with block_ms.
	//	@Tags			lists
	//	@Produce		json
	//	@Param			request	body		ListMoveRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/lists/move [post]
	s.api(mux, "POST /api/v1/lists/move", bodyRoute(s, func(_ *http.Request, b *ListMoveRequest) ([]any, func(any) any, error) {
		from, err1 := side(b.From)
		to, err2 := side(b.To)

		if err := cmpErr(err1, err2, required([]string{"source", "destination"}, b.Source, b.Destination)); err != nil {
			return nil, nil, err
		}

		names := map[string]string{"l": "left", "r": "right"}
		args := cmd(b.Source, b.Destination, names[from], names[to])

		if b.BlockMS > 0 {
			return cmd("blmove", args, blockSeconds(b.BlockMS)), nil, nil
		}

		return cmd("lmove", args), nil, nil
	}))
}

type ListPushRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Values: strings, numbers or {"base64": "..."}.
	Values []codec.Arg `json:"values"`
	// List end, left by default.
	Side string `json:"side" enums:"left,right"`
	// Push only to an existing list (LPUSHX/RPUSHX).
	OnlyExisting bool `json:"only_existing"`
}

type ListPopRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// List end, left by default.
	Side string `json:"side" enums:"left,right"`
	// Number of elements.
	Count int64 `json:"count"`
	// Block for up to this many milliseconds waiting for data; pass ?timeout accordingly.
	BlockMS int64 `json:"block_ms"`
}

type ListSetRequest struct {
	KeyValue

	// Index.
	Index int64 `json:"index"`
}

type ListRemoveRequest struct {
	KeyValue

	// Occurrences to remove: 0 for all, negative from the tail.
	Count int64 `json:"count"`
}

type ListTrimRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Start index.
	Start int64 `json:"start"`
	// Stop index.
	Stop int64 `json:"stop"`
}

type ListInsertRequest struct {
	KeyValue

	// Existing element.
	Pivot codec.Arg `json:"pivot"`
	// Insert before or after the pivot.
	Position string `json:"position" enums:"before,after"`
}

type ListMoveRequest struct {
	// Source key.
	Source codec.Arg `json:"source"`
	// Destination key.
	Destination codec.Arg `json:"destination"`
	// Pop from this end, left by default.
	From string `json:"from" enums:"left,right"`
	// Push to this end, left by default.
	To string `json:"to" enums:"left,right"`
	// Block for up to this many milliseconds waiting for data; pass ?timeout accordingly.
	BlockMS int64 `json:"block_ms"`
}

func (s *Server) setRoutes(mux *http.ServeMux) {
	//	@Summary		Get set members
	//	@Description	SMEMBERS.
	//	@Tags			sets
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/sets [get]
	s.api(mux, "GET /api/v1/sets", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("smembers", q.key("key")), nil
	}))
	//	@Summary		Scan a set
	//	@Description	SSCAN page: {"cursor", "items"}.
	//	@Tags			sets
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			cursor	query		string	false	"Cursor"
	//	@Param			match	query		string	false	"Glob pattern"
	//	@Param			count	query		int		false	"Page size hint"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/sets/scan [get]
	s.api(mux, "GET /api/v1/sets/scan", scanRoute(s, "sscan", nil))
	//	@Summary		Set size
	//	@Description	SCARD.
	//	@Tags			sets
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/sets/len [get]
	s.api(mux, "GET /api/v1/sets/len", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("scard", q.key("key")), nil
	}))
	//	@Summary		Check members
	//	@Description	SMISMEMBER.
	//	@Tags			sets
	//	@Produce		json
	//	@Param			key		query		string		true	"Key"
	//	@Param			member	query		[]string	true	"Members"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/sets/contains [get]
	s.api(mux, "GET /api/v1/sets/contains", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("smismember", q.key("key"), q.keys("member")), nil
	}))
	//	@Summary		Random members
	//	@Description	SRANDMEMBER.
	//	@Tags			sets
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			count	query		int		false	"Number of members"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/sets/random [get]
	s.api(mux, "GET /api/v1/sets/random", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("srandmember", q.key("key"), q.int("count", 1)), nil
	}))

	members := func(name string) jsonHandler {
		return bodyRoute(s, func(_ *http.Request, b *MembersRequest) ([]any, func(any) any, error) {
			if len(b.Members) == 0 {
				return nil, nil, badRequest("members are required")
			}

			return cmd(name, b.Key, b.Members), nil, required([]string{"key"}, b.Key)
		})
	}

	//	@Summary		Add members
	//	@Description	SADD.
	//	@Tags			sets
	//	@Produce		json
	//	@Param			request	body		MembersRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/sets/add [post]
	s.api(mux, "POST /api/v1/sets/add", members("sadd"))
	//	@Summary		Remove members
	//	@Description	SREM.
	//	@Tags			sets
	//	@Produce		json
	//	@Param			request	body		MembersRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/sets/remove [post]
	s.api(mux, "POST /api/v1/sets/remove", members("srem"))

	//	@Summary		Pop members
	//	@Description	SPOP.
	//	@Tags			sets
	//	@Produce		json
	//	@Param			request	body		CountRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/sets/pop [post]
	s.api(mux, "POST /api/v1/sets/pop", bodyRoute(s, func(_ *http.Request, b *CountRequest) ([]any, func(any) any, error) {
		return cmd("spop", b.Key, max(b.Count, 1)), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Move a member
	//	@Description	SMOVE.
	//	@Tags			sets
	//	@Produce		json
	//	@Param			request	body		SetMoveRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/sets/move [post]
	s.api(mux, "POST /api/v1/sets/move", bodyRoute(s, func(_ *http.Request, b *SetMoveRequest) ([]any, func(any) any, error) {
		return cmd("smove", b.Source, b.Destination, b.Member), nil,
			required([]string{"source", "destination"}, b.Source, b.Destination)
	}))

	// {"keys", "destination"}: without destination returns the result, with it stores it (S*STORE).
	for _, op := range []string{"inter", "union", "diff"} {
		//	@Summary		Set operation
		//	@Description	SINTER, SUNION or SDIFF; with destination the result is stored (S*STORE). In cluster keys must hash to the same slot.
		//	@Tags			sets
		//	@Produce		json
		//	@Param			request	body		KeysOpRequest	true	"Request body"
		//	@Success		200		{object}	Response{result=service.CommandResult}
		//	@Failure		default	{object}	ErrorResponse
		//	@Security		BearerAuth
		//	@Router			/sets/inter [post]
		//	@Router			/sets/union [post]
		//	@Router			/sets/diff [post]
		s.api(mux, "POST /api/v1/sets/"+op, bodyRoute(s, func(_ *http.Request, b *KeysOpRequest) ([]any, func(any) any, error) {
			if len(b.Keys) == 0 {
				return nil, nil, badRequest("keys are required")
			}

			if b.Destination != "" {
				return cmd("s"+op+"store", b.Destination, b.Keys), nil, nil
			}

			return cmd("s"+op, b.Keys), nil, nil
		}))
	}
}

type MembersRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Members.
	Members []codec.Arg `json:"members"`
}

type CountRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Number of elements.
	Count int64 `json:"count"`
}

type SetMoveRequest struct {
	// Source key.
	Source codec.Arg `json:"source"`
	// Destination key.
	Destination codec.Arg `json:"destination"`
	// Member. A string, a number or {"base64": "..."}.
	Member codec.Arg `json:"member"`
}

type KeysOpRequest struct {
	// Keys.
	Keys []codec.Arg `json:"keys"`
	// Store the result into this key.
	Destination codec.Arg `json:"destination"`
}

func (s *Server) zsetRoutes(mux *http.ServeMux) {
	//	@Summary		Get sorted set members
	//	@Description	ZRANGE by rank, score or lex, optionally reversed and limited; with_scores (default true) returns [{"member", "score"}].
	//	@Tags			sorted sets
	//	@Produce		json
	//	@Param			key			query		string	true	"Key"
	//	@Param			start		query		string	false	"Start: rank, score or lex boundary"
	//	@Param			stop		query		string	false	"Stop"
	//	@Param			by			query		string	false	"rank, score or lex"
	//	@Param			rev			query		bool	false	"Reverse order"
	//	@Param			offset		query		int		false	"LIMIT offset"
	//	@Param			count		query		int		false	"LIMIT count"
	//	@Param			with_scores	query		bool	false	"Include scores"
	//	@Success		200			{object}	Response{result=service.CommandResult}
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/zsets [get]
	s.api(mux, "GET /api/v1/zsets", queryRoute(s, func(q *query) ([]any, func(any) any) {
		args := cmd("zrange", q.key("key"), q.str("start", "0"), q.str("stop", "-1"))

		switch by := q.str("by", "rank"); by {
		case "rank":
		case "score", "lex":
			args = append(args, "by"+by)
		default:
			q.fail("by must be rank, score or lex")
		}

		args = append(args, flag(q.bool("rev"), "rev")...)

		if q.r.URL.Query().Has("count") {
			args = append(args, "limit", q.int("offset", 0), q.int("count", 0))
		}

		withScores := q.str("with_scores", "true") == "true"
		if !withScores {
			return args, nil
		}

		return append(args, "withscores"), service.ShapeScored
	}))
	//	@Summary		Scan a sorted set
	//	@Description	ZSCAN page: {"cursor", "items": [{"member", "score"}]}.
	//	@Tags			sorted sets
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			cursor	query		string	false	"Cursor"
	//	@Param			match	query		string	false	"Glob pattern"
	//	@Param			count	query		int		false	"Page size hint"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/zsets/scan [get]
	s.api(mux, "GET /api/v1/zsets/scan", scanRoute(s, "zscan", service.ShapeScored))
	//	@Summary		Sorted set size
	//	@Description	ZCARD, or ZCOUNT with min/max.
	//	@Tags			sorted sets
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			min		query		string	false	"Minimum score"
	//	@Param			max		query		string	false	"Maximum score"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/zsets/len [get]
	s.api(mux, "GET /api/v1/zsets/len", queryRoute(s, func(q *query) ([]any, func(any) any) {
		if q.r.URL.Query().Has("min") || q.r.URL.Query().Has("max") {
			return cmd("zcount", q.key("key"), q.str("min", "-inf"), q.str("max", "+inf")), nil
		}

		return cmd("zcard", q.key("key")), nil
	}))
	//	@Summary		Scores of members
	//	@Description	ZMSCORE.
	//	@Tags			sorted sets
	//	@Produce		json
	//	@Param			key		query		string		true	"Key"
	//	@Param			member	query		[]string	true	"Members"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/zsets/score [get]
	s.api(mux, "GET /api/v1/zsets/score", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("zmscore", q.key("key"), q.keys("member")), nil
	}))
	//	@Summary		Rank of a member
	//	@Description	ZRANK or ZREVRANK with rev.
	//	@Tags			sorted sets
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			member	query		string	true	"Member"
	//	@Param			rev		query		bool	false	"Reverse rank"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/zsets/rank [get]
	s.api(mux, "GET /api/v1/zsets/rank", queryRoute(s, func(q *query) ([]any, func(any) any) {
		name := "zrank"
		if q.bool("rev") {
			name = "zrevrank"
		}

		return cmd(name, q.key("key"), q.key("member")), nil
	}))

	//	@Summary		Add members
	//	@Description	ZADD with NX/XX/GT/LT/CH.
	//	@Tags			sorted sets
	//	@Produce		json
	//	@Param			request	body		ZSetAddRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/zsets/add [post]
	s.api(mux, "POST /api/v1/zsets/add", bodyRoute(s, func(_ *http.Request, b *ZSetAddRequest) ([]any, func(any) any, error) {
		if len(b.Members) == 0 {
			return nil, nil, badRequest("members are required")
		}

		args := cmd("zadd", b.Key, flag(b.NX, "nx"), flag(b.XX, "xx"), flag(b.GT, "gt"), flag(b.LT, "lt"), flag(b.CH, "ch"))
		for _, m := range b.Members {
			args = append(args, m.Score.String(), string(m.Member))
		}

		return args, nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Remove members
	//	@Description	ZREM.
	//	@Tags			sorted sets
	//	@Produce		json
	//	@Param			request	body		MembersRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/zsets/remove [post]
	s.api(mux, "POST /api/v1/zsets/remove", bodyRoute(s, func(_ *http.Request, b *MembersRequest) ([]any, func(any) any, error) {
		if len(b.Members) == 0 {
			return nil, nil, badRequest("members are required")
		}

		return cmd("zrem", b.Key, b.Members), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Increment a score
	//	@Description	ZINCRBY.
	//	@Tags			sorted sets
	//	@Produce		json
	//	@Param			request	body		ZSetIncrRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/zsets/incr [post]
	s.api(mux, "POST /api/v1/zsets/incr", bodyRoute(s, func(_ *http.Request, b *ZSetIncrRequest) ([]any, func(any) any, error) {
		by, _ := numberArg(b.By)

		return cmd("zincrby", b.Key, by, b.Member), nil, required([]string{"key", "member"}, b.Key, b.Member)
	}))

	//	@Summary		Pop members
	//	@Description	ZPOPMIN/ZPOPMAX, or BZPOPMIN/BZPOPMAX with block_ms.
	//	@Tags			sorted sets
	//	@Produce		json
	//	@Param			request	body		ZSetPopRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/zsets/pop [post]
	s.api(mux, "POST /api/v1/zsets/pop", bodyRoute(s, func(_ *http.Request, b *ZSetPopRequest) ([]any, func(any) any, error) {
		end := strings.ToLower(cmp.Or(b.Side, "min"))
		if end != "min" && end != "max" {
			return nil, nil, badRequest("side must be min or max")
		}

		if b.BlockMS > 0 {
			return cmd("bzpop"+end, b.Key, blockSeconds(b.BlockMS)), nil, required([]string{"key"}, b.Key)
		}

		return cmd("zpop"+end, b.Key, max(b.Count, 1)), service.ShapeScored, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Remove a range
	//	@Description	ZREMRANGEBYRANK, ZREMRANGEBYSCORE or ZREMRANGEBYLEX.
	//	@Tags			sorted sets
	//	@Produce		json
	//	@Param			request	body		ZSetRemoveRangeRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/zsets/remove-range [post]
	s.api(mux, "POST /api/v1/zsets/remove-range", bodyRoute(s, func(_ *http.Request, b *ZSetRemoveRangeRequest) ([]any, func(any) any, error) {
		by := strings.ToLower(cmp.Or(b.By, "rank"))
		if by != "rank" && by != "score" && by != "lex" {
			return nil, nil, badRequest("by must be rank, score or lex")
		}

		return cmd("zremrangeby"+by, b.Key, b.Min, b.Max), nil, required([]string{"key"}, b.Key)
	}))

	// {"keys", "weights", "aggregate": "sum|min|max", "destination"}.
	for _, op := range []string{"inter", "union", "diff"} {
		//	@Summary		Sorted set operation
		//	@Description	ZINTER, ZUNION or ZDIFF with weights and aggregate; with destination the result is stored (Z*STORE).
		//	@Tags			sorted sets
		//	@Produce		json
		//	@Param			request	body		ZSetOpRequest	true	"Request body"
		//	@Success		200		{object}	Response{result=service.CommandResult}
		//	@Failure		default	{object}	ErrorResponse
		//	@Security		BearerAuth
		//	@Router			/zsets/inter [post]
		//	@Router			/zsets/union [post]
		//	@Router			/zsets/diff [post]
		s.api(mux, "POST /api/v1/zsets/"+op, bodyRoute(s, func(_ *http.Request, b *ZSetOpRequest) ([]any, func(any) any, error) {
			if len(b.Keys) == 0 {
				return nil, nil, badRequest("keys are required")
			}

			var args []any
			if b.Destination != "" {
				args = cmd("z"+op+"store", b.Destination, len(b.Keys), b.Keys)
			} else {
				args = cmd("z"+op, len(b.Keys), b.Keys)
			}

			if len(b.Weights) > 0 {
				args = append(args, "weights")
				for _, w := range b.Weights {
					args = append(args, w.String())
				}
			}

			if b.Aggregate != "" {
				args = append(args, "aggregate", b.Aggregate)
			}

			if b.Destination != "" {
				return args, nil, nil
			}

			return append(args, "withscores"), service.ShapeScored, nil
		}))
	}
}

type ZSetAddRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Members.
	Members []ScoredMember `json:"members"`
	// Only if the key or the element does not exist.
	NX bool `json:"nx"`
	// Only if the key or the element exists.
	XX bool `json:"xx"`
	// Update only if the new score is greater.
	GT bool `json:"gt"`
	// Update only if the new score is less.
	LT bool `json:"lt"`
	// Return the number of changed elements.
	CH bool `json:"ch"`
}

type ZSetIncrRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Member. A string, a number or {"base64": "..."}.
	Member codec.Arg `json:"member"`
	// Increment, 1 by default.
	By json.Number `json:"by" swaggertype:"number"`
}

type ZSetPopRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Pop the lowest or the highest scores, min by default.
	Side string `json:"side" enums:"min,max"`
	// Number of elements.
	Count int64 `json:"count"`
	// Block for up to this many milliseconds waiting for data; pass ?timeout accordingly.
	BlockMS int64 `json:"block_ms"`
}

type ZSetRemoveRangeRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Range type, rank by default.
	By string `json:"by" enums:"rank,score,lex"`
	// Range minimum.
	Min string `json:"min"`
	// Range maximum.
	Max string `json:"max"`
}

type ZSetOpRequest struct {
	// Keys.
	Keys []codec.Arg `json:"keys"`
	// Weights of keys.
	Weights []json.Number `json:"weights" swaggertype:"array,number"`
	// Score aggregation, sum by default.
	Aggregate string `json:"aggregate" enums:"sum,min,max"`
	// Store the result into this key.
	Destination codec.Arg `json:"destination"`
}

// cmpErr returns the first non-nil error.
func cmpErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	return nil
}
