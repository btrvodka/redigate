package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/service"
)

// moduleRoutes registers routes that require a module capability: without it they answer 501.
func (s *Server) moduleRoutes(mux *http.ServeMux, capability string) func(pattern string, h jsonHandler) {
	return func(pattern string, h jsonHandler) {
		s.api(mux, pattern, func(r *http.Request) (any, error) {
			if err := s.svc.RequireCapability(capability); err != nil {
				return nil, err //nolint:wrapcheck // mapped by the API
			}

			return h(r)
		})
	}
}

// compactJSON validates a raw JSON body field and returns it as a single line.
func compactJSON(name string, raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", badRequest("%s is required", name)
	}

	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "", badRequest("invalid %s: %v", name, err)
	}

	return buf.String(), nil
}

func (s *Server) jsonRoutes(mux *http.ServeMux) {
	route := s.moduleRoutes(mux, "json")

	//	@Summary		Get a JSON value
	//	@Description	JSON.GET; values are returned as parsed JSON.
	//	@Tags			json
	//	@Produce		json
	//	@Param			key		query		string		true	"Key"
	//	@Param			path	query		[]string	false	"JSONPaths"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/json [get]
	route("GET /api/v1/json", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("json.get", q.key("key"), q.r.URL.Query()["path"]), service.ShapeJSON
	}))

	//	@Summary		Get JSON values of several keys
	//	@Description	JSON.GET per key; keys may belong to different cluster slots.
	//	@Tags			json
	//	@Produce		json
	//	@Param			key		query		[]string	true	"Keys, repeat for several"	collectionFormat(multi)
	//	@Param			path	query		string		false	"JSONPath, default $"
	//	@Success		200		{object}	Response{result=service.MultiKeyResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/json/mget [get]
	route("GET /api/v1/json/mget", func(r *http.Request) (any, error) {
		q := newQuery(r)
		keys, path := q.keys("key"), q.str("path", "$")

		if q.err != nil {
			return nil, q.err
		}

		opts, err := execOptions(r, execParams{})
		if err != nil {
			return nil, err
		}

		opts.Shape = service.ShapeJSON

		return s.svc.MultiKeyEncoded(r.Context(), "json.get", keys, []any{path}, opts)
	})

	//	@Summary		JSON type
	//	@Description	JSON.TYPE.
	//	@Tags			json
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			path	query		string	false	"JSONPath"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/json/type [get]
	route("GET /api/v1/json/type", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("json.type", q.key("key"), q.str("path", "$")), nil
	}))
	//	@Summary		JSON array length
	//	@Description	JSON.ARRLEN.
	//	@Tags			json
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			path	query		string	false	"JSONPath"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/json/arrlen [get]
	route("GET /api/v1/json/arrlen", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("json.arrlen", q.key("key"), q.str("path", "$")), nil
	}))
	//	@Summary		JSON object keys
	//	@Description	JSON.OBJKEYS.
	//	@Tags			json
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			path	query		string	false	"JSONPath"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/json/objkeys [get]
	route("GET /api/v1/json/objkeys", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("json.objkeys", q.key("key"), q.str("path", "$")), nil
	}))
	//	@Summary		Delete a JSON value
	//	@Description	JSON.DEL.
	//	@Tags			json
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			path	query		string	false	"JSONPath, default $"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/json [delete]
	route("DELETE /api/v1/json", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("json.del", q.key("key"), q.str("path", "$")), nil
	}))

	//	@Summary		Set a JSON value
	//	@Description	JSON.SET.
	//	@Tags			json
	//	@Produce		json
	//	@Param			body	body		JSONSetRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/json [put]
	route("PUT /api/v1/json", bodyRoute(s, func(_ *http.Request, b *JSONSetRequest) ([]any, func(any) any, error) {
		value, err := compactJSON("value", b.Value)
		if err != nil {
			return nil, nil, err
		}

		return cmd("json.set", b.Key, jsonPath(b.Path), value, flag(b.NX, "nx"), flag(b.XX, "xx")), nil,
			required([]string{"key"}, b.Key)
	}))

	//	@Summary		Merge a JSON value
	//	@Description	JSON.MERGE (RFC 7386 merge patch).
	//	@Tags			json
	//	@Produce		json
	//	@Param			body	body		JSONSetRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/json/merge [post]
	route("POST /api/v1/json/merge", bodyRoute(s, func(_ *http.Request, b *JSONSetRequest) ([]any, func(any) any, error) {
		value, err := compactJSON("value", b.Value)
		if err != nil {
			return nil, nil, err
		}

		return cmd("json.merge", b.Key, jsonPath(b.Path), value), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Increment a JSON number
	//	@Description	JSON.NUMINCRBY.
	//	@Tags			json
	//	@Produce		json
	//	@Param			body	body		JSONNumIncrRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/json/numincr [post]
	route("POST /api/v1/json/numincr", bodyRoute(s, func(_ *http.Request, b *JSONNumIncrRequest) ([]any, func(any) any, error) {
		by, _ := numberArg(b.By)

		return cmd("json.numincrby", b.Key, jsonPath(b.Path), by), service.ShapeJSON, required([]string{"key"}, b.Key)
	}))

	arrayValues := func(b *JSONArrayRequest) ([]any, error) {
		if len(b.Values) == 0 {
			return nil, badRequest("values are required")
		}

		out := make([]any, 0, len(b.Values))

		for _, raw := range b.Values {
			value, err := compactJSON("values", raw)
			if err != nil {
				return nil, err
			}

			out = append(out, value)
		}

		return out, nil
	}

	//	@Summary		Append to JSON arrays
	//	@Description	JSON.ARRAPPEND.
	//	@Tags			json
	//	@Produce		json
	//	@Param			body	body		JSONArrayRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/json/arrappend [post]
	route("POST /api/v1/json/arrappend", bodyRoute(s, func(_ *http.Request, b *JSONArrayRequest) ([]any, func(any) any, error) {
		values, err := arrayValues(b)

		return cmd("json.arrappend", b.Key, jsonPath(b.Path), values), nil, cmpErr(err, required([]string{"key"}, b.Key))
	}))
	//	@Summary		Insert into JSON arrays
	//	@Description	JSON.ARRINSERT.
	//	@Tags			json
	//	@Produce		json
	//	@Param			body	body		JSONArrayRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/json/arrinsert [post]
	route("POST /api/v1/json/arrinsert", bodyRoute(s, func(_ *http.Request, b *JSONArrayRequest) ([]any, func(any) any, error) {
		values, err := arrayValues(b)
		if b.Index == nil {
			err = cmpErr(err, badRequest("index is required"))
		}

		if err != nil {
			return nil, nil, err
		}

		return cmd("json.arrinsert", b.Key, jsonPath(b.Path), *b.Index, values), nil, required([]string{"key"}, b.Key)
	}))
	//	@Summary		Pop from JSON arrays
	//	@Description	JSON.ARRPOP.
	//	@Tags			json
	//	@Produce		json
	//	@Param			body	body		JSONArrayRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/json/arrpop [post]
	route("POST /api/v1/json/arrpop", bodyRoute(s, func(_ *http.Request, b *JSONArrayRequest) ([]any, func(any) any, error) {
		args := cmd("json.arrpop", b.Key, jsonPath(b.Path))
		if b.Index != nil {
			args = append(args, *b.Index)
		}

		return args, decodeEach, required([]string{"key"}, b.Key)
	}))

	for name, command := range map[string]string{"toggle": "json.toggle", "clear": "json.clear"} {
		//	@Summary		Toggle or clear JSON values
		//	@Description	JSON.TOGGLE or JSON.CLEAR.
		//	@Tags			json
		//	@Produce		json
		//	@Param			body	body		JSONPathRequest	true	"Request body"
		//	@Success		200		{object}	Response{result=service.CommandResult}
		//	@Failure		default	{object}	ErrorResponse
		//	@Security		BearerAuth
		//	@Router			/json/toggle [post]
		//	@Router			/json/clear [post]
		route("POST /api/v1/json/"+name, bodyRoute(s, func(_ *http.Request, b *JSONPathRequest) ([]any, func(any) any, error) {
			return cmd(command, b.Key, jsonPath(b.Path)), nil, required([]string{"key"}, b.Key)
		}))
	}
}

type JSONSetRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// JSONPath, $ by default.
	Path string `json:"path"`
	// Any JSON value.
	Value json.RawMessage `json:"value" swaggertype:"object"`
	// Only if the key or the element does not exist.
	NX bool `json:"nx"`
	// Only if the key or the element exists.
	XX bool `json:"xx"`
}

type JSONNumIncrRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// JSONPath, $ by default.
	Path string `json:"path"`
	// Increment, 1 by default.
	By json.Number `json:"by" swaggertype:"number"`
}

type JSONArrayRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// JSONPath, $ by default.
	Path string `json:"path"`
	// Array index: required by arrinsert, the last element for arrpop by default.
	Index *int64 `json:"index"`
	// JSON values.
	Values []json.RawMessage `json:"values" swaggertype:"array,object"`
}

type JSONPathRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// JSONPath, $ by default.
	Path string `json:"path"`
}

// jsonPath returns the JSONPath or the root.
func jsonPath(path string) string {
	if path == "" {
		return "$"
	}

	return path
}

// decodeEach decodes JSON strings in an array reply (JSON.ARRPOP with a JSONPath).
func decodeEach(reply any) any {
	items, ok := reply.([]any)
	if !ok {
		return service.ShapeJSON(reply)
	}

	out := make([]any, len(items))
	for i, item := range items {
		out[i] = service.ShapeJSON(item)
	}

	return out
}
