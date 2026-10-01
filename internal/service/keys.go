package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/redisx"
)

const (
	defaultScanCount = 100
	// maxScanCalls bounds the work of one request when the pattern matches few keys.
	maxScanCalls = 100
	// deleteBatch is the number of keys unlinked per pipeline.
	deleteBatch = 500
)

type ScanOptions struct {
	Match    string
	Type     string
	Count    int
	Cursor   string
	DB       *int
	Encoding codec.Encoding
	// WithMeta adds type and TTL of every key.
	WithMeta bool
	ReadOnly bool
}

type ScanResult struct {
	// Cursor continues the scan, "0" means the scan is complete.
	Cursor string `json:"cursor"`
	// Keys are encoded keys or, with meta, objects {"key", "type", "ttl_ms"}.
	Keys      []any `json:"keys"`
	Truncated bool  `json:"truncated,omitempty"`
}

// scanPosition is a position in a scan over several nodes. Outside of
// cluster there is a single "node" with an empty address.
type scanPosition struct {
	node   string
	cursor uint64
}

func parseScanCursor(raw string) (scanPosition, error) {
	if raw == "" || raw == "0" {
		return scanPosition{}, nil
	}

	if cursor, err := strconv.ParseUint(raw, 10, 64); err == nil {
		return scanPosition{cursor: cursor}, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return scanPosition{}, fmt.Errorf("%w: invalid cursor", ErrInvalidRequest)
	}

	node, cursor, ok := strings.Cut(string(decoded), " ")
	pos := scanPosition{node: node}

	if pos.cursor, err = strconv.ParseUint(cursor, 10, 64); !ok || err != nil {
		return scanPosition{}, fmt.Errorf("%w: invalid cursor", ErrInvalidRequest)
	}

	return pos, nil
}

func (p scanPosition) String() string {
	if p.node == "" {
		return strconv.FormatUint(p.cursor, 10)
	}

	return base64.RawURLEncoding.EncodeToString([]byte(p.node + " " + strconv.FormatUint(p.cursor, 10)))
}

// scanNode is a server holding a part of the keyspace.
type scanNode struct {
	addr   string
	client redis.UniversalClient
}

// scanNodes returns masters in cluster and the db client otherwise.
func (s *Service) scanNodes(ctx context.Context, dbPtr *int) ([]scanNode, error) {
	db := s.db(dbPtr)

	if s.redis.Topology() != redisx.TopologyCluster {
		client, err := s.redis.DB(ctx, db)
		if err != nil {
			return nil, err
		}

		return []scanNode{{client: client}}, nil
	}

	if db != 0 {
		return nil, redisx.ErrClusterDB
	}

	masters, err := s.targetNodes(ctx, TargetMasters)
	if err != nil {
		return nil, err
	}

	nodes := make([]scanNode, 0, len(masters))
	for _, master := range masters {
		nodes = append(nodes, scanNode{addr: master.Addr, client: master.Client})
	}

	return nodes, nil
}

// ScanKeys returns a page of keys. In cluster it scans masters one after another
// and the cursor encodes the current master.
func (s *Service) ScanKeys(ctx context.Context, opts ScanOptions) (*ScanResult, error) {
	if opts.Count <= 0 {
		opts.Count = defaultScanCount
	}

	opts.Count = min(opts.Count, s.limits.MaxResponseItems)

	pos, err := parseScanCursor(opts.Cursor)
	if err != nil {
		return nil, err
	}

	nodes, err := s.scanNodes(ctx, opts.DB)
	if err != nil {
		return nil, err
	}

	index := 0

	if pos.node != "" {
		index = -1

		for i, node := range nodes {
			if node.addr == pos.node {
				index = i
			}
		}

		if index < 0 {
			return nil, fmt.Errorf("%w: node %s of the cursor is not a master anymore, restart the scan",
				ErrInvalidRequest, pos.node)
		}
	}

	enc := codec.NewEncoder(opts.Encoding, s.limits.MaxResponseItems)
	result := &ScanResult{Keys: []any{}}

	for calls := 0; calls < maxScanCalls && len(result.Keys) < opts.Count; calls++ {
		node := nodes[index]

		var keys []string

		keys, pos.cursor, err = scanCmd(ctx, node.client, pos.cursor, opts).Result()
		if err != nil {
			return nil, err //nolint:wrapcheck // mapped by the API
		}

		page, err := s.scanPage(ctx, node.client, keys, opts.WithMeta, enc)
		if err != nil {
			return nil, err
		}

		result.Keys = append(result.Keys, page...)

		if pos.cursor == 0 {
			if index++; index == len(nodes) {
				result.Cursor = "0"
				result.Truncated = enc.Truncated()

				return result, nil
			}
		}
	}

	pos.node = nodes[index].addr
	result.Cursor = pos.String()
	result.Truncated = enc.Truncated()

	return result, nil
}

