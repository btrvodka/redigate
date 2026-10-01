package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/btrvodka/redigate/internal/redisx"
)

func (s *Service) Info(ctx context.Context, sel NodeSelector, sections []string) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, parseInfo, commandArgs([]any{"info"}, sections)...)
}

type DBSizeResult struct {
	OK bool `json:"ok"`
	// Total is the sum over masters, replicas hold copies of the same keys.
	Total int64        `json:"total"`
	Nodes []NodeResult `json:"nodes"`
}

func (s *Service) DBSize(ctx context.Context, sel NodeSelector) (*DBSizeResult, error) {
	res, err := s.perNodeCommand(ctx, sel, TargetMasters, func(reply any) (any, error) {
		return toInt64(reply), nil
	}, "dbsize")
	if err != nil {
		return nil, err
	}

	result := &DBSizeResult{OK: res.OK, Nodes: res.Nodes}

	for _, node := range res.Nodes {
		if size, ok := node.Value.(int64); ok && node.Role == string(redisx.RoleMaster) {
			result.Total += size
		}
	}

	return result, nil
}

func (s *Service) ConfigGet(ctx context.Context, sel NodeSelector, patterns []string) (*FanOutResult, error) {
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}

	return s.perNodeCommand(ctx, sel, TargetAll, parseStringMap, commandArgs([]any{"config", "get"}, patterns)...)
}

// ConfigSet applies parameters with a single CONFIG SET, which is atomic since redis 7.
func (s *Service) ConfigSet(ctx context.Context, sel NodeSelector, params map[string]string) (*FanOutResult, error) {
	if len(params) == 0 {
		return nil, fmt.Errorf("%w: no parameters", ErrInvalidRequest)
	}

	args := []any{"config", "set"}
	for _, name := range slices.Sorted(maps.Keys(params)) {
		args = append(args, name, params[name])
	}

	return s.perNodeCommand(ctx, sel, TargetAll, nil, args...)
}

func (s *Service) ConfigRewrite(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "config", "rewrite")
}

func (s *Service) ConfigResetStat(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "config", "resetstat")
}

func (s *Service) Slowlog(ctx context.Context, sel NodeSelector, count int) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, parseSlowlog, "slowlog", "get", count)
}

func (s *Service) SlowlogReset(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "slowlog", "reset")
}

func (s *Service) ClientList(ctx context.Context, sel NodeSelector, clientType string) (*FanOutResult, error) {
	args := []any{"client", "list"}
	if clientType != "" {
		args = append(args, "type", clientType)
	}

	return s.perNodeCommand(ctx, sel, TargetAll, parseClientList, args...)
}

// ClientKill kills clients matching all filters, e.g. {"id": "42"} or {"user": "app", "skipme": "yes"}.
func (s *Service) ClientKill(ctx context.Context, sel NodeSelector, filters map[string]string) (*FanOutResult, error) {
	if len(filters) == 0 {
		return nil, fmt.Errorf("%w: at least one filter is required", ErrInvalidRequest)
	}

	args := []any{"client", "kill"}
	for _, name := range slices.Sorted(maps.Keys(filters)) {
		args = append(args, strings.ToUpper(name), filters[name])
	}

	return s.perNodeCommand(ctx, sel, TargetAll, nil, args...)
}

// ClientPause pauses clients for timeoutMS milliseconds, mode is "write" or "all".
func (s *Service) ClientPause(ctx context.Context, sel NodeSelector, timeoutMS int64, mode string) (*FanOutResult, error) {
	if timeoutMS <= 0 {
		return nil, fmt.Errorf("%w: timeout_ms must be positive", ErrInvalidRequest)
	}

	args := []any{"client", "pause", timeoutMS}
	if mode != "" {
		args = append(args, strings.ToUpper(mode))
	}

	return s.perNodeCommand(ctx, sel, TargetMasters, nil, args...)
}

func (s *Service) ClientUnpause(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetMasters, nil, "client", "unpause")
}

func (s *Service) MemoryStats(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "memory", "stats")
}

func (s *Service) MemoryDoctor(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "memory", "doctor")
}

func (s *Service) LatencyLatest(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "latency", "latest")
}

func (s *Service) LatencyReset(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "latency", "reset")
}

func (s *Service) Role(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "role")
}

// FlushAll removes all keys from all databases of the selected masters.
func (s *Service) FlushAll(ctx context.Context, sel NodeSelector, async bool) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetMasters, nil, flushArgs("flushall", async)...)
}

// FlushDB removes all keys of the selected database on the selected masters.
func (s *Service) FlushDB(ctx context.Context, sel NodeSelector, async bool) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetMasters, nil, flushArgs("flushdb", async)...)
}

// flushArgs omits SYNC by default: servers before 6.2 don't accept it.
func flushArgs(cmd string, async bool) []any {
	if async {
		return []any{cmd, "async"}
	}

	return []any{cmd}
}

// Persist runs SAVE, BGSAVE or BGREWRITEAOF.
func (s *Service) Persist(ctx context.Context, sel NodeSelector, cmd string) (*FanOutResult, error) {
	switch cmd {
	case "save", "bgsave", "bgrewriteaof":
	default:
		return nil, fmt.Errorf("%w: unknown persistence command %q", ErrInvalidRequest, cmd)
	}

	return s.perNodeCommand(ctx, sel, TargetMasters, nil, cmd)
}

