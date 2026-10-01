package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/redisx"
)

const (
	eventBuffer  = 256
	retryBackoff = time.Second
	tailBlock    = 5 * time.Second
	maxTailKeys  = 16
)

// Event is an item of a stream (server-sent events). Streams end when the
// context is canceled: the channel is closed after all resources are released.
type Event struct {
	Type string
	Data any
}

type ErrorEvent struct {
	Node  string `json:"node,omitempty"`
	Error string `json:"error"`
}

// streamGroup runs producers and closes the output when all of them have stopped
// and the cleanup has run.
type streamGroup struct {
	ctx context.Context //nolint:containedctx // lifetime of the stream
	out chan Event
	wg  sync.WaitGroup
}

func newStreamGroup(ctx context.Context) *streamGroup {
	return &streamGroup{ctx: ctx, out: make(chan Event, eventBuffer)}
}

func (g *streamGroup) send(event Event) bool {
	select {
	case g.out <- event:
		return true
	case <-g.ctx.Done():
		return false
	}
}

// sleep waits before a retry and reports whether the stream is still alive.
func (g *streamGroup) sleep() bool {
	select {
	case <-time.After(retryBackoff):
		return true
	case <-g.ctx.Done():
		return false
	}
}

func (g *streamGroup) goProduce(fn func()) {
	g.wg.Go(fn)
}

// start closes the output when the context is done and producers have stopped.
// cleanup unblocks producers waiting in network reads.
func (g *streamGroup) start(cleanup func()) <-chan Event {
	go func() {
		<-g.ctx.Done()
		cleanup()
		g.wg.Wait()
		close(g.out)
	}()

	return g.out
}

type SubscribeOptions struct {
	Channels      []string
	Patterns      []string
	ShardChannels []string
	Encoding      codec.Encoding
	ReadOnly      bool
}

type PubSubMessage struct {
	Channel any  `json:"channel"`
	Pattern any  `json:"pattern,omitempty"`
	Payload any  `json:"payload"`
	Sharded bool `json:"sharded,omitempty"`
}

type PubSubSubscription struct {
	Kind    string `json:"kind"`
	Channel any    `json:"channel"`
	Count   int    `json:"count"`
}

// Subscribe streams messages of channels, patterns and shard channels. Shard
// channels of different cluster slots are subscribed through separate connections.
func (s *Service) Subscribe(ctx context.Context, opts SubscribeOptions) (<-chan Event, error) {
	if len(opts.Channels)+len(opts.Patterns)+len(opts.ShardChannels) == 0 {
		return nil, fmt.Errorf("%w: channel, pattern or shard_channel is required", ErrInvalidRequest)
	}

	for name, used := range map[string]bool{
		"subscribe":  len(opts.Channels) > 0,
		"psubscribe": len(opts.Patterns) > 0,
		"ssubscribe": len(opts.ShardChannels) > 0,
	} {
		if !used {
			continue
		}

		if err := s.checkStreaming(codec.Args{name, "x"}, opts.ReadOnly); err != nil {
			return nil, err
		}
	}

	client := s.redis.Default()

	var subs []*redis.PubSub

	closeAll := func() {
		for _, sub := range subs {
			_ = sub.Close()
		}
	}

	if len(opts.Channels)+len(opts.Patterns) > 0 {
		sub := client.Subscribe(ctx)
		subs = append(subs, sub)

		if err := subscribeAll(ctx, sub, opts.Channels, opts.Patterns); err != nil {
			closeAll()

			return nil, err
		}
	}

	for _, group := range s.groupBySlot(opts.ShardChannels) {
		sub := client.SSubscribe(ctx)
		subs = append(subs, sub)

		if err := sub.SSubscribe(ctx, group...); err != nil {
			closeAll()

			return nil, err //nolint:wrapcheck // mapped by the API
		}
	}

	group := newStreamGroup(ctx)

	for i, sub := range subs {
		sharded := len(opts.Channels)+len(opts.Patterns) == 0 || i > 0
		group.goProduce(func() { receivePubSub(group, sub, sharded, opts.Encoding) })
	}

	return group.start(closeAll), nil
}

func subscribeAll(ctx context.Context, sub *redis.PubSub, channels, patterns []string) error {
	if len(channels) > 0 {
		if err := sub.Subscribe(ctx, channels...); err != nil {
			return err //nolint:wrapcheck // mapped by the API
		}
	}

	if len(patterns) > 0 {
		if err := sub.PSubscribe(ctx, patterns...); err != nil {
			return err //nolint:wrapcheck // mapped by the API
		}
	}

	return nil
}

// groupBySlot splits names by cluster slot; outside of cluster there is one group.
func (s *Service) groupBySlot(names []string) [][]string {
	if len(names) == 0 {
		return nil
	}

	if s.redis.Topology() != redisx.TopologyCluster {
		return [][]string{names}
	}

	bySlot := make(map[int][]string)

	var order []int

	for _, name := range names {
		slot := redisx.Slot(name)
		if _, ok := bySlot[slot]; !ok {
			order = append(order, slot)
		}

		bySlot[slot] = append(bySlot[slot], name)
	}

	groups := make([][]string, 0, len(order))
	for _, slot := range order {
		groups = append(groups, bySlot[slot])
	}

	return groups
}

