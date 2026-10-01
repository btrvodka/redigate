package httpapi

import (
	"cmp"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/btrvodka/redigate/internal/codec"
)

// rangeArgs adds optional start/end/unit query parameters of BITCOUNT and BITPOS.
func rangeArgs(q *query, args []any) []any {
	if !q.r.URL.Query().Has("start") {
		return args
	}

	args = append(args, q.int("start", 0))
	if q.r.URL.Query().Has("end") {
		args = append(args, q.int("end", -1))

		if unit := q.str("unit", ""); unit != "" {
			args = append(args, unit)
		}
	}

	return args
}

func (s *Server) bitmapRoutes(mux *http.ServeMux) {
	//	@Summary		Get a bit
	//	@Description	GETBIT.
	//	@Tags			bitmaps
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			offset	query		int		false	"Bit offset"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/bitmaps/bit [get]
	s.api(mux, "GET /api/v1/bitmaps/bit", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("getbit", q.key("key"), q.int("offset", 0)), nil
	}))

	//	@Summary		Set a bit
	//	@Description	SETBIT.
	//	@Tags			bitmaps
	//	@Produce		json
	//	@Param			request	body		BitmapSetBitRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/bitmaps/bit [put]
	s.api(mux, "PUT /api/v1/bitmaps/bit", bodyRoute(s, func(_ *http.Request, b *BitmapSetBitRequest) ([]any, func(any) any, error) {
		if b.Value != 0 && b.Value != 1 {
			return nil, nil, badRequest("value must be 0 or 1")
		}

		return cmd("setbit", b.Key, b.Offset, b.Value), nil, required([]string{"key"}, b.Key)
	}))
	//	@Summary		Count set bits
	//	@Description	BITCOUNT.
	//	@Tags			bitmaps
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			start	query		int		false	"Start"
	//	@Param			end		query		int		false	"End"
	//	@Param			unit	query		string	false	"byte or bit"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/bitmaps/count [get]
	s.api(mux, "GET /api/v1/bitmaps/count", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return rangeArgs(q, cmd("bitcount", q.key("key"))), nil
	}))
	//	@Summary		Find a bit
	//	@Description	BITPOS.
	//	@Tags			bitmaps
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			bit		query		int		false	"0 or 1"
	//	@Param			start	query		int		false	"Start"
	//	@Param			end		query		int		false	"End"
	//	@Param			unit	query		string	false	"byte or bit"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/bitmaps/pos [get]
	s.api(mux, "GET /api/v1/bitmaps/pos", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return rangeArgs(q, cmd("bitpos", q.key("key"), q.int("bit", 1))), nil
	}))

	//	@Summary		Bitwise operation
	//	@Description	BITOP.
	//	@Tags			bitmaps
	//	@Produce		json
	//	@Param			request	body		BitmapOpRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/bitmaps/op [post]
	s.api(mux, "POST /api/v1/bitmaps/op", bodyRoute(s, func(_ *http.Request, b *BitmapOpRequest) ([]any, func(any) any, error) {
		if b.Op == "" || len(b.Keys) == 0 {
			return nil, nil, badRequest("op and keys are required")
		}

		return cmd("bitop", strings.ToUpper(b.Op), b.Destination, b.Keys), nil, required([]string{"destination"}, b.Destination)
	}))

	//	@Summary		Add to a HyperLogLog
	//	@Description	PFADD.
	//	@Tags			hyperloglog
	//	@Produce		json
	//	@Param			request	body		HLLAddRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hll/add [post]
	s.api(mux, "POST /api/v1/hll/add", bodyRoute(s, func(_ *http.Request, b *HLLAddRequest) ([]any, func(any) any, error) {
		return cmd("pfadd", b.Key, b.Elements), nil, required([]string{"key"}, b.Key)
	}))
	//	@Summary		Estimate cardinality
	//	@Description	PFCOUNT of the union; keys must hash to the same slot in cluster.
	//	@Tags			hyperloglog
	//	@Produce		json
	//	@Param			key		query		[]string	true	"Keys, repeat for several"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hll/count [get]
	s.api(mux, "GET /api/v1/hll/count", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("pfcount", q.keys("key")), nil
	}))

	//	@Summary		Merge HyperLogLogs
	//	@Description	PFMERGE.
	//	@Tags			hyperloglog
	//	@Produce		json
	//	@Param			request	body		HLLMergeRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/hll/merge [post]
	s.api(mux, "POST /api/v1/hll/merge", bodyRoute(s, func(_ *http.Request, b *HLLMergeRequest) ([]any, func(any) any, error) {
		return cmd("pfmerge", b.Destination, b.Keys), nil, required([]string{"destination"}, b.Destination)
	}))
}

type BitmapSetBitRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Offset.
	Offset int64 `json:"offset"`
	// Bit value.
	Value int `json:"value" enums:"0,1"`
}

type BitmapOpRequest struct {
	// Operation.
	Op string `json:"op" enums:"and,or,xor,not,diff,diff1,andor,one"`
	// Destination key.
	Destination codec.Arg `json:"destination"`
	// Keys.
	Keys []codec.Arg `json:"keys"`
}

type HLLAddRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Elements.
	Elements []codec.Arg `json:"elements"`
}

type HLLMergeRequest struct {
	// Destination key.
	Destination codec.Arg `json:"destination"`
	// Keys.
	Keys []codec.Arg `json:"keys"`
}

func (s *Server) geoRoutes(mux *http.ServeMux) {
	//	@Summary		Add locations
	//	@Description	GEOADD.
	//	@Tags			geo
	//	@Produce		json
	//	@Param			request	body		GeoAddRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/geo/add [post]
	s.api(mux, "POST /api/v1/geo/add", bodyRoute(s, func(_ *http.Request, b *GeoAddRequest) ([]any, func(any) any, error) {
		if len(b.Members) == 0 {
			return nil, nil, badRequest("members are required")
		}

		args := cmd("geoadd", b.Key, flag(b.NX, "nx"), flag(b.XX, "xx"), flag(b.CH, "ch"))
		for _, m := range b.Members {
			args = append(args, m.Longitude.String(), m.Latitude.String(), string(m.Member))
		}

		return args, nil, required([]string{"key"}, b.Key)
	}))
	//	@Summary		Coordinates
	//	@Description	GEOPOS.
	//	@Tags			geo
	//	@Produce		json
	//	@Param			key		query		string		true	"Key"
	//	@Param			member	query		[]string	true	"Members"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/geo/pos [get]
	s.api(mux, "GET /api/v1/geo/pos", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("geopos", q.key("key"), q.keys("member")), nil
	}))
	//	@Summary		Geohashes
	//	@Description	GEOHASH.
	//	@Tags			geo
	//	@Produce		json
	//	@Param			key		query		string		true	"Key"
	//	@Param			member	query		[]string	true	"Members"	collectionFormat(multi)
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/geo/hash [get]
	s.api(mux, "GET /api/v1/geo/hash", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("geohash", q.key("key"), q.keys("member")), nil
	}))
	//	@Summary		Distance
	//	@Description	GEODIST.
	//	@Tags			geo
	//	@Produce		json
	//	@Param			key		query		string	true	"Key"
	//	@Param			member1	query		string	true	"Member"
	//	@Param			member2	query		string	true	"Member"
	//	@Param			unit	query		string	false	"m, km, ft or mi"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/geo/dist [get]
	s.api(mux, "GET /api/v1/geo/dist", queryRoute(s, func(q *query) ([]any, func(any) any) {
		return cmd("geodist", q.key("key"), q.key("member1"), q.key("member2"), q.str("unit", "m")), nil
	}))

	//	@Summary		Search locations
	//	@Description	GEOSEARCH, or GEOSEARCHSTORE with destination: a center (from_member or longitude and latitude) and a shape (radius or width and height).
	//	@Tags			geo
	//	@Produce		json
	//	@Param			request	body		GeoSearchRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/geo/search [post]
	s.api(mux, "POST /api/v1/geo/search", bodyRoute(s, func(_ *http.Request, b *GeoSearchRequest) ([]any, func(any) any, error) {
		var args []any
		if b.Destination != "" {
			args = cmd("geosearchstore", b.Destination, b.Key)
		} else {
			args = cmd("geosearch", b.Key)
		}

		switch {
		case b.FromMember != "" && b.Longitude == "":
			args = cmd(args, "frommember", b.FromMember)
		case b.FromMember == "" && b.Longitude != "" && b.Latitude != "":
			args = append(args, "fromlonlat", b.Longitude.String(), b.Latitude.String())
		default:
			return nil, nil, badRequest("either from_member or longitude and latitude are required")
		}

		unit := cmp.Or(b.Unit, "m")

		switch {
		case b.Radius != "" && b.Width == "":
			args = append(args, "byradius", b.Radius.String(), unit)
		case b.Radius == "" && b.Width != "" && b.Height != "":
			args = append(args, "bybox", b.Width.String(), b.Height.String(), unit)
		default:
			return nil, nil, badRequest("either radius or width and height are required")
		}

		if b.Sort != "" {
			args = append(args, b.Sort)
		}

		if b.Count > 0 {
			args = append(args, "count", b.Count)
			args = append(args, flag(b.Any, "any")...)
		}

		if b.Destination != "" {
			return cmd(args, flag(b.StoreDist, "storedist")), nil, required([]string{"key"}, b.Key)
		}

		args = cmd(args, flag(b.WithCoord, "withcoord"), flag(b.WithDist, "withdist"), flag(b.WithHash, "withhash"))

		return args, nil, required([]string{"key"}, b.Key)
	}))
}

