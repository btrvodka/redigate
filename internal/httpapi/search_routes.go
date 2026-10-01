package httpapi

import (
	"cmp"
	"net/http"
	"slices"
	"strings"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/service"
)

// Search commands have no keys: they go to one node. Redis 8 coordinates them
// across the cluster; for clusters without a coordinator pass ?target=masters
// to get per-shard results.

type SchemaField struct {
	// Attribute name or JSONPath.
	Field string `json:"field"`
	// Alias.
	As string `json:"as"`
	// Field type.
	Type string `json:"type" enums:"text,tag,numeric,geo,vector,geoshape"`
	// Extra field options, e.g. SORTABLE or vector parameters.
	Options []string `json:"options"`
}

func (f SchemaField) args() ([]any, error) {
	if f.Field == "" || f.Type == "" {
		return nil, badRequest("schema fields require field and type")
	}

	args := []any{f.Field}
	if f.As != "" {
		args = append(args, "as", f.As)
	}

	args = append(args, strings.ToUpper(f.Type))

	return append(args, codec.Strings(f.Options)...), nil
}

// paramsArgs builds PARAMS for query parameters, values may be binary (vectors).
func paramsArgs(params map[string]codec.Arg) []any {
	if len(params) == 0 {
		return nil
	}

	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}

	slices.Sort(names)

	args := []any{"params", len(params) * 2} //nolint:mnd // name and value
	for _, name := range names {
		args = append(args, name, string(params[name]))
	}

	return args
}

//nolint:funlen // flat list of routes
func (s *Server) searchRoutes(mux *http.ServeMux) {
	route := s.moduleRoutes(mux, "search")

	//	@Summary		List indexes
	//	@Description	FT._LIST.
	//	@Tags			search
	//	@Produce		json
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/search/indexes [get]
	route("GET /api/v1/search/indexes", func(r *http.Request) (any, error) {
		return s.call(r, nil, "ft._list")
	})
	//	@Summary		Index info
	//	@Description	FT.INFO.
	//	@Tags			search
	//	@Produce		json
	//	@Param			index	path		string	true	"Index"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/search/indexes/{index} [get]
	route("GET /api/v1/search/indexes/{index}", func(r *http.Request) (any, error) {
		return s.call(r, service.ShapeMap, "ft.info", r.PathValue("index"))
	})
	//	@Summary		Drop an index
	//	@Description	FT.DROPINDEX, with DD when delete_documents is set.
	//	@Tags			search
	//	@Produce		json
	//	@Param			index				path		string	true	"Index"
	//	@Param			delete_documents	query		bool	false	"Delete indexed documents"
	//	@Success		200					{object}	Response{result=service.CommandResult}
	//	@Failure		default				{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/search/indexes/{index} [delete]
	route("DELETE /api/v1/search/indexes/{index}", func(r *http.Request) (any, error) {
		return s.call(r, nil, cmd("ft.dropindex", r.PathValue("index"), flag(newQuery(r).bool("delete_documents"), "dd"))...)
	})

	//	@Summary		Create an index
	//	@Description	FT.CREATE.
	//	@Tags			search
	//	@Produce		json
	//	@Param			body	body		SearchCreateIndexRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/search/indexes [post]
	route("POST /api/v1/search/indexes", bodyRoute(s, func(_ *http.Request, b *SearchCreateIndexRequest) ([]any, func(any) any, error) {
		if b.Index == "" || len(b.Schema) == 0 {
			return nil, nil, badRequest("index and schema are required")
		}

		args := cmd("ft.create", b.Index, "on", strings.ToUpper(cmp.Or(b.On, "hash")))

		if len(b.Prefixes) > 0 {
			args = cmd(args, "prefix", len(b.Prefixes), b.Prefixes)
		}

		if b.Filter != "" {
			args = append(args, "filter", b.Filter)
		}

		if b.Language != "" {
			args = append(args, "language", b.Language)
		}

		if b.Stopwords != nil {
			args = cmd(args, "stopwords", len(*b.Stopwords), *b.Stopwords)
		}

		args = cmd(args, b.Args, "schema")

		for _, field := range b.Schema {
			fieldArgs, err := field.args()
			if err != nil {
				return nil, nil, err
			}

			args = append(args, fieldArgs...)
		}

		return args, nil, nil
	}))

	//	@Summary		Add a field
	//	@Description	FT.ALTER SCHEMA ADD.
	//	@Tags			search
	//	@Produce		json
	//	@Param			index	path		string		true	"Index"
	//	@Param			body	body		SchemaField	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/search/indexes/{index}/fields [post]
	route("POST /api/v1/search/indexes/{index}/fields", func(r *http.Request) (any, error) {
		var field SchemaField
		if err := bindJSON(r, &field); err != nil {
			return nil, err
		}

		fieldArgs, err := field.args()
		if err != nil {
			return nil, err
		}

		return s.call(r, nil, cmd("ft.alter", r.PathValue("index"), "schema", "add", fieldArgs)...)
	})

	//	@Summary		Search
	//	@Description	FT.SEARCH: {"total", "results": [{"id", "score", "fields"}]}.
	//	@Tags			search
	//	@Produce		json
	//	@Param			index	path		string			true	"Index"
	//	@Param			body	body		SearchRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/search/indexes/{index}/search [post]
	route("POST /api/v1/search/indexes/{index}/search", func(r *http.Request) (any, error) {
		var b SearchRequest
		if err := bindJSON(r, &b); err != nil {
			return nil, err
		}

		args := cmd("ft.search", r.PathValue("index"), cmp.Or(b.Query, "*"),
			flag(b.NoContent, "nocontent"), flag(b.WithScores, "withscores"))

		if len(b.Return) > 0 {
			args = cmd(args, "return", len(b.Return), b.Return)
		}

		if b.SortBy != "" {
			order := "asc"
			if b.SortDesc {
				order = "desc"
			}

			args = append(args, "sortby", b.SortBy, order)
		}

		if b.Limit != nil || b.Offset > 0 {
			limit := int64(10) //nolint:mnd // redis default
			if b.Limit != nil {
				limit = *b.Limit
			}

			args = append(args, "limit", b.Offset, limit)
		}

		args = cmd(args, paramsArgs(b.Params), b.Args)
		if b.Dialect > 0 {
			args = append(args, "dialect", b.Dialect)
		}

		return s.call(r, service.ShapeSearch(b.WithScores, b.NoContent), args...)
	})

	//	@Summary		Aggregate
	//	@Description	FT.AGGREGATE: {"total", "rows"}.
	//	@Tags			search
	//	@Produce		json
	//	@Param			index	path		string					true	"Index"
	//	@Param			body	body		SearchAggregateRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/search/indexes/{index}/aggregate [post]
	route("POST /api/v1/search/indexes/{index}/aggregate", func(r *http.Request) (any, error) {
		var b SearchAggregateRequest
		if err := bindJSON(r, &b); err != nil {
			return nil, err
		}

		args := cmd("ft.aggregate", r.PathValue("index"), cmp.Or(b.Query, "*"), b.Args, paramsArgs(b.Params))
		if b.Dialect > 0 {
			args = append(args, "dialect", b.Dialect)
		}

		return s.call(r, service.ShapeAggregate, args...)
	})

	//	@Summary		Explain a query
	//	@Description	FT.EXPLAIN.
	//	@Tags			search
	//	@Produce		json
	//	@Param			index	path		string	true	"Index"
	//	@Param			query	query		string	false	"Query, default *"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/search/indexes/{index}/explain [get]
	route("GET /api/v1/search/indexes/{index}/explain", func(r *http.Request) (any, error) {
		return s.call(r, nil, "ft.explain", r.PathValue("index"), cmp.Or(r.URL.Query().Get("query"), "*"))
	})
	//	@Summary		Tag values
	//	@Description	FT.TAGVALS.
	//	@Tags			search
	//	@Produce		json
	//	@Param			index	path		string	true	"Index"
	//	@Param			field	query		string	true	"Tag field"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/search/indexes/{index}/tagvals [get]
	route("GET /api/v1/search/indexes/{index}/tagvals", func(r *http.Request) (any, error) {
		q := newQuery(r)
		field := q.str("field", "")

		if field == "" {
			return nil, badRequest("field is required")
		}

		return s.call(r, nil, "ft.tagvals", r.PathValue("index"), field)
	})

	//	@Summary		Create or move an alias
	//	@Description	FT.ALIASUPDATE.
	//	@Tags			search
	//	@Produce		json
	//	@Param			body	body		SearchAliasRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/search/aliases [put]
	route("PUT /api/v1/search/aliases", bodyRoute(s, func(_ *http.Request, b *SearchAliasRequest) ([]any, func(any) any, error) {
		if b.Alias == "" || b.Index == "" {
			return nil, nil, badRequest("alias and index are required")
		}

		return cmd("ft.aliasupdate", b.Alias, b.Index), nil, nil
	}))
	//	@Summary		Delete an alias
	//	@Description	FT.ALIASDEL.
	//	@Tags			search
	//	@Produce		json
	//	@Param			alias	path		string	true	"Alias"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/search/aliases/{alias} [delete]
	route("DELETE /api/v1/search/aliases/{alias}", func(r *http.Request) (any, error) {
		return s.call(r, nil, "ft.aliasdel", r.PathValue("alias"))
	})
}