// receivePubSub forwards messages until the subscription is closed. go-redis
// reconnects and resubscribes on the next Receive after a network error.
func receivePubSub(group *streamGroup, sub *redis.PubSub, sharded bool, encoding codec.Encoding) {
	enc := codec.NewEncoder(encoding, 1)

	for {
		msg, err := sub.Receive(group.ctx)
		if err != nil {
			if group.ctx.Err() != nil || errors.Is(err, redis.ErrClosed) {
				return
			}

			if !group.send(Event{Type: "error", Data: ErrorEvent{Error: err.Error()}}) || !group.sleep() {
				return
			}

			continue
		}

		var event Event

		switch m := msg.(type) {
		case *redis.Message:
			data := PubSubMessage{Channel: enc.String(m.Channel), Payload: enc.String(m.Payload), Sharded: sharded}
			if m.Pattern != "" {
				data.Pattern = enc.String(m.Pattern)
			}

			event = Event{Type: "message", Data: data}
		case *redis.Subscription:
			event = Event{Type: "subscription", Data: PubSubSubscription{Kind: m.Kind, Channel: enc.String(m.Channel), Count: m.Count}}
		default:
			continue
		}

		if !group.send(event) {
			return
		}
	}
}

type MonitorEntry struct {
	Node   string  `json:"node"`
	Time   float64 `json:"time"`
	DB     int     `json:"db"`
	Client string  `json:"client"`
	Args   []any   `json:"args"`
	// Raw is set when the line could not be parsed.
	Raw string `json:"raw,omitempty"`
}

// Monitor streams commands processed by the selected nodes (masters by default).
func (s *Service) Monitor(ctx context.Context, sel NodeSelector, encoding codec.Encoding, readOnly bool) (<-chan Event, error) {
	if err := s.checkStreaming(codec.Args{"monitor"}, readOnly); err != nil {
		return nil, err
	}

	nodes, err := s.selectNodes(ctx, sel, TargetMasters)
	if err != nil {
		return nil, err
	}

	conns := make([]*redisx.MonitorConn, 0, len(nodes))

	closeAll := func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}

	for _, node := range nodes {
		conn, err := s.redis.Monitor(ctx, node)
		if err != nil {
			closeAll()

			return nil, err
		}

		conns = append(conns, conn)
	}

	group := newStreamGroup(ctx)

	for i, conn := range conns {
		addr := nodes[i].Addr

		group.goProduce(func() {
			enc := codec.NewEncoder(encoding, s.limits.MaxResponseItems)

			for {
				line, err := conn.Next()
				if err != nil {
					if group.ctx.Err() == nil {
						group.send(Event{Type: "error", Data: ErrorEvent{Node: addr, Error: err.Error()}})
					}

					return
				}

				if !group.send(Event{Type: "command", Data: parseMonitorLine(addr, line, enc)}) {
					return
				}
			}
		})
	}

	return group.start(closeAll), nil
}

// parseMonitorLine parses `1700000000.123456 [0 127.0.0.1:5000] "set" "k" "v"`.
func parseMonitorLine(node, line string, enc *codec.Encoder) MonitorEntry {
	entry := MonitorEntry{Node: node, Raw: line}

	ts, rest, ok1 := strings.Cut(line, " [")
	inside, command, ok2 := strings.Cut(rest, "] ")
	db, client, ok3 := strings.Cut(inside, " ")

	if !ok1 || !ok2 || !ok3 {
		return entry
	}

	args, err := codec.SplitArgs(command)
	if err != nil {
		return entry
	}

	entry.Time, _ = strconv.ParseFloat(ts, 64)
	entry.DB, _ = strconv.Atoi(db)
	entry.Client = client
	entry.Raw = ""

	for i := range args {
		entry.Args = append(entry.Args, enc.String(args.String(i)))
	}

	return entry
}

type TailOptions struct {
	Keys     []string
	IDs      []string
	Group    string
	Consumer string
	NoAck    bool
	Count    int64
	DB       *int
	Encoding codec.Encoding
	ReadOnly bool
}

type StreamEntry struct {
	Stream any    `json:"stream"`
	ID     string `json:"id"`
	Fields any    `json:"fields"`
}

// TailStreams streams new entries of streams. Without ids it starts after the
// last entry; with a group it reads with XREADGROUP (ids default to ">").
// Every key is read by its own loop, so keys may belong to different cluster slots.
func (s *Service) TailStreams(ctx context.Context, opts TailOptions) (<-chan Event, error) {
	if len(opts.Keys) == 0 || len(opts.Keys) > maxTailKeys {
		return nil, fmt.Errorf("%w: from 1 to %d keys are required", ErrInvalidRequest, maxTailKeys)
	}

	if len(opts.IDs) > 0 && len(opts.IDs) != len(opts.Keys) {
		return nil, fmt.Errorf("%w: ids must match keys", ErrInvalidRequest)
	}

	if (opts.Group == "") != (opts.Consumer == "") {
		return nil, fmt.Errorf("%w: group and consumer must be set together", ErrInvalidRequest)
	}

	name := "xread"
	if opts.Group != "" {
		name = "xreadgroup"
	}

	if _, err := s.checkCommand(codec.Args{name, "group"}, opts.ReadOnly); err != nil {
		return nil, err
	}

	db := s.db(opts.DB)

	client, err := s.redis.DB(ctx, db)
	if err != nil {
		return nil, err
	}

	ids, err := tailStartIDs(ctx, client, opts)
	if err != nil {
		return nil, err
	}

	group := newStreamGroup(ctx)

	for i, key := range opts.Keys {
		group.goProduce(func() { s.tailStream(group, key, ids[i], db, opts) })
	}

	return group.start(func() {}), nil
}