func scanCmd(ctx context.Context, client redis.Cmdable, cursor uint64, opts ScanOptions) *redis.ScanCmd {
	if opts.Type != "" {
		return client.ScanType(ctx, cursor, opts.Match, int64(opts.Count), opts.Type)
	}

	return client.Scan(ctx, cursor, opts.Match, int64(opts.Count))
}

// KeyBrief is a key with its type and TTL in a SCAN page with meta.
type KeyBrief struct {
	Key   any    `json:"key"`
	Type  string `json:"type"`
	TTLMS int64  `json:"ttl_ms"`
}

func (s *Service) scanPage(ctx context.Context, client redis.Cmdable, keys []string, withMeta bool, enc *codec.Encoder) ([]any, error) {
	page := make([]any, 0, len(keys))

	if !withMeta {
		for _, key := range keys {
			page = append(page, enc.String(key))
		}

		return page, nil
	}

	pipe := client.Pipeline()
	types := make([]*redis.StatusCmd, len(keys))
	ttls := make([]*redis.DurationCmd, len(keys))

	for i, key := range keys {
		types[i] = pipe.Type(ctx, key)
		ttls[i] = pipe.PTTL(ctx, key)
	}

	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err //nolint:wrapcheck // mapped by the API
	}

	for i, key := range keys {
		page = append(page, KeyBrief{Key: enc.String(key), Type: types[i].Val(), TTLMS: ttlMS(ttls[i].Val())})
	}

	return page, nil
}

// MultiKey runs a single-key command for every key in one pipeline, so keys may
// belong to different cluster slots. It returns raw replies in the order of keys.
func (s *Service) MultiKey(ctx context.Context, cmd string, keys []string, extra []any, db *int, readOnly bool) ([]*redis.Cmd, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: no keys", ErrInvalidRequest)
	}

	if len(keys) > s.limits.MaxPipelineCommands {
		return nil, fmt.Errorf("%w: %d keys exceed the limit of %d", ErrInvalidRequest, len(keys), s.limits.MaxPipelineCommands)
	}

	if _, err := s.checkCommand(append(codec.Args{cmd, keys[0]}, extra...), readOnly); err != nil {
		return nil, err
	}

	client, err := s.redis.DB(ctx, s.db(db))
	if err != nil {
		return nil, err
	}

	pipe := client.Pipeline()
	cmds := make([]*redis.Cmd, len(keys))

	for i, key := range keys {
		cmds[i] = pipe.Do(ctx, append([]any{cmd, key}, extra...)...)
	}

	// Errors are reported per key.
	_, _ = pipe.Exec(ctx)

	return cmds, nil
}

type MultiKeyResult struct {
	// Count is the sum of integer replies: deleted, existing or touched keys.
	Count   int64           `json:"count"`
	Results []PipelineEntry `json:"results"`
}

// MultiKeyEncoded runs MultiKey and encodes per-key replies, applying opts.Shape to each.
func (s *Service) MultiKeyEncoded(ctx context.Context, cmd string, keys []string, extra []any, opts ExecOptions) (*MultiKeyResult, error) {
	cmds, err := s.MultiKey(ctx, cmd, keys, extra, opts.DB, opts.ReadOnly)
	if err != nil {
		return nil, err
	}

	enc := codec.NewEncoder(opts.Encoding, s.limits.MaxResponseItems)
	result := &MultiKeyResult{Results: make([]PipelineEntry, len(cmds))}

	for i, cmd := range cmds {
		reply, err := cmd.Result()
		if err = ignoreNil(err); err != nil {
			result.Results[i].Error = err.Error()

			continue
		}

		if n, ok := reply.(int64); ok {
			result.Count += n
		}

		result.Results[i].Value = enc.Encode(opts.shape(reply))
	}

	return result, nil
}