type ShutdownOptions struct {
	// Save is "", "save" or "nosave".
	Save  string
	Now   bool
	Force bool
	Abort bool
}

// Shutdown stops servers. It never has a default target: a node or a target must be given.
func (s *Service) Shutdown(ctx context.Context, sel NodeSelector, opts ShutdownOptions) (*FanOutResult, error) {
	args := []any{"shutdown"}

	switch strings.ToLower(opts.Save) {
	case "":
	case "save", "nosave":
		args = append(args, strings.ToLower(opts.Save))
	default:
		return nil, fmt.Errorf("%w: save must be save or nosave", ErrInvalidRequest)
	}

	if opts.Now {
		args = append(args, "now")
	}

	if opts.Force {
		args = append(args, "force")
	}

	if opts.Abort {
		args = append(args, "abort")
	}

	return s.perNode(ctx, sel, "", func(ctx context.Context, node redisx.Node) (any, error) {
		err := node.Client.Do(ctx, args...).Err()

		// A successful shutdown closes the connection without a reply.
		if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || redisx.IsNetworkError(err) {
			return "shutting down", nil
		}

		return nil, err
	})
}

// ReplicaOf makes the node a replica of host:port, or a master if host is empty.
func (s *Service) ReplicaOf(ctx context.Context, sel NodeSelector, host string, port int) (*FanOutResult, error) {
	if sel.Node == "" {
		return nil, fmt.Errorf("%w: node is required", ErrInvalidRequest)
	}

	args := []any{"replicaof", "no", "one"}
	if host != "" {
		args = []any{"replicaof", host, port}
	}

	return s.perNodeCommand(ctx, sel, "", nil, args...)
}

type CommandInfo struct {
	Name          string        `json:"name"`
	Arity         int           `json:"arity"`
	Flags         []string      `json:"flags"`
	FirstKey      int           `json:"first_key"`
	LastKey       int           `json:"last_key"`
	Step          int           `json:"step"`
	ACLCategories []string      `json:"acl_categories,omitempty"`
	Subcommands   []CommandInfo `json:"subcommands,omitempty"`
}

// Commands returns metadata of commands supported by the server, optionally filtered by name prefix.
func (s *Service) Commands(prefix string) []CommandInfo {
	prefix = strings.ToLower(prefix)

	var result []CommandInfo

	for _, spec := range s.redis.Commands().All() {
		if strings.HasPrefix(spec.Name, prefix) {
			result = append(result, commandInfo(spec))
		}
	}

	slices.SortFunc(result, func(a, b CommandInfo) int { return cmp.Compare(a.Name, b.Name) })

	return result
}

func commandInfo(spec *redisx.CommandSpec) CommandInfo {
	info := CommandInfo{
		Name:          spec.FullName,
		Arity:         spec.Arity,
		Flags:         spec.Flags,
		FirstKey:      spec.FirstKey,
		LastKey:       spec.LastKey,
		Step:          spec.Step,
		ACLCategories: spec.ACLCategories,
	}

	for _, sub := range spec.Subcommands {
		info.Subcommands = append(info.Subcommands, commandInfo(sub))
	}

	slices.SortFunc(info.Subcommands, func(a, b CommandInfo) int { return cmp.Compare(a.Name, b.Name) })

	return info
}

// CommandDocs returns COMMAND DOCS for the given commands or for all commands.
func (s *Service) CommandDocs(ctx context.Context, names []string) (*CommandResult, error) {
	return s.onNode(ctx, "", s.anyMaster, nil, commandArgs([]any{"command", "docs"}, names)...)
}

type ModulesResult struct {
	Modules any `json:"modules"`
	// Capabilities are detected by command presence, so they work for modules
	// built into the server (redis 8) as well as for loaded ones.
	Capabilities map[string]bool `json:"capabilities"`
}

//nolint:gochecknoglobals // static table
var capabilityCommands = map[string]string{
	"json":       "json.get",
	"search":     "ft.search",
	"timeseries": "ts.add",
	"bloom":      "bf.add",
	"cuckoo":     "cf.add",
	"cms":        "cms.incrby",
	"topk":       "topk.add",
	"tdigest":    "tdigest.add",
	"vectorset":  "vadd",
	"gears":      "tfcall",
}

func (s *Service) Modules(ctx context.Context) (*ModulesResult, error) {
	result := &ModulesResult{Modules: []any{}, Capabilities: make(map[string]bool, len(capabilityCommands))}

	for capability, command := range capabilityCommands {
		result.Capabilities[capability] = s.redis.Commands().Has(command)
	}

	modules, err := s.onNode(ctx, "", s.anyMaster, nil, "module", "list")
	if err == nil {
		result.Modules = modules.Value
	} else if _, ok := redisx.ReplyError(err); !ok {
		// Servers without MODULE (e.g. some forks) reply with an error, that's fine.
		return nil, err
	}

	return result, nil
}

// commandArgs appends string arguments to a command prefix.
func commandArgs(prefix []any, args []string) []any {
	out := make([]any, 0, len(prefix)+len(args))
	out = append(out, prefix...)

	for _, arg := range args {
		out = append(out, arg)
	}

	return out
}