type SearchCreateIndexRequest struct {
	// Index.
	Index string `json:"index"`
	// Indexed data type, hash by default.
	On string `json:"on" enums:"hash,json"`
	// Key prefixes to index.
	Prefixes []string `json:"prefixes"`
	// Filter expression for documents.
	Filter string `json:"filter"`
	// Default language for stemming.
	Language string `json:"language"`
	// Stop words, an empty list disables them.
	Stopwords *[]string `json:"stopwords"`
	// Other FT.CREATE options placed before SCHEMA.
	Args []string `json:"args"`
	// Indexed fields.
	Schema []SchemaField `json:"schema"`
}

type SearchRequest struct {
	// Query, * by default.
	Query string `json:"query"`
	// Fields to return.
	Return []string `json:"return"`
	// Sortable field to order by.
	SortBy string `json:"sort_by"`
	// Descending order.
	SortDesc bool `json:"sort_desc"`
	// Offset.
	Offset int64 `json:"offset"`
	// Number of results, 10 by default.
	Limit *int64 `json:"limit"`
	// Query parameters referenced as $name, values may be binary (vectors).
	Params map[string]codec.Arg `json:"params"`
	// Query dialect.
	Dialect int `json:"dialect"`
	// Return document ids only.
	NoContent bool `json:"no_content"`
	// Return relevance scores.
	WithScores bool `json:"with_scores"`
	// Other FT.SEARCH options.
	Args []string `json:"args"`
}

type SearchAggregateRequest struct {
	// Query, * by default.
	Query string `json:"query"`
	// Aggregation pipeline, e.g. ["GROUPBY", "1", "@city", "REDUCE", "COUNT", "0", "AS", "n"].
	Args []string `json:"args"`
	// Query parameters referenced as $name, values may be binary (vectors).
	Params map[string]codec.Arg `json:"params"`
	// Query dialect.
	Dialect int `json:"dialect"`
}

type SearchAliasRequest struct {
	// Alias.
	Alias string `json:"alias"`
	// Index.
	Index string `json:"index"`
}
