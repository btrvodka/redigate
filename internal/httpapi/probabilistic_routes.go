package httpapi

import (
	"cmp"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/service"
)

type ItemsRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Items.
	Items []codec.Arg `json:"items"`
}

func (b *ItemsRequest) validate() error {
	if len(b.Items) == 0 {
		return badRequest("items are required")
	}

	return required([]string{"key"}, b.Key)
}

type ItemIncrement struct {
	// Item. A string, a number or {"base64": "..."}.
	Item codec.Arg `json:"item"`
	// Increment.
	Increment int64 `json:"increment"`
}

type MergeRequest struct {
	// Destination key.
	Destination codec.Arg `json:"destination"`
	// Source keys.
	Sources []codec.Arg `json:"sources"`
	// Weights of sources.
	Weights []int64 `json:"weights"`
}

func numbers[T ~string](values []T) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = string(v)
	}

	return out
}

//nolint:funlen // flat list of routes
func (s *Server) bloomRoutes(mux *http.ServeMux) {
	route := s.moduleRoutes(mux, "bloom")

	//	@Summary		Create a Bloom filter
	//	@Description	BF.RESERVE.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		BloomReserveRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/bloom [post]
	route("POST /api/v1/bloom", bodyRoute(s, func(_ *http.Request, b *BloomReserveRequest) ([]any, func(any) any, error) {
		if b.ErrorRate == "" || b.Capacity <= 0 {
			return nil, nil, badRequest("error_rate and a positive capacity are required")
		}

		args := cmd("bf.reserve", b.Key, b.ErrorRate.String(), b.Capacity, flag(b.Expansion > 0, "expansion", b.Expansion),
			flag(b.NonScaling, "nonscaling"))

		return args, nil, required([]string{"key"}, b.Key)
	}))
	//	@Summary		Add items
	//	@Description	BF.MADD.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		ItemsRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/bloom/add [post]
	route("POST /api/v1/bloom/add", bodyRoute(s, func(_ *http.Request, b *ItemsRequest) ([]any, func(any) any, error) {
		return cmd("bf.madd", b.Key, b.Items), nil, b.validate()
	}))
	//	@Summary		Check items
	//	@Description	BF.MEXISTS.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			key		query		string		true	"Key"
	//	@Param			item	query		[]string	true	"Items"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/bloom/exists [get]
	route("GET /api/v1/bloom/exists", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("bf.mexists", q.key("key"), q.keys("item")), nil
	}))
	//	@Summary		Bloom filter cardinality
	//	@Description	BF.CARD.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/bloom/card [get]
	route("GET /api/v1/bloom/card", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("bf.card", q.key("key")), nil
	}))
	//	@Summary		Bloom filter info
	//	@Description	BF.INFO.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/bloom/info [get]
	route("GET /api/v1/bloom/info", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("bf.info", q.key("key")), service.ShapeMap
	}))

	cuckoo := s.moduleRoutes(mux, "cuckoo")

	//	@Summary		Create a Cuckoo filter
	//	@Description	CF.RESERVE.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		CuckooReserveRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cuckoo [post]
	cuckoo("POST /api/v1/cuckoo", bodyRoute(s, func(_ *http.Request, b *CuckooReserveRequest) ([]any, func(any) any, error) {
		if b.Capacity <= 0 {
			return nil, nil, badRequest("a positive capacity is required")
		}

		args := cmd("cf.reserve", b.Key, b.Capacity, flag(b.BucketSize > 0, "bucketsize", b.BucketSize),
			flag(b.MaxIterations > 0, "maxiterations", b.MaxIterations), flag(b.Expansion > 0, "expansion", b.Expansion))

		return args, nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Add items
	//	@Description	CF.INSERT, or CF.INSERTNX with nx.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		CuckooAddRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cuckoo/add [post]
	cuckoo("POST /api/v1/cuckoo/add", bodyRoute(s, func(_ *http.Request, b *CuckooAddRequest) ([]any, func(any) any, error) {
		name := "cf.insert"
		if b.NX {
			name = "cf.insertnx"
		}

		return cmd(name, b.Key, "items", b.Items), nil, b.validate()
	}))
	//	@Summary		Check items
	//	@Description	CF.MEXISTS.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			key		query		string		true	"Key"
	//	@Param			item	query		[]string	true	"Items"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cuckoo/exists [get]
	cuckoo("GET /api/v1/cuckoo/exists", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("cf.mexists", q.key("key"), q.keys("item")), nil
	}))
	//	@Summary		Count an item
	//	@Description	CF.COUNT.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			item	query		string	true	"Item"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cuckoo/count [get]
	cuckoo("GET /api/v1/cuckoo/count", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("cf.count", q.key("key"), q.key("item")), nil
	}))

	//	@Summary		Delete an item
	//	@Description	CF.DEL.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		CuckooItemRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cuckoo/delete [post]
	cuckoo("POST /api/v1/cuckoo/delete", bodyRoute(s, func(_ *http.Request, b *CuckooItemRequest) ([]any, func(any) any, error) {
		return cmd("cf.del", b.Key, b.Item), nil, required([]string{"key", "item"}, b.Key, b.Item)
	}))
	//	@Summary		Cuckoo filter info
	//	@Description	CF.INFO.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cuckoo/info [get]
	cuckoo("GET /api/v1/cuckoo/info", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("cf.info", q.key("key")), service.ShapeMap
	}))
}

type BloomReserveRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// False positive rate, e.g. 0.01.
	ErrorRate json.Number `json:"error_rate" swaggertype:"number"`
	// Expected number of items.
	Capacity int64 `json:"capacity"`
	// Growth factor of sub-filters.
	Expansion int64 `json:"expansion"`
	// Do not add sub-filters when full.
	NonScaling bool `json:"nonscaling"`
}

type CuckooReserveRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Capacity.
	Capacity int64 `json:"capacity"`
	// Items per bucket.
	BucketSize int64 `json:"bucket_size"`
	// Swap attempts before declaring the filter full.
	MaxIterations int64 `json:"max_iterations"`
	// Growth factor of sub-filters.
	Expansion int64 `json:"expansion"`
}

type CuckooAddRequest struct {
	ItemsRequest

	// Add only absent items (CF.INSERTNX).
	NX bool `json:"nx"`
}

type CuckooItemRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Item. A string, a number or {"base64": "..."}.
	Item codec.Arg `json:"item"`
}

//nolint:funlen // flat list of routes
func (s *Server) sketchRoutes(mux *http.ServeMux) {
	cms := s.moduleRoutes(mux, "cms")

	//	@Summary		Create a Count-Min Sketch
	//	@Description	CMS.INITBYDIM with width and depth or CMS.INITBYPROB with error and probability.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		CMSInitRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cms [post]
	cms("POST /api/v1/cms", bodyRoute(s, func(_ *http.Request, b *CMSInitRequest) ([]any, func(any) any, error) {
		switch {
		case b.Width > 0 && b.Depth > 0:
			return cmd("cms.initbydim", b.Key, b.Width, b.Depth), nil, required([]string{"key"}, b.Key)
		case b.Error != "" && b.Probability != "":
			return cmd("cms.initbyprob", b.Key, b.Error.String(), b.Probability.String()), nil, required([]string{"key"}, b.Key)
		default:
			return nil, nil, badRequest("width and depth or error and probability are required")
		}
	}))

	incrArgs := func(name string, b *ItemIncrementsRequest) ([]any, error) {
		if len(b.Items) == 0 {
			return nil, badRequest("items are required")
		}

		args := cmd(name, b.Key)
		for _, item := range b.Items {
			args = append(args, string(item.Item), item.Increment)
		}

		return args, required([]string{"key"}, b.Key)
	}

	//	@Summary		Increment items
	//	@Description	CMS.INCRBY.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		ItemIncrementsRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cms/incr [post]
	cms("POST /api/v1/cms/incr", bodyRoute(s, func(_ *http.Request, b *ItemIncrementsRequest) ([]any, func(any) any, error) {
		args, err := incrArgs("cms.incrby", b)

		return args, nil, err
	}))
	//	@Summary		Item counts
	//	@Description	CMS.QUERY.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			key		query		string		true	"Key"
	//	@Param			item	query		[]string	true	"Items"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cms/query [get]
	cms("GET /api/v1/cms/query", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("cms.query", q.key("key"), q.keys("item")), nil
	}))
	//	@Summary		Sketch info
	//	@Description	CMS.INFO.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cms/info [get]
	cms("GET /api/v1/cms/info", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("cms.info", q.key("key")), service.ShapeMap
	}))
	//	@Summary		Merge sketches
	//	@Description	CMS.MERGE.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		MergeRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/cms/merge [post]
	cms("POST /api/v1/cms/merge", bodyRoute(s, func(_ *http.Request, b *MergeRequest) ([]any, func(any) any, error) {
		if len(b.Sources) == 0 {
			return nil, nil, badRequest("sources are required")
		}

		args := cmd("cms.merge", b.Destination, len(b.Sources), b.Sources)
		if len(b.Weights) > 0 {
			args = append(args, "weights")
			for _, w := range b.Weights {
				args = append(args, w)
			}
		}

		return args, nil, required([]string{"destination"}, b.Destination)
	}))

	topk := s.moduleRoutes(mux, "topk")

	//	@Summary		Create a Top-K
	//	@Description	TOPK.RESERVE.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		TopKReserveRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/topk [post]
	topk("POST /api/v1/topk", bodyRoute(s, func(_ *http.Request, b *TopKReserveRequest) ([]any, func(any) any, error) {
		if b.K <= 0 {
			return nil, nil, badRequest("a positive k is required")
		}

		args := cmd("topk.reserve", b.Key, b.K)
		if b.Width > 0 {
			args = append(args, b.Width, b.Depth, cmp.Or(b.Decay.String(), "0.9"))
		}

		return args, nil, required([]string{"key"}, b.Key)
	}))
	//	@Summary		Add items
	//	@Description	TOPK.ADD: items pushed out of the top.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		ItemsRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/topk/add [post]
	topk("POST /api/v1/topk/add", bodyRoute(s, func(_ *http.Request, b *ItemsRequest) ([]any, func(any) any, error) {
		return cmd("topk.add", b.Key, b.Items), nil, b.validate()
	}))
	//	@Summary		Increment items
	//	@Description	TOPK.INCRBY.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		ItemIncrementsRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/topk/incr [post]
	topk("POST /api/v1/topk/incr", bodyRoute(s, func(_ *http.Request, b *ItemIncrementsRequest) ([]any, func(any) any, error) {
		args, err := incrArgs("topk.incrby", b)

		return args, nil, err
	}))
	//	@Summary		Top items
	//	@Description	TOPK.LIST, with with_count [{"item", "count"}].
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			key			query		string	true	"Key"
	//	@Param			with_count	query		bool	false	"Include counts"
	//	@Success		200			{object}	Response{result=service.CommandResult}
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/topk/list [get]
	topk("GET /api/v1/topk/list", queryRoute(s, func(q *query) ([]any, func(any) any) {
		if q.bool("with_count") {
			return cmd("topk.list", q.key("key"), "withcount"), shapeItemCounts
		}

		return cmd("topk.list", q.key("key")), nil
	}))
	//	@Summary		Check items
	//	@Description	TOPK.QUERY.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			key		query		string		true	"Key"
	//	@Param			item	query		[]string	true	"Items"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/topk/query [get]
	topk("GET /api/v1/topk/query", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("topk.query", q.key("key"), q.keys("item")), nil
	}))
	//	@Summary		Top-K info
	//	@Description	TOPK.INFO.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/topk/info [get]
	topk("GET /api/v1/topk/info", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("topk.info", q.key("key")), service.ShapeMap
	}))

	s.tdigestRoutes(s.moduleRoutes(mux, "tdigest"))
}

type CMSInitRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Counters per row, with depth.
	Width int64 `json:"width"`
	// Depth.
	Depth int64 `json:"depth"`
	// Estimation error, with probability, alternative to width and depth.
	Error json.Number `json:"error" swaggertype:"number"`
	// Probability of exceeding the error.
	Probability json.Number `json:"probability" swaggertype:"number"`
}

type ItemIncrementsRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Items.
	Items []ItemIncrement `json:"items"`
}

type TopKReserveRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Number of top items.
	K int64 `json:"k"`
	// Width.
	Width int64 `json:"width"`
	// Depth.
	Depth int64 `json:"depth"`
	// Decay probability, 0.9 by default; width and depth must be set.
	Decay json.Number `json:"decay" swaggertype:"number"`
}

func (s *Server) tdigestRoutes(route func(string, jsonHandler)) {
	//	@Summary		Create a t-digest
	//	@Description	TDIGEST.CREATE.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		TDigestCreateRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/tdigest [post]
	route("POST /api/v1/tdigest", bodyRoute(s, func(_ *http.Request, b *TDigestCreateRequest) ([]any, func(any) any, error) {
		return cmd("tdigest.create", b.Key, flag(b.Compression > 0, "compression", b.Compression)), nil,
			required([]string{"key"}, b.Key)
	}))

	//	@Summary		Add values
	//	@Description	TDIGEST.ADD.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		TDigestAddRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/tdigest/add [post]
	route("POST /api/v1/tdigest/add", bodyRoute(s, func(_ *http.Request, b *TDigestAddRequest) ([]any, func(any) any, error) {
		if len(b.Values) == 0 {
			return nil, nil, badRequest("values are required")
		}

		return cmd("tdigest.add", b.Key, numbers(b.Values)), nil, required([]string{"key"}, b.Key)
	}))

	// Commands with a list of numbers in the query: ?key&q=0.5&q=0.99.
	for path, spec := range map[string][2]string{
		"quantile":  {"tdigest.quantile", "q"},
		"cdf":       {"tdigest.cdf", "value"},
		"rank":      {"tdigest.rank", "value"},
		"revrank":   {"tdigest.revrank", "value"},
		"byrank":    {"tdigest.byrank", "rank"},
		"byrevrank": {"tdigest.byrevrank", "rank"},
	} {
		//	@Summary		Quantiles and ranks
		//	@Description	TDIGEST.QUANTILE (q), CDF, RANK and REVRANK (value), BYRANK and BYREVRANK (rank).
		//	@Tags			probabilistic
		//	@Produce		json
		//	@Param			key		query		string		true	"Key"
		//	@Param			q		query		[]string	false	"Quantiles for /quantile"			collectionFormat(multi)
		//	@Param			value	query		[]string	false	"Values for /cdf, /rank, /revrank"	collectionFormat(multi)
		//	@Param			rank	query		[]string	false	"Ranks for /byrank, /byrevrank"		collectionFormat(multi)
		//	@Success		200		{object}	Response{result=service.CommandResult}
		//	@Failure		default	{object}	ErrorResponse
		//	@Security		BearerAuth
		//	@Router			/tdigest/quantile [get]
		//	@Router			/tdigest/cdf [get]
		//	@Router			/tdigest/rank [get]
		//	@Router			/tdigest/revrank [get]
		//	@Router			/tdigest/byrank [get]
		//	@Router			/tdigest/byrevrank [get]
		route("GET /api/v1/tdigest/"+path, queryRoute(s, func(q *query) ([]any, func(any) any) {
			values := q.r.URL.Query()[spec[1]]
			if len(values) == 0 {
				q.fail("%s is required", spec[1])
			}

			return cmd(spec[0], q.key("key"), values), nil
		}))
	}

	for _, name := range []string{"min", "max", "info"} {
		//	@Summary		Minimum, maximum and info
		//	@Description	TDIGEST.MIN, TDIGEST.MAX or TDIGEST.INFO.
		//	@Tags			probabilistic
		//	@Produce		json
		//	@Param			key		query		string	true	"Key"
		//	@Success		200		{object}	Response{result=service.CommandResult}
		//	@Failure		default	{object}	ErrorResponse
		//	@Security		BearerAuth
		//	@Router			/tdigest/min [get]
		//	@Router			/tdigest/max [get]
		//	@Router			/tdigest/info [get]
		route("GET /api/v1/tdigest/"+name, queryRoute(s, func(q *query) ([]any, func(any) any) {
			if name == "info" {
				return cmd("tdigest.info", q.key("key")), service.ShapeMap
			}

			return cmd("tdigest."+name, q.key("key")), nil
		}))
	}

	//	@Summary		Trimmed mean
	//	@Description	TDIGEST.TRIMMED_MEAN.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			low		query		string	false	"Low cut quantile, default 0.1"
	//	@Param			high	query		string	false	"High cut quantile, default 0.9"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/tdigest/trimmed-mean [get]
	route("GET /api/v1/tdigest/trimmed-mean", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("tdigest.trimmed_mean", q.key("key"), q.str("low", "0.1"), q.str("high", "0.9")), nil
	}))

	//	@Summary		Reset a t-digest
	//	@Description	TDIGEST.RESET.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		KeyRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/tdigest/reset [post]
	route("POST /api/v1/tdigest/reset", bodyRoute(s, func(_ *http.Request, b *KeyRequest) ([]any, func(any) any, error) {
		return cmd("tdigest.reset", b.Key), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Merge t-digests
	//	@Description	TDIGEST.MERGE.
	//	@Tags			probabilistic
	//	@Produce		json
	//	@Param			body	body		TDigestMergeRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/tdigest/merge [post]
	route("POST /api/v1/tdigest/merge", bodyRoute(s, func(_ *http.Request, b *TDigestMergeRequest) ([]any, func(any) any, error) {
		if len(b.Sources) == 0 {
			return nil, nil, badRequest("sources are required")
		}

		args := cmd("tdigest.merge", b.Destination, len(b.Sources), b.Sources,
			flag(b.Compression > 0, "compression", b.Compression), flag(b.Override, "override"))

		return args, nil, required([]string{"destination"}, b.Destination)
	}))
}

type TDigestCreateRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Accuracy versus size, 100 by default.
	Compression int64 `json:"compression"`
}

type TDigestAddRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Observations.
	Values []json.Number `json:"values" swaggertype:"array,number"`
}

type KeyRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
}

type TDigestMergeRequest struct {
	MergeRequest

	// Compression of the destination.
	Compression int64 `json:"compression"`
	// Replace the destination instead of merging into it.
	Override bool `json:"override"`
}

// shapeItemCounts converts TOPK.LIST WITHCOUNT [item, count, ...] to [{"item", "count"}].
func shapeItemCounts(reply any) any {
	items, ok := reply.([]any)
	if !ok {
		return reply
	}

	out := make([]any, 0, len(items)/2) //nolint:mnd // pairs
	for i := 0; i+1 < len(items); i += 2 {
		out = append(out, map[any]any{"item": items[i], "count": items[i+1]})
	}

	return out
}

//nolint:funlen // flat list of routes
func (s *Server) vectorsetRoutes(mux *http.ServeMux) {
	route := s.moduleRoutes(mux, "vectorset")

	//	@Summary		Add a vector
	//	@Description	VADD with optional REDUCE, quantization, EF, M and attributes.
	//	@Tags			vector sets
	//	@Produce		json
	//	@Param			body	body		VectorAddRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/vectorsets/add [post]
	route("POST /api/v1/vectorsets/add", bodyRoute(s, func(_ *http.Request, b *VectorAddRequest) ([]any, func(any) any, error) {
		if len(b.Vector) == 0 {
			return nil, nil, badRequest("vector is required")
		}

		args := cmd("vadd", b.Key, flag(b.Reduce > 0, "reduce", b.Reduce), vectorArgs(b.Vector), b.Element, flag(b.CAS, "cas"))
		if b.Quantization != "" {
			args = append(args, strings.ToUpper(b.Quantization))
		}

		args = cmd(args, flag(b.EF > 0, "ef", b.EF), flag(b.M > 0, "m", b.M))

		if len(b.Attributes) > 0 {
			attributes, err := compactJSON("attributes", b.Attributes)
			if err != nil {
				return nil, nil, err
			}

			args = append(args, "setattr", attributes)
		}

		return args, nil, required([]string{"key", "element"}, b.Key, b.Element)
	}))

	//	@Summary		Similar elements
	//	@Description	VSIM by vector or element: [{"element", "score"}] ordered by score.
	//	@Tags			vector sets
	//	@Produce		json
	//	@Param			body	body		VectorSearchRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/vectorsets/search [post]
	route("POST /api/v1/vectorsets/search", bodyRoute(s, func(_ *http.Request, b *VectorSearchRequest) ([]any, func(any) any, error) {
		args := cmd("vsim", b.Key)

		switch {
		case len(b.Vector) > 0 && b.Element == "":
			args = append(args, vectorArgs(b.Vector)...)
		case len(b.Vector) == 0 && b.Element != "":
			args = cmd(args, "ele", b.Element)
		default:
			return nil, nil, badRequest("either vector or element is required")
		}

		args = cmd(args, flag(b.Count > 0, "count", b.Count), flag(b.EF > 0, "ef", b.EF), flag(b.Filter != "", "filter", b.Filter))

		if b.WithScores != nil && !*b.WithScores {
			return args, nil, required([]string{"key"}, b.Key)
		}

		return append(args, "withscores"), service.ShapeElementScores, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Remove an element
	//	@Description	VREM.
	//	@Tags			vector sets
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			element	query		string	true	"Element"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/vectorsets/elements [delete]
	route("DELETE /api/v1/vectorsets/elements", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("vrem", q.key("key"), q.key("element")), nil
	}))

	for path, name := range map[string]string{"card": "vcard", "dim": "vdim", "info": "vinfo"} {
		//	@Summary		Size, dimension and info
		//	@Description	VCARD, VDIM or VINFO.
		//	@Tags			vector sets
		//	@Produce		json
		//	@Param			key		query		string	true	"Key"
		//	@Success		200		{object}	Response{result=service.CommandResult}
		//	@Failure		default	{object}	ErrorResponse
		//	@Security		BearerAuth
		//	@Router			/vectorsets/card [get]
		//	@Router			/vectorsets/dim [get]
		//	@Router			/vectorsets/info [get]
		route("GET /api/v1/vectorsets/"+path, queryRoute(s, func(q *query) ([]any, func(any) any) {
			if name == "vinfo" {
				return cmd(name, q.key("key")), service.ShapeMap
			}

			return cmd(name, q.key("key")), nil
		}))
	}

	//	@Summary		Vector of an element
	//	@Description	VEMB.
	//	@Tags			vector sets
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			element	query		string	true	"Element"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/vectorsets/embedding [get]
	route("GET /api/v1/vectorsets/embedding", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("vemb", q.key("key"), q.key("element")), toFloats
	}))
	//	@Summary		Attributes of an element
	//	@Description	VGETATTR, parsed JSON.
	//	@Tags			vector sets
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			element	query		string	true	"Element"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/vectorsets/attributes [get]
	route("GET /api/v1/vectorsets/attributes", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("vgetattr", q.key("key"), q.key("element")), service.ShapeJSON
	}))

	//	@Summary		Set attributes
	//	@Description	VSETATTR; an empty object removes attributes.
	//	@Tags			vector sets
	//	@Produce		json
	//	@Param			body	body		VectorAttributesRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/vectorsets/attributes [put]
	route("PUT /api/v1/vectorsets/attributes", bodyRoute(s, func(_ *http.Request, b *VectorAttributesRequest) ([]any, func(any) any, error) {
		attributes, err := compactJSON("attributes", b.Attributes)
		if err != nil {
			return nil, nil, err
		}

		if attributes == "{}" {
			attributes = ""
		}

		return cmd("vsetattr", b.Key, b.Element, attributes), nil, required([]string{"key", "element"}, b.Key, b.Element)
	}))
	//	@Summary		Random elements
	//	@Description	VRANDMEMBER.
	//	@Tags			vector sets
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			count	query		int		false	"Number of elements"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/vectorsets/random [get]
	route("GET /api/v1/vectorsets/random", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("vrandmember", q.key("key"), q.int("count", 1)), nil
	}))
	//	@Summary		HNSW links
	//	@Description	VLINKS with scores.
	//	@Tags			vector sets
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			element	query		string	true	"Element"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/vectorsets/links [get]
	route("GET /api/v1/vectorsets/links", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("vlinks", q.key("key"), q.key("element"), "withscores"), nil
	}))
}

type VectorAddRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Element name.
	Element codec.Arg `json:"element"`
	// Vector components.
	Vector []float64 `json:"vector"`
	// Random projection to this number of dimensions.
	Reduce int64 `json:"reduce"`
	// Quantization, q8 by default.
	Quantization string `json:"quantization" enums:"noquant,q8,bin"`
	// Build exploration factor.
	EF int64 `json:"ef"`
	// Maximum number of links per node.
	M int64 `json:"m"`
	// JSON attributes used by FILTER.
	Attributes json.RawMessage `json:"attributes" swaggertype:"object"`
	// Add the element in a background thread.
	CAS bool `json:"cas"`
}

type VectorSearchRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Query vector, alternative to element.
	Vector []float64 `json:"vector"`
	// Query by an existing element.
	Element codec.Arg `json:"element"`
	// Number of elements.
	Count int64 `json:"count"`
	// Exploration factor.
	EF int64 `json:"ef"`
	// Filter expression on attributes, e.g. .year > 2000.
	Filter string `json:"filter"`
	// Return scores, true by default.
	WithScores *bool `json:"with_scores"`
}

type VectorAttributesRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Element name.
	Element codec.Arg `json:"element"`
	// JSON attributes, {} removes them.
	Attributes json.RawMessage `json:"attributes" swaggertype:"object"`
}

func vectorArgs(vector []float64) []any {
	args := make([]any, 0, len(vector)+2) //nolint:mnd // VALUES and the dimension
	args = append(args, "values", len(vector))

	for _, v := range vector {
		args = append(args, strconv.FormatFloat(v, 'g', -1, 64))
	}

	return args
}

// toFloats converts VEMB replies (strings in RESP2, doubles in RESP3) to numbers.
func toFloats(reply any) any {
	items, ok := reply.([]any)
	if !ok {
		return reply
	}

	out := make([]any, len(items))

	for i, item := range items {
		out[i] = item

		if s, ok := item.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				out[i] = f
			}
		}
	}

	return out
}
