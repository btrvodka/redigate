package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/btrvodka/redigate/internal/codec"
)

// Export and import move keys as NDJSON, one key per line.
//
// The "dump" format keeps exact values of every type (including module types)
// with DUMP/RESTORE, but works only between servers with a compatible RDB version.
// The "value" format stores plain values of core types and works between any
// versions and forks.

const (
	FormatDump  = "dump"
	FormatValue = "value"

	transferBatch   = 500
	transferChunk   = 1000
	maxImportErrors = 100
	maxImportLine   = 512 << 20
)

type ExportOptions struct {
	Match    string
	Type     string
	Format   string
	DB       *int
	ReadOnly bool
}

type ExportRecord struct {
	Key   any    `json:"key"`
	Type  string `json:"type,omitempty"`
	TTLMS int64  `json:"ttl_ms"`
	Dump  string `json:"dump,omitempty"`
	Value any    `json:"value,omitempty"`
	// Error marks keys the value format can't represent; import skips them.
	Error string `json:"error,omitempty"`
}

// Export scans keys on every master and passes a record per key to emit.
func (s *Service) Export(ctx context.Context, opts ExportOptions, emit func(*ExportRecord) error) (int64, error) {
	switch opts.Format {
	case "":
		opts.Format = FormatDump
	case FormatDump, FormatValue:
	default:
		return 0, fmt.Errorf("%w: format must be dump or value", ErrInvalidRequest)
	}

	if _, err := s.checkCommand(codec.Args{"dump", "key"}, opts.ReadOnly); err != nil {
		return 0, err
	}

	nodes, err := s.scanNodes(ctx, opts.DB)
	if err != nil {
		return 0, err
	}

	var count int64

	for _, node := range nodes {
		var cursor uint64

		for {
			var keys []string

			keys, cursor, err = scanCmd(ctx, node.client, cursor, ScanOptions{
				Match: opts.Match, Type: opts.Type, Count: transferBatch,
			}).Result()
			if err != nil {
				return count, err //nolint:wrapcheck // mapped by the API
			}

			records, err := exportBatch(ctx, node.client, keys, opts.Format)
			if err != nil {
				return count, err
			}

			for _, record := range records {
				if err := emit(record); err != nil {
					return count, err
				}

				count++
			}

			if cursor == 0 {
				break
			}
		}
	}

	return count, nil
}

func exportBatch(ctx context.Context, client redis.UniversalClient, keys []string, format string) ([]*ExportRecord, error) {
	pipe := client.Pipeline()
	ttls := make([]*redis.DurationCmd, len(keys))
	dumps := make([]*redis.StringCmd, len(keys))
	types := make([]*redis.StatusCmd, len(keys))

	for i, key := range keys {
		ttls[i] = pipe.PTTL(ctx, key)

		if format == FormatDump {
			dumps[i] = pipe.Dump(ctx, key)
		} else {
			types[i] = pipe.Type(ctx, key)
		}
	}

	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err //nolint:wrapcheck // mapped by the API
	}

	enc := codec.NewEncoder(codec.EncodingAuto, math.MaxInt)
	records := make([]*ExportRecord, 0, len(keys))

	for i, key := range keys {
		record := &ExportRecord{Key: enc.String(key), TTLMS: ttlMS(ttls[i].Val())}

		if format == FormatDump {
			// The key expired or was deleted after SCAN.
			if errors.Is(dumps[i].Err(), redis.Nil) {
				continue
			}

			record.Dump = base64.StdEncoding.EncodeToString([]byte(dumps[i].Val()))
		} else {
			record.Type = types[i].Val()
			if record.Type == "none" {
				continue
			}

			if err := exportValue(ctx, client, record, key, enc); err != nil {
				return nil, err
			}
		}

		records = append(records, record)
	}

	return records, nil
}

func exportValue(ctx context.Context, client redis.UniversalClient, record *ExportRecord, key string, enc *codec.Encoder) error {
	reply, shape, err := readValue(ctx, client, record.Type, key, math.MaxInt32)
	if err != nil {
		return err
	}

	switch {
	case shape == nil || record.Type == "TSDB-TYPE":
		record.Error = fmt.Sprintf("type %s is not supported by the value format, use format=dump", record.Type)
	case record.Type == "ReJSON-RL":
		record.Value = decodeJSONValue(reply)
	default:
		record.Value = enc.Encode(shape(reply))
	}

	return nil
}

type ImportOptions struct {
	Replace  bool
	DB       *int
	ReadOnly bool
}

type ImportResult struct {
	Imported int64 `json:"imported"`
	// Skipped are existing keys (without replace) and keys exported with an error.
	Skipped int64         `json:"skipped"`
	Failed  int64         `json:"failed"`
	Errors  []ImportError `json:"errors,omitempty"`
}

type ImportError struct {
	Line  int64  `json:"line"`
	Key   any    `json:"key,omitempty"`
	Error string `json:"error"`
}