type DeleteByPatternResult struct {
	Matched int64 `json:"matched"`
	Deleted int64 `json:"deleted"`
	// Cursor is not "0" when the request ran out of time, repeat it with the cursor to continue.
	Cursor string `json:"cursor"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// DeleteByPattern unlinks every key matching the pattern on every master. When the
// request is about to time out it stops and returns a cursor to continue from.
func (s *Service) DeleteByPattern(ctx context.Context, opts ScanOptions, dryRun bool) (*DeleteByPatternResult, error) {
	if opts.Match == "" {
		return nil, fmt.Errorf("%w: match is required, use flushdb to delete everything", ErrInvalidRequest)
	}

	if !dryRun {
		if _, err := s.checkCommand(codec.Args{"unlink", "key"}, opts.ReadOnly); err != nil {
			return nil, err
		}
	}

	pos, err := parseScanCursor(opts.Cursor)
	if err != nil {
		return nil, err
	}

	nodes, err := s.scanNodes(ctx, opts.DB)
	if err != nil {
		return nil, err
	}

	result := &DeleteByPatternResult{Cursor: "0", DryRun: dryRun}
	started := pos.node == ""

	for _, node := range nodes {
		if !started && node.addr != pos.node {
			continue
		}

		started = true

		cursor, err := deleteOnNode(ctx, node, pos.cursor, opts, dryRun, result)
		if err != nil {
			return nil, err
		}

		if cursor != 0 {
			result.Cursor = scanPosition{node: node.addr, cursor: cursor}.String()

			return result, nil
		}

		pos.cursor = 0
	}

	return result, nil
}

// deleteOnNode scans and unlinks keys on one node. It returns a non-zero cursor
// when it stopped because the request deadline is close.
func deleteOnNode(
	ctx context.Context,
	node scanNode,
	cursor uint64,
	opts ScanOptions,
	dryRun bool,
	result *DeleteByPatternResult,
) (uint64, error) {
	deadline, hasDeadline := ctx.Deadline()

	for {
		var (
			keys []string
			err  error
		)

		keys, cursor, err = scanCmd(ctx, node.client, cursor, ScanOptions{
			Match: opts.Match, Type: opts.Type, Count: deleteBatch,
		}).Result()
		if err != nil {
			return 0, err //nolint:wrapcheck // mapped by the API
		}

		result.Matched += int64(len(keys))

		if !dryRun && len(keys) > 0 {
			deleted, err := unlinkKeys(ctx, node.client, keys)
			if err != nil {
				return 0, err
			}

			result.Deleted += deleted
		}

		if cursor == 0 || (hasDeadline && time.Until(deadline) < time.Second) {
			return cursor, nil
		}
	}
}

// unlinkKeys unlinks keys one by one in a pipeline: keys on a node may belong to different slots.
func unlinkKeys(ctx context.Context, client redis.Cmdable, keys []string) (int64, error) {
	pipe := client.Pipeline()
	cmds := make([]*redis.IntCmd, len(keys))

	for i, key := range keys {
		cmds[i] = pipe.Unlink(ctx, key)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err //nolint:wrapcheck // mapped by the API
	}

	var deleted int64
	for _, cmd := range cmds {
		deleted += cmd.Val()
	}

	return deleted, nil
}

type KeyMeta struct {
	Key         any    `json:"key"`
	Type        string `json:"type"`
	TTLMS       int64  `json:"ttl_ms"`
	Length      *int64 `json:"length,omitempty"`
	Encoding    string `json:"encoding,omitempty"`
	MemoryBytes *int64 `json:"memory_bytes,omitempty"`
	IdleSeconds *int64 `json:"idle_seconds,omitempty"`
	Frequency   *int64 `json:"frequency,omitempty"`
}

//nolint:gochecknoglobals // static table
var lengthCommands = map[string]string{
	"string": "strlen",
	"list":   "llen",
	"set":    "scard",
	"zset":   "zcard",
	"hash":   "hlen",
	"stream": "xlen",
}

// KeyMeta returns the type, TTL and object metadata of the key.
func (s *Service) KeyMeta(ctx context.Context, key string, db *int, encoding codec.Encoding) (*KeyMeta, error) {
	client, err := s.redis.DB(ctx, s.db(db))
	if err != nil {
		return nil, err
	}

	keyType, err := client.Type(ctx, key).Result()
	if err != nil {
		return nil, err //nolint:wrapcheck // mapped by the API
	}

	if keyType == "none" {
		return nil, fmt.Errorf("key not found: %w", redis.Nil)
	}

	pipe := client.Pipeline()
	ttl := pipe.PTTL(ctx, key)
	objEncoding := pipe.ObjectEncoding(ctx, key)
	memory := pipe.MemoryUsage(ctx, key)
	// IDLETIME fails with an LFU maxmemory policy, FREQ fails without it.
	idle := pipe.Do(ctx, "object", "idletime", key)
	freq := pipe.Do(ctx, "object", "freq", key)

	var length *redis.IntCmd
	if cmd, ok := lengthCommands[keyType]; ok {
		length = redis.NewIntCmd(ctx, cmd, key)
		_ = pipe.Process(ctx, length)
	}

	// Errors of optional fields are ignored.
	_, _ = pipe.Exec(ctx)

	meta := &KeyMeta{
		Key:      codec.NewEncoder(encoding, 1).String(key),
		Type:     keyType,
		TTLMS:    ttlMS(ttl.Val()),
		Encoding: objEncoding.Val(),
	}

	meta.MemoryBytes = optionalInt(memory.Val(), memory.Err())
	meta.IdleSeconds = optionalInt(idle.Int64())
	meta.Frequency = optionalInt(freq.Int64())

	if length != nil {
		meta.Length = optionalInt(length.Val(), length.Err())
	}

	return meta, nil
}

type KeyValue struct {
	KeyMeta

	Value     any  `json:"value"`
	Truncated bool `json:"truncated,omitempty"`
	// Unsupported is set for types without a known read command, use /command for them.
	Unsupported bool `json:"unsupported,omitempty"`
}

// KeyValue reads the whole value of the key according to its type, collections
// are limited to MAX_RESPONSE_ITEMS elements.
func (s *Service) KeyValue(ctx context.Context, key string, db *int, encoding codec.Encoding) (*KeyValue, error) {
	meta, err := s.KeyMeta(ctx, key, db, encoding)
	if err != nil {
		return nil, err
	}

	client, err := s.redis.DB(ctx, s.db(db))
	if err != nil {
		return nil, err
	}

	limit := s.limits.MaxResponseItems
	result := &KeyValue{KeyMeta: *meta}

	reply, shape, err := readValue(ctx, client, meta.Type, key, limit)
	if err != nil {
		return nil, err
	}

	if reply == nil && shape == nil {
		result.Unsupported = true

		return result, nil
	}

	enc := codec.NewEncoder(encoding, limit)
	result.Value = enc.Encode(shape(reply))
	result.Truncated = enc.Truncated() || (meta.Length != nil && *meta.Length > int64(limit) && meta.Type != "string")

	if meta.Type == "ReJSON-RL" {
		result.Value = decodeJSONValue(reply)
	}

	return result, nil
}

func readValue(ctx context.Context, client redis.UniversalClient, keyType, key string, limit int) (any, func(any) any, error) {
	identity := func(v any) any { return v }

	var cmd *redis.Cmd

	switch keyType {
	case "string":
		cmd = client.Do(ctx, "get", key)
	case "list":
		cmd = client.Do(ctx, "lrange", key, 0, limit-1)
	case "zset":
		cmd = client.Do(ctx, "zrange", key, 0, limit-1, "withscores")
		identity = ShapeScored
	case "stream":
		cmd = client.Do(ctx, "xrange", key, "-", "+", "count", limit)
		identity = ShapeStreamEntries
	case "set":
		members, err := scanCollection(ctx, client, "sscan", key, limit)

		return members, identity, err
	case "hash":
		fields, err := scanCollection(ctx, client, "hscan", key, limit*2) //nolint:mnd // field and value

		return fields, ShapeMap, err
	case "ReJSON-RL":
		cmd = client.Do(ctx, "json.get", key)
	case "TSDB-TYPE":
		cmd = client.Do(ctx, "ts.range", key, "-", "+", "count", limit)
	default:
		return nil, nil, nil
	}

	reply, err := cmd.Result()
	if err = ignoreNil(err); err != nil {
		return nil, nil, err
	}

	return reply, identity, nil
}

// scanCollection reads up to limit items of a set or a hash with SSCAN/HSCAN.
func scanCollection(ctx context.Context, client redis.UniversalClient, cmd, key string, limit int) ([]any, error) {
	var (
		items  []any
		cursor any = "0"
	)

	for {
		page, err := client.Do(ctx, cmd, key, cursor, "count", defaultScanCount).Slice()
		if err != nil {
			return nil, err //nolint:wrapcheck // mapped by the API
		}

		if len(page) != 2 { //nolint:mnd // cursor and items
			return nil, fmt.Errorf("unexpected %s reply", strings.ToUpper(cmd))
		}

		batch, _ := page[1].([]any)
		items = append(items, batch...)
		cursor = page[0]

		if fmt.Sprint(cursor) == "0" || len(items) >= limit {
			return items[:min(len(items), limit)], nil
		}
	}
}

func decodeJSONValue(reply any) any {
	raw, ok := reply.(string)
	if !ok {
		return reply
	}

	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}

	return value
}

// ttlMS converts PTTL to milliseconds. go-redis returns -1 (no TTL) and
// -2 (no key) as raw durations, they are kept as is.
func ttlMS(d time.Duration) int64 {
	if d < 0 {
		return int64(d)
	}

	return d.Milliseconds()
}

func optionalInt(v int64, err error) *int64 {
	if err != nil {
		return nil
	}

	return &v
}
