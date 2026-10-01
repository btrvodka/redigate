package httpapi

import (
	"cmp"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/service"
)

type SeriesOptions struct {
	// Retention in milliseconds, 0 keeps samples forever.
	RetentionMS *int64 `json:"retention_ms"`
	// Chunk encoding.
	Encoding string `json:"encoding" enums:"compressed,uncompressed"`
	// Chunk size in bytes.
	ChunkSize int64 `json:"chunk_size"`
	// Policy for samples with an existing timestamp.
	DuplicatePolicy string `json:"duplicate_policy" enums:"block,first,last,min,max,sum"`
	// Labels for filters.
	Labels map[string]string `json:"labels"`
}

// args builds options shared by TS.CREATE, TS.ALTER and TS.ADD; alter has no ENCODING.
func (o SeriesOptions) args(duplicateKeyword string) []any {
	var args []any

	if o.RetentionMS != nil {
		args = append(args, "retention", *o.RetentionMS)
	}

	if o.Encoding != "" && duplicateKeyword != "" {
		args = append(args, "encoding", strings.ToUpper(o.Encoding))
	}

	if o.ChunkSize > 0 {
		args = append(args, "chunk_size", o.ChunkSize)
	}

	if o.DuplicatePolicy != "" {
		args = append(args, cmp.Or(duplicateKeyword, "duplicate_policy"), strings.ToUpper(o.DuplicatePolicy))
	}

	return append(args, labelsArgs(o.Labels)...)
}

func labelsArgs(labels map[string]string) []any {
	if len(labels) == 0 {
		return nil
	}

	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}

	slices.Sort(names)

	args := []any{"labels"}
	for _, name := range names {
		args = append(args, name, labels[name])
	}

	return args
}

// timestampArg accepts a JSON number (milliseconds) or "*" (server time).
func timestampArg(ts json.Number) string {
	return cmp.Or(ts.String(), "*")
}

// rangeQuery builds the options of TS.RANGE and TS.MRANGE from query parameters:
// ?from=-&to=+&count=&aggregation=avg&bucket_ms=60000&latest.
func rangeQuery(q *query) []any {
	args := []any{q.str("from", "-"), q.str("to", "+")}
	args = append(args, flag(q.bool("latest"), "latest")...)

	if q.r.URL.Query().Has("count") {
		args = append(args, "count", q.int("count", 0))
	}

	if aggregation := q.str("aggregation", ""); aggregation != "" {
		args = append(args, "aggregation", aggregation, q.int("bucket_ms", 0))
	}

	return args
}