// tailStartIDs resolves where reading starts: given ids, ">" for groups or after the last entry.
func tailStartIDs(ctx context.Context, client redis.UniversalClient, opts TailOptions) ([]string, error) {
	ids := make([]string, len(opts.Keys))

	for i, key := range opts.Keys {
		switch {
		case len(opts.IDs) > 0 && opts.IDs[i] != "$":
			ids[i] = opts.IDs[i]
		case opts.Group != "":
			ids[i] = ">"
		default:
			// "$" in a loop would skip entries added between calls: resolve it once.
			id, err := lastStreamID(ctx, client, key)
			if err != nil {
				return nil, err
			}

			ids[i] = id
		}
	}

	return ids, nil
}

func lastStreamID(ctx context.Context, client redis.UniversalClient, key string) (string, error) {
	entries, err := client.XRevRangeN(ctx, key, "+", "-", 1).Result()
	if err != nil {
		return "", err //nolint:wrapcheck // mapped by the API
	}

	if len(entries) == 0 {
		return "0-0", nil
	}

	return entries[0].ID, nil
}

// tailStream reads one stream on a dedicated connection: closing it interrupts a
// blocking read at once, go-redis only honors context deadlines. After network
// errors and redirects (failover) the connection is reopened to the current master.
func (s *Service) tailStream(group *streamGroup, key, id string, db int, opts TailOptions) {
	enc := codec.NewEncoder(opts.Encoding, s.limits.MaxResponseItems)

	for {
		node, err := s.redis.MasterFor(group.ctx, key, true)

		var sess *redisx.Session
		if err == nil {
			sess, err = s.redis.OpenSession(group.ctx, node, db)
		}

		if err == nil {
			stop := context.AfterFunc(group.ctx, func() { _ = sess.Close() })
			id, err = s.readStream(group, sess.Conn(), key, id, opts, enc)

			stop()
			_ = sess.Close()
		}

		if group.ctx.Err() != nil {
			return
		}

		group.send(Event{Type: "error", Data: ErrorEvent{Node: node.Addr, Error: fmt.Sprintf("%s: %v", key, err)}})

		// Error replies (WRONGTYPE, NOGROUP) won't fix themselves, redirects mean a failover.
		switch prefix := redisx.ReplyErrorPrefix(err); prefix {
		case "":
		case "MOVED", "READONLY":
			s.redis.ReloadState(group.ctx)
		default:
			return
		}

		if !group.sleep() {
			return
		}
	}
}

// readStream reads entries until an error and returns the last delivered id.
func (s *Service) readStream(
	group *streamGroup,
	conn *redis.Conn,
	key, id string,
	opts TailOptions,
	enc *codec.Encoder,
) (string, error) {
	for {
		var (
			streams []redis.XStream
			err     error
		)

		if opts.Group != "" {
			streams, err = conn.XReadGroup(group.ctx, &redis.XReadGroupArgs{
				Group: opts.Group, Consumer: opts.Consumer, Streams: []string{key, id},
				Count: opts.Count, Block: tailBlock, NoAck: opts.NoAck,
			}).Result()
		} else {
			streams, err = conn.XRead(group.ctx, &redis.XReadArgs{
				Streams: []string{key, id}, Count: opts.Count, Block: tailBlock,
			}).Result()
		}

		if errors.Is(err, redis.Nil) {
			continue
		}

		if err != nil {
			return id, err //nolint:wrapcheck // reported as an event
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				fields := make(map[any]any, len(msg.Values))
				for field, value := range msg.Values {
					fields[field] = value
				}

				entry := StreamEntry{Stream: enc.String(stream.Stream), ID: msg.ID, Fields: enc.Encode(fields)}
				if !group.send(Event{Type: "entry", Data: entry}) {
					return id, group.ctx.Err()
				}

				// The group cursor ">" moves by itself.
				if opts.Group == "" {
					id = msg.ID
				}
			}
		}
	}
}

// checkStreaming applies the read-only policy to commands served by streaming endpoints.
func (s *Service) checkStreaming(args codec.Args, readOnly bool) error {
	spec, _ := s.redis.Commands().Lookup(args.Name(), args.String(1))
	if readOnly && !readOnlyAllowed(spec, args) {
		return fmt.Errorf("%w: %s is not allowed with read-only access", ErrForbidden, commandTitle(spec, args))
	}

	return nil
}