func (r *ImportResult) fail(line int64, key any, err string) {
	r.Failed++

	if len(r.Errors) < maxImportErrors {
		r.Errors = append(r.Errors, ImportError{Line: line, Key: key, Error: err})
	}
}

type importRecord struct {
	Key     *codec.Arg      `json:"key"`
	Type    string          `json:"type"`
	TTLMS   int64           `json:"ttl_ms"`
	Dump    string          `json:"dump"`
	Value   json.RawMessage `json:"value"`
	Error   string          `json:"error"`
	Summary json.RawMessage `json:"summary"`

	line int64
}

// Import restores keys from NDJSON produced by Export. Keys are written in
// pipelines of transferBatch records, so keys may belong to different cluster slots.
func (s *Service) Import(ctx context.Context, body io.Reader, opts ImportOptions) (*ImportResult, error) {
	for _, name := range []string{"restore", "del"} {
		if _, err := s.checkCommand(codec.Args{name, "key"}, opts.ReadOnly); err != nil {
			return nil, err
		}
	}

	client, err := s.redis.DB(ctx, s.db(opts.DB))
	if err != nil {
		return nil, err
	}

	result := &ImportResult{}
	reader := bufio.NewReaderSize(body, 1<<20) //nolint:mnd // 1 MiB buffer
	batch := make([]importRecord, 0, transferBatch)

	for line := int64(1); ; line++ {
		raw, readErr := readLine(reader)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return result, readErr
		}

		if record, ok := parseImportLine(raw, line, result); ok {
			batch = append(batch, record)
		}

		if len(batch) == transferBatch || (errors.Is(readErr, io.EOF) && len(batch) > 0) {
			if err := importBatch(ctx, client, batch, opts.Replace, result); err != nil {
				return result, err
			}

			batch = batch[:0]
		}

		if errors.Is(readErr, io.EOF) {
			return result, nil
		}
	}
}

func readLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte

	for {
		chunk, isPrefix, err := reader.ReadLine()
		line = append(line, chunk...)

		if err != nil {
			return line, err //nolint:wrapcheck // io.EOF and body errors are handled by the caller
		}

		if len(line) > maxImportLine {
			return nil, fmt.Errorf("%w: line exceeds %d bytes", ErrInvalidRequest, maxImportLine)
		}

		if !isPrefix {
			return line, nil
		}
	}
}

func parseImportLine(raw []byte, line int64, result *ImportResult) (importRecord, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return importRecord{}, false
	}

	var record importRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		result.fail(line, nil, "invalid JSON: "+err.Error())

		return importRecord{}, false
	}

	record.line = line

	switch {
	case record.Key == nil && (record.Summary != nil || record.Error != ""):
		// Trailing summary or error lines of an export.
		return importRecord{}, false
	case record.Key == nil:
		result.fail(line, nil, "key is required")

		return importRecord{}, false
	case record.Error != "":
		result.Skipped++

		return importRecord{}, false
	case record.Dump == "" && record.Value == nil:
		result.fail(line, string(*record.Key), "dump or value is required")

		return importRecord{}, false
	}

	return record, true
}

// importBatch writes records in one pipeline and accounts the results.
func importBatch(ctx context.Context, client redis.UniversalClient, batch []importRecord, replace bool, result *ImportResult) error {
	exists := make([]bool, len(batch))

	if !replace {
		var err error
		if exists, err = existingKeys(ctx, client, batch); err != nil {
			return err
		}
	}

	pipe := client.Pipeline()
	cmds := make([][]*redis.Cmd, len(batch))

	for i, record := range batch {
		if exists[i] {
			result.Skipped++

			continue
		}

		commands, err := importCommands(record, replace)
		if err != nil {
			result.fail(record.line, string(*record.Key), err.Error())

			continue
		}

		for _, args := range commands {
			cmds[i] = append(cmds[i], pipe.Do(ctx, args...))
		}
	}

	if pipe.Len() > 0 {
		// Errors are accounted per record.
		_, _ = pipe.Exec(ctx)
	}

	for i, record := range batch {
		if cmds[i] != nil {
			accountRecord(result, record, firstError(cmds[i]))
		}
	}

	return nil
}

// existingKeys checks keys of value records: RESTORE reports existing keys itself.
func existingKeys(ctx context.Context, client redis.UniversalClient, batch []importRecord) ([]bool, error) {
	pipe := client.Pipeline()
	cmds := make([]*redis.IntCmd, len(batch))

	for i, record := range batch {
		if record.Dump == "" {
			cmds[i] = pipe.Exists(ctx, string(*record.Key))
		}
	}

	if pipe.Len() > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err //nolint:wrapcheck // mapped by the API
		}
	}

	exists := make([]bool, len(batch))
	for i, cmd := range cmds {
		exists[i] = cmd != nil && cmd.Val() > 0
	}

	return exists, nil
}