//nolint:funlen // flat list of routes
func (s *Server) timeseriesRoutes(mux *http.ServeMux) {
	route := s.moduleRoutes(mux, "timeseries")

	//	@Summary		Create a series
	//	@Description	TS.CREATE.
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			request	body		TimeSeriesCreateRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries [post]
	route("POST /api/v1/timeseries", bodyRoute(s, func(_ *http.Request, b *TimeSeriesCreateRequest) ([]any, func(any) any, error) {
		return cmd("ts.create", b.Key, b.args("duplicate_policy")), nil, required([]string{"key"}, b.Key)
	}))
	//	@Summary		Change a series
	//	@Description	TS.ALTER.
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			request	body		TimeSeriesCreateRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries [put]
	route("PUT /api/v1/timeseries", bodyRoute(s, func(_ *http.Request, b *TimeSeriesCreateRequest) ([]any, func(any) any, error) {
		return cmd("ts.alter", b.Key, b.args("")), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Add a sample
	//	@Description	TS.ADD; the series is created with the given options when missing.
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			request	body		TimeSeriesAddRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries/add [post]
	route("POST /api/v1/timeseries/add", bodyRoute(s, func(_ *http.Request, b *TimeSeriesAddRequest) ([]any, func(any) any, error) {
		if b.Value == "" {
			return nil, nil, badRequest("value is required")
		}

		args := cmd("ts.add", b.Key, timestampArg(b.Timestamp), b.Value.String(), b.args("duplicate_policy"))
		if b.OnDuplicate != "" {
			args = append(args, "on_duplicate", strings.ToUpper(b.OnDuplicate))
		}

		return args, nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Add samples
	//	@Description	TS.ADD per sample in one pipeline; series may be in different cluster slots.
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			request	body		TimeSeriesMAddRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.PipelineResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries/madd [post]
	route("POST /api/v1/timeseries/madd", func(r *http.Request) (any, error) {
		var body TimeSeriesMAddRequest

		if err := bindJSON(r, &body); err != nil {
			return nil, err
		}

		commands := make([]codec.Args, 0, len(body.Samples))
		for _, sm := range body.Samples {
			commands = append(commands, cmd("ts.add", sm.Key, timestampArg(sm.Timestamp), sm.Value.String()))
		}

		opts, err := execOptions(r, execParams{})
		if err != nil {
			return nil, err
		}

		return s.svc.Pipeline(r.Context(), commands, service.PipelineOptions{ExecOptions: opts})
	})

	//	@Summary		Increment the last sample
	//	@Description	TS.INCRBY, or TS.DECRBY for a negative by.
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			request	body		TimeSeriesIncrRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries/incr [post]
	route("POST /api/v1/timeseries/incr", bodyRoute(s, func(_ *http.Request, b *TimeSeriesIncrRequest) ([]any, func(any) any, error) {
		by, _ := numberArg(b.By)
		name := "ts.incrby"

		if after, negative := strings.CutPrefix(by, "-"); negative {
			name, by = "ts.decrby", after
		}

		args := cmd(name, b.Key, by)
		if b.Timestamp != "" {
			args = append(args, "timestamp", b.Timestamp.String())
		}

		return args, nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Last sample
	//	@Description	TS.GET: {"timestamp", "value"}.
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			latest	query		bool	false	"LATEST"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries/get [get]
	route("GET /api/v1/timeseries/get", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("ts.get", q.key("key"), flag(q.bool("latest"), "latest")), service.ShapeSample
	}))
	//	@Summary		Series info
	//	@Description	TS.INFO.
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries/info [get]
	route("GET /api/v1/timeseries/info", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("ts.info", q.key("key")), service.ShapeMap
	}))
	//	@Summary		Samples of a series
	//	@Description	TS.RANGE or TS.REVRANGE: [{"timestamp", "value"}].
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			key			query		string	true	"Key"
	//	@Param			from		query		string	false	"Start timestamp, default -"
	//	@Param			to			query		string	false	"End timestamp, default +"
	//	@Param			count		query		int		false	"Number of samples"
	//	@Param			aggregation	query		string	false	"avg, sum, min, max, ..."
	//	@Param			bucket_ms	query		int		false	"Aggregation bucket"
	//	@Param			latest		query		bool	false	"LATEST"
	//	@Param			rev			query		bool	false	"Reverse order"
	//	@Success		200			{object}	Response{result=service.CommandResult}
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries/range [get]
	route("GET /api/v1/timeseries/range", queryRoute(s, func(q *query) ([]any, func(any) any) {
		name := "ts.range"
		if q.bool("rev") {
			name = "ts.revrange"
		}

		return cmd(name, q.key("key"), rangeQuery(q)), service.ShapeSamples
	}))

	//	@Summary		Samples of several series
	//	@Description	TS.MRANGE or TS.MREVRANGE by label filters: [{"key", "labels", "samples"}].
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			filter		query		[]string	true	"Label filters such as sensor=1"	collectionFormat(multi)
	//	@Param			with_labels	query		bool		false	"Include labels"
	//	@Param			from		query		string		false	"Start timestamp, default -"
	//	@Param			to			query		string		false	"End timestamp, default +"
	//	@Param			count		query		int			false	"Number of samples"
	//	@Param			aggregation	query		string		false	"avg, sum, min, max, ..."
	//	@Param			bucket_ms	query		int			false	"Aggregation bucket"
	//	@Param			latest		query		bool		false	"LATEST"
	//	@Param			rev			query		bool		false	"Reverse order"
	//	@Success		200			{object}	Response{result=service.CommandResult}
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries/mrange [get]
	route("GET /api/v1/timeseries/mrange", queryRoute(s, func(q *query) ([]any, func(any) any) {
		name := "ts.mrange"
		if q.bool("rev") {
			name = "ts.mrevrange"
		}

		return cmd(name, rangeQuery(q), flag(q.bool("with_labels"), "withlabels"), "filter", q.keys("filter")),
			service.ShapeTSMulti(false)
	}))
	//	@Summary		Last samples of several series
	//	@Description	TS.MGET by label filters: [{"key", "labels", "sample"}].
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			filter		query		[]string	true	"Label filters such as sensor=1"	collectionFormat(multi)
	//	@Param			with_labels	query		bool		false	"Include labels"
	//	@Param			latest		query		bool		false	"LATEST"
	//	@Success		200			{object}	Response{result=service.CommandResult}
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries/mget [get]
	route("GET /api/v1/timeseries/mget", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("ts.mget", flag(q.bool("latest"), "latest"), flag(q.bool("with_labels"), "withlabels"),
			"filter", q.keys("filter")), service.ShapeTSMulti(true)
	}))
	//	@Summary		Series by labels
	//	@Description	TS.QUERYINDEX.
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			filter	query		[]string	true	"Label filters such as sensor=1"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries/queryindex [get]
	route("GET /api/v1/timeseries/queryindex", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("ts.queryindex", q.keys("filter")), nil
	}))

	//	@Summary		Delete samples
	//	@Description	TS.DEL in [from, to].
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			request	body		TimeSeriesDeleteRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries/delete [post]
	route("POST /api/v1/timeseries/delete", bodyRoute(s, func(_ *http.Request, b *TimeSeriesDeleteRequest) ([]any, func(any) any, error) {
		return cmd("ts.del", b.Key, cmp.Or(b.From, "-"), cmp.Or(b.To, "+")), nil, required([]string{"key"}, b.Key)
	}))

	//	@Summary		Create a compaction rule
	//	@Description	TS.CREATERULE.
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			request	body		TimeSeriesRuleRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries/rules [post]
	route("POST /api/v1/timeseries/rules", bodyRoute(s, func(_ *http.Request, b *TimeSeriesRuleRequest) ([]any, func(any) any, error) {
		if b.Aggregation == "" || b.BucketMS <= 0 {
			return nil, nil, badRequest("aggregation and a positive bucket_ms are required")
		}

		return cmd("ts.createrule", b.Source, b.Destination, "aggregation", b.Aggregation, b.BucketMS), nil,
			required([]string{"source", "destination"}, b.Source, b.Destination)
	}))
	//	@Summary		Delete a compaction rule
	//	@Description	TS.DELETERULE.
	//	@Tags			timeseries
	//	@Produce		json
	//	@Param			source		query		string	true	"Source series"
	//	@Param			destination	query		string	true	"Destination series"
	//	@Success		200			{object}	Response{result=service.CommandResult}
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/timeseries/rules [delete]
	route("DELETE /api/v1/timeseries/rules", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("ts.deleterule", q.key("source"), q.key("destination")), nil
	}))
}

