package service

import (
	"context"
	"fmt"
	"strings"
)

// The script cache, functions and ACL are kept by every node separately, so
// changes are sent to all of them by default: scripts and ACL to all data
// nodes (EVALSHA_RO and authentication may hit replicas), functions to masters
// (replicas receive them through replication).

func (s *Service) ScriptLoad(ctx context.Context, sel NodeSelector, script string) (*FanOutResult, error) {
	if script == "" {
		return nil, fmt.Errorf("%w: script is required", ErrInvalidRequest)
	}

	return s.perNodeCommand(ctx, sel, TargetAll, nil, "script", "load", script)
}

type ScriptExistsResult struct {
	// Everywhere tells for every sha whether all selected nodes have the script.
	Everywhere map[string]bool `json:"everywhere"`
	Nodes      []NodeResult    `json:"nodes"`
}

func (s *Service) ScriptExists(ctx context.Context, sel NodeSelector, shas []string) (*ScriptExistsResult, error) {
	if len(shas) == 0 {
		return nil, fmt.Errorf("%w: sha is required", ErrInvalidRequest)
	}

	res, err := s.perNodeCommand(ctx, sel, TargetAll, okRaw, commandArgs([]any{"script", "exists"}, shas)...)
	if err != nil {
		return nil, err
	}

	result := &ScriptExistsResult{Everywhere: make(map[string]bool, len(shas)), Nodes: res.Nodes}
	for _, sha := range shas {
		result.Everywhere[sha] = true
	}

	for _, node := range res.Nodes {
		flags, _ := node.Value.([]any)

		for i, sha := range shas {
			if node.Error != "" || i >= len(flags) || !truthy(flags[i]) {
				result.Everywhere[sha] = false
			}
		}
	}

	return result, nil
}

// truthy accepts 1 (RESP2) and true (RESP3).
func truthy(v any) bool {
	switch v := v.(type) {
	case int64:
		return v == 1
	case bool:
		return v
	default:
		return false
	}
}

// ScriptFlush omits SYNC by default: servers before 6.2 don't accept a mode.
func (s *Service) ScriptFlush(ctx context.Context, sel NodeSelector, async bool) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, withAsync([]any{"script", "flush"}, async)...)
}

func withAsync(args []any, async bool) []any {
	if async {
		return append(args, "async")
	}

	return args
}

// ScriptKill stops a running read-only script; NOTBUSY on idle nodes is reported per node.
func (s *Service) ScriptKill(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "script", "kill")
}

func (s *Service) FunctionList(ctx context.Context, sel NodeSelector, library string, withCode bool) (*FanOutResult, error) {
	args := []any{"function", "list"}
	if library != "" {
		args = append(args, "libraryname", library)
	}

	if withCode {
		args = append(args, "withcode")
	}

	return s.perNodeCommand(ctx, sel, TargetMasters, shapeFunctionList, args...)
}

// shapeFunctionList converts nested flat key-value lists of FUNCTION LIST to objects.
func shapeFunctionList(reply any) (any, error) {
	libraries, ok := reply.([]any)
	if !ok {
		return reply, nil
	}

	out := make([]any, 0, len(libraries))

	for _, library := range libraries {
		lib, ok := ShapeMap(library).(map[any]any)
		if !ok {
			out = append(out, library)

			continue
		}

		lib["functions"] = ShapeMapList(lib["functions"])
		out = append(out, lib)
	}

	return encodeAll(out), nil
}

func (s *Service) FunctionLoad(ctx context.Context, sel NodeSelector, code string, replace bool) (*FanOutResult, error) {
	if code == "" {
		return nil, fmt.Errorf("%w: code is required", ErrInvalidRequest)
	}

	args := []any{"function", "load"}
	if replace {
		args = append(args, "replace")
	}

	return s.perNodeCommand(ctx, sel, TargetMasters, nil, append(args, code)...)
}

func (s *Service) FunctionDelete(ctx context.Context, sel NodeSelector, library string) (*FanOutResult, error) {
	if library == "" {
		return nil, fmt.Errorf("%w: library is required", ErrInvalidRequest)
	}

	return s.perNodeCommand(ctx, sel, TargetMasters, nil, "function", "delete", library)
}

func (s *Service) FunctionFlush(ctx context.Context, sel NodeSelector, async bool) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetMasters, nil, withAsync([]any{"function", "flush"}, async)...)
}

func (s *Service) FunctionKill(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetMasters, nil, "function", "kill")
}

func (s *Service) FunctionStats(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "function", "stats")
}

// FunctionDump returns the serialized libraries of one master (any by default) as base64.
func (s *Service) FunctionDump(ctx context.Context, node string) (*CommandResult, error) {
	return s.onNode(ctx, node, s.anyMaster, func(reply any) (any, error) {
		return encodeBase64(reply), nil
	}, "function", "dump")
}

// FunctionRestore loads a dump on the masters; policy is APPEND, REPLACE or FLUSH.
func (s *Service) FunctionRestore(ctx context.Context, sel NodeSelector, dump []byte, policy string) (*FanOutResult, error) {
	if len(dump) == 0 {
		return nil, fmt.Errorf("%w: dump is required", ErrInvalidRequest)
	}

	args := []any{"function", "restore", dump}
	if policy != "" {
		args = append(args, strings.ToUpper(policy))
	}

	return s.perNodeCommand(ctx, sel, TargetMasters, nil, args...)
}