func accountRecord(result *ImportResult, record importRecord, err error) {
	switch {
	case err == nil:
		result.Imported++
	case strings.HasPrefix(err.Error(), "BUSYKEY"):
		result.Skipped++
	default:
		result.fail(record.line, string(*record.Key), err.Error())
	}
}

func firstError(cmds []*redis.Cmd) error {
	for _, cmd := range cmds {
		if err := cmd.Err(); err != nil && !errors.Is(err, redis.Nil) {
			return err //nolint:wrapcheck // reported per record
		}
	}

	return nil
}

func importCommands(record importRecord, replace bool) ([][]any, error) {
	key := string(*record.Key)

	if record.Dump != "" {
		dump, err := base64.StdEncoding.DecodeString(record.Dump)
		if err != nil {
			return nil, fmt.Errorf("invalid dump: %w", err)
		}

		args := []any{"restore", key, max(record.TTLMS, 0), dump}
		if replace {
			args = append(args, "replace")
		}

		return [][]any{args}, nil
	}

	writes, err := valueCommands(key, record.Type, record.Value)
	if err != nil {
		return nil, err
	}

	var commands [][]any
	if replace {
		commands = append(commands, []any{"del", key})
	}

	commands = append(commands, writes...)

	if record.TTLMS > 0 {
		commands = append(commands, []any{"pexpire", key, record.TTLMS})
	}

	return commands, nil
}

func valueCommands(key, keyType string, raw json.RawMessage) ([][]any, error) {
	switch keyType {
	case "string":
		var value codec.Arg
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("invalid string value: %w", err)
		}

		return [][]any{{"set", key, string(value)}}, nil
	case "list", "set":
		var items []codec.Arg
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("invalid %s value: %w", keyType, err)
		}

		cmd := map[string]string{"list": "rpush", "set": "sadd"}[keyType]

		return chunked(cmd, key, codec.Strings(items), 1), nil
	case "hash":
		pairs, err := decodePairs(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid hash value: %w", err)
		}

		return chunked("hset", key, pairs, 2), nil //nolint:mnd // field and value
	case "zset":
		var members []struct {
			Member codec.Arg       `json:"member"`
			Score  json.RawMessage `json:"score"`
		}

		if err := json.Unmarshal(raw, &members); err != nil {
			return nil, fmt.Errorf("invalid zset value: %w", err)
		}

		args := make([]any, 0, len(members)*2) //nolint:mnd // score and member
		for _, m := range members {
			// Scores are numbers or "inf", "-inf".
			args = append(args, strings.Trim(string(m.Score), `"`), string(m.Member))
		}

		return chunked("zadd", key, args, 2), nil //nolint:mnd // score and member
	case "stream":
		return streamCommands(key, raw)
	case "ReJSON-RL":
		return [][]any{{"json.set", key, "$", string(raw)}}, nil
	default:
		return nil, fmt.Errorf("type %q is not supported by the value format", keyType)
	}
}

func streamCommands(key string, raw json.RawMessage) ([][]any, error) {
	var entries []struct {
		ID     string          `json:"id"`
		Fields json.RawMessage `json:"fields"`
	}

	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("invalid stream value: %w", err)
	}

	commands := make([][]any, 0, len(entries))

	for _, entry := range entries {
		fields, err := decodePairs(entry.Fields)
		if err != nil {
			return nil, fmt.Errorf("invalid fields of entry %s: %w", entry.ID, err)
		}

		commands = append(commands, append([]any{"xadd", key, entry.ID}, fields...))
	}

	if len(commands) == 0 {
		return nil, errors.New("empty stream")
	}

	return commands, nil
}

// decodePairs accepts an object {"field": value} or pairs [{"key", "value"}] produced
// by the encoder for binary field names, and returns flat field-value arguments.
func decodePairs(raw json.RawMessage) ([]any, error) {
	var object map[string]codec.Arg
	if err := json.Unmarshal(raw, &object); err == nil {
		out := make([]any, 0, len(object)*2) //nolint:mnd // field and value
		for field, value := range object {
			out = append(out, field, string(value))
		}

		return out, nil
	}

	var pairs []struct {
		Key   codec.Arg `json:"key"`
		Value codec.Arg `json:"value"`
	}

	if err := json.Unmarshal(raw, &pairs); err != nil {
		return nil, err //nolint:wrapcheck // wrapped by the caller
	}

	out := make([]any, 0, len(pairs)*2) //nolint:mnd // field and value
	for _, pair := range pairs {
		out = append(out, string(pair.Key), string(pair.Value))
	}

	return out, nil
}

// chunked splits long argument lists into several commands of transferChunk items.
func chunked(cmd, key string, args []any, itemSize int) [][]any {
	if len(args) == 0 {
		return nil
	}

	step := transferChunk * itemSize

	var commands [][]any

	for start := 0; start < len(args); start += step {
		end := min(start+step, len(args))
		commands = append(commands, append([]any{cmd, key}, args[start:end]...))
	}

	return commands
}
