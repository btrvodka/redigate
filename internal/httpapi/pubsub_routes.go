package httpapi

import (
	"net/http"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/service"
)

// optionalList returns repeated query values decoded with key_encoding, or nil when absent.
func (q *query) optionalList(name string) []string {
	if !q.r.URL.Query().Has(name) {
		return nil
	}

	return q.keys(name)
}

func (s *Server) pubsubRoutes(mux *http.ServeMux) {
	//	@Summary		Subscribe to channels
	//	@Description	Server-sent events: "subscription" (service.PubSubSubscription), "message" (service.PubSubMessage), "error" (service.ErrorEvent) and the final "end" with the reason. Shard channels of different cluster slots use separate connections.
	//	@Tags			pubsub
	//	@Produce		text/event-stream
	//	@Param			channel			query		[]string	false	"Channels"			collectionFormat(multi)
	//	@Param			pattern			query		[]string	false	"Patterns"			collectionFormat(multi)
	//	@Param			shard_channel	query		[]string	false	"Shard channels"	collectionFormat(multi)
	//	@Param			encoding		query		string		false	"auto, utf8 or base64"
	//	@Param			duration		query		string		false	"Maximum duration"
	//	@Success		200				{string}	string		"Server-sent events"
	//	@Failure		default			{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/pubsub/subscribe [get]
	s.stream(mux, "GET /api/v1/pubsub/subscribe", func(r *http.Request) (<-chan service.Event, error) {
		q := newQuery(r)
		opts := service.SubscribeOptions{
			Channels:      q.optionalList("channel"),
			Patterns:      q.optionalList("pattern"),
			ShardChannels: q.optionalList("shard_channel"),
			Encoding:      q.encoding(),
			ReadOnly:      readOnlyRequest(r),
		}

		if q.err != nil {
			return nil, q.err
		}

		return s.svc.Subscribe(r.Context(), opts)
	})

	//	@Summary		Publish a message
	//	@Description	PUBLISH, or SPUBLISH with sharded; returns the number of receivers on the node that got the message.
	//	@Tags			pubsub
	//	@Produce		json
	//	@Param			body	body		PublishRequest	true	"Request body"
	//	@Success		200		{object}	Response{result=service.CommandResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/pubsub/publish [post]
	s.api(mux, "POST /api/v1/pubsub/publish", bodyRoute(s, func(_ *http.Request, b *PublishRequest) ([]any, func(any) any, error) {
		name := "publish"
		if b.Sharded {
			name = "spublish"
		}

		return cmd(name, b.Channel, b.Message), nil, required([]string{"channel"}, b.Channel)
	}))

	//	@Summary		Active channels
	//	@Description	PUBSUB CHANNELS (or SHARDCHANNELS) of all nodes merged.
	//	@Tags			pubsub
	//	@Produce		json
	//	@Param			pattern	query		string	false	"Glob pattern"
	//	@Param			sharded	query		bool	false	"Shard channels"
	//	@Success		200		{object}	Response{result=service.PubSubChannelsResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/pubsub/channels [get]
	s.api(mux, "GET /api/v1/pubsub/channels", func(r *http.Request) (any, error) {
		q := newQuery(r)
		pattern, sharded, enc := q.str("pattern", ""), q.bool("sharded"), q.encoding()

		if q.err != nil {
			return nil, q.err
		}

		return s.svc.PubSubChannels(r.Context(), pattern, sharded, enc)
	})
	//	@Summary		Subscribers per channel
	//	@Description	PUBSUB NUMSUB (or SHARDNUMSUB) summed over all nodes.
	//	@Tags			pubsub
	//	@Produce		json
	//	@Param			channel	query		[]string	true	"Channels"	collectionFormat(multi)
	//	@Param			sharded	query		bool		false	"Shard channels"
	//	@Success		200		{object}	Response{result=service.PubSubCountResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/pubsub/numsub [get]
	s.api(mux, "GET /api/v1/pubsub/numsub", func(r *http.Request) (any, error) {
		q := newQuery(r)
		channels, sharded, enc := q.keys("channel"), q.bool("sharded"), q.encoding()

		if q.err != nil {
			return nil, q.err
		}

		return s.svc.PubSubNumSub(r.Context(), channels, sharded, enc)
	})
	//	@Summary		Pattern subscriptions
	//	@Description	PUBSUB NUMPAT summed over all nodes.
	//	@Tags			pubsub
	//	@Produce		json
	//	@Success		200		{object}	Response{result=service.PubSubCountResult}
	//	@Failure		default	{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/pubsub/numpat [get]
	s.api(mux, "GET /api/v1/pubsub/numpat", func(r *http.Request) (any, error) {
		return s.svc.PubSubNumPat(r.Context())
	})

	//	@Summary		Monitor commands
	//	@Description	Server-sent events: "command" (service.MonitorEntry) for every command processed by the selected nodes (masters by default), "error" and "end".
	//	@Tags			pubsub
	//	@Produce		text/event-stream
	//	@Param			node		query		string	false	"Node address"
	//	@Param			target		query		string	false	"masters, replicas, all or sentinels"
	//	@Param			encoding	query		string	false	"auto, utf8 or base64"
	//	@Param			duration	query		string	false	"Maximum duration"
	//	@Success		200			{string}	string	"Server-sent events"
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/monitor [get]
	s.stream(mux, "GET /api/v1/monitor", func(r *http.Request) (<-chan service.Event, error) {
		q := newQuery(r)
		enc := q.encoding()

		if q.err != nil {
			return nil, q.err
		}

		events, err := withSelector(r, func(sel service.NodeSelector) (any, error) {
			return s.svc.Monitor(r.Context(), sel, enc, readOnlyRequest(r))
		})
		if err != nil {
			return nil, err
		}

		return events.(<-chan service.Event), nil //nolint:forcetypeassert // returned above
	})

	//	@Summary		Tail streams
	//	@Description	Server-sent events: "entry" (service.StreamEntry) for new entries. Without ids reading starts after the last entry; with group and consumer it reads with XREADGROUP.
	//	@Tags			streams
	//	@Produce		text/event-stream
	//	@Param			key			query		[]string	true	"Keys, repeat for several"	collectionFormat(multi)
	//	@Param			id			query		[]string	false	"Start ids per key"			collectionFormat(multi)
	//	@Param			group		query		string		false	"Group"
	//	@Param			consumer	query		string		false	"Consumer"
	//	@Param			noack		query		bool		false	"NOACK"
	//	@Param			count		query		int			false	"Entries per read"
	//	@Param			db			query		int			false	"Database"
	//	@Param			duration	query		string		false	"Maximum duration"
	//	@Success		200			{string}	string		"Server-sent events"
	//	@Failure		default		{object}	ErrorResponse
	//	@Security		BearerAuth
	//	@Router			/streams/tail [get]
	s.stream(mux, "GET /api/v1/streams/tail", func(r *http.Request) (<-chan service.Event, error) {
		q := newQuery(r)
		opts := service.TailOptions{
			Keys:     q.keys("key"),
			IDs:      r.URL.Query()["id"],
			Group:    q.str("group", ""),
			Consumer: q.str("consumer", ""),
			NoAck:    q.bool("noack"),
			Count:    q.int("count", 0),
			DB:       q.db(),
			Encoding: q.encoding(),
			ReadOnly: readOnlyRequest(r),
		}

		if q.err != nil {
			return nil, q.err
		}

		return s.svc.TailStreams(r.Context(), opts)
	})
}

type PublishRequest struct {
	// Channel. A string, a number or {"base64": "..."}.
	Channel codec.Arg `json:"channel"`
	// Message. A string, a number or {"base64": "..."}.
	Message codec.Arg `json:"message"`
	// Use SPUBLISH.
	Sharded bool `json:"sharded"`
}