type GeoMember struct {
	// Member. A string, a number or {"base64": "..."}.
	Member codec.Arg `json:"member"`
	// Longitude.
	Longitude json.Number `json:"longitude" swaggertype:"number"`
	// Latitude.
	Latitude json.Number `json:"latitude" swaggertype:"number"`
}

type GeoAddRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Members.
	Members []GeoMember `json:"members"`
	// Only if the key or the element does not exist.
	NX bool `json:"nx"`
	// Only if the key or the element exists.
	XX bool `json:"xx"`
	// Return the number of changed elements.
	CH bool `json:"ch"`
}

type GeoSearchRequest struct {
	// Key. A string, a number or {"base64": "..."}.
	Key codec.Arg `json:"key"`
	// Center: an existing member.
	FromMember codec.Arg `json:"from_member"`
	// Center longitude, with latitude.
	Longitude json.Number `json:"longitude" swaggertype:"number"`
	// Center latitude, with longitude.
	Latitude json.Number `json:"latitude" swaggertype:"number"`
	// Search in a circle.
	Radius json.Number `json:"radius" swaggertype:"number"`
	// Search in a box, with height.
	Width json.Number `json:"width" swaggertype:"number"`
	// Box height, with width.
	Height json.Number `json:"height" swaggertype:"number"`
	// Distance unit, m by default.
	Unit string `json:"unit" enums:"m,km,ft,mi"`
	// Order by distance.
	Sort string `json:"sort" enums:"asc,desc"`
	// Number of elements.
	Count int64 `json:"count"`
	// Return as soon as count matches are found.
	Any bool `json:"any"`
	// Return coordinates.
	WithCoord bool `json:"with_coord"`
	// Return distances.
	WithDist bool `json:"with_dist"`
	// Return geohashes.
	WithHash bool `json:"with_hash"`
	// Store the result (GEOSEARCHSTORE).
	Destination codec.Arg `json:"destination"`
	// Store distances as scores.
	StoreDist bool `json:"store_dist"`
}