type TimeSeriesCreateRequest struct {
	SeriesOptions

	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
}

type TimeSeriesSample struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Unix milliseconds, the server time by default.
	Timestamp json.Number `json:"timestamp" swaggertype:"number"`
	// Sample value.
	Value json.Number `json:"value" swaggertype:"number"`
}

type TimeSeriesAddRequest struct {
	TimeSeriesSample
	SeriesOptions

	// Duplicate policy for this sample.
	OnDuplicate string `json:"on_duplicate" enums:"block,first,last,min,max,sum"`
}

type TimeSeriesIncrRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Increment, negative values decrement, 1 by default.
	By json.Number `json:"by" swaggertype:"number"`
	// Unix milliseconds, the server time by default.
	Timestamp json.Number `json:"timestamp" swaggertype:"number"`
}

type TimeSeriesDeleteRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Start timestamp, - by default.
	From string `json:"from"`
	// End timestamp, + by default.
	To string `json:"to"`
}

type TimeSeriesRuleRequest struct {
	// Source key.
	Source codec.Arg `json:"source"`
	// Destination key.
	Destination codec.Arg `json:"destination"`
	// Aggregation, e.g. avg, sum, min, max.
	Aggregation string `json:"aggregation"`
	// Bucket duration in milliseconds.
	BucketMS int64 `json:"bucket_ms"`
}

type TimeSeriesMAddRequest struct {
	// Samples.
	Samples []TimeSeriesSample `json:"samples"`
}
