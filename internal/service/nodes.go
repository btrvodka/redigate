package service

import (
	"context"
	"fmt"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/redisx"
)

// NodeSelector chooses nodes for per-node operations: a single node by address
// or a group by target. An empty selector means the default target of the operation.
type NodeSelector struct {
	Node   string
	Target Target
	DB     *int
}

func (s *Service) selectNodes(ctx context.Context, sel NodeSelector, defaultTarget Target) ([]redisx.Node, error) {
	if sel.Node != "" {
		node, err := s.redis.Node(ctx, sel.Node)
		if err != nil {
			return nil, err
		}

		return []redisx.Node{node}, nil
	}

	target := sel.Target
	if target == "" || target == TargetAuto {
		target = defaultTarget
	}

	if target == "" {
		return nil, fmt.Errorf("%w: node or target is required for this operation", ErrInvalidRequest)
	}

	if target == TargetNode {
		return nil, fmt.Errorf("%w: node is required for target=node", ErrInvalidRequest)
	}

	return s.targetNodes(ctx, target)
}

// perNode runs fn on the selected nodes concurrently and collects per-node results.
func (s *Service) perNode(
	ctx context.Context,
	sel NodeSelector,
	defaultTarget Target,
	fn func(ctx context.Context, node redisx.Node) (any, error),
) (*FanOutResult, error) {
	nodes, err := s.selectNodes(ctx, sel, defaultTarget)
	if err != nil {
		return nil, err
	}

	results := forEachNode(ctx, nodes, func(ctx context.Context, node redisx.Node) NodeResult {
		result := NodeResult{Addr: node.Addr, Role: string(node.Role)}

		value, err := fn(ctx, node)
		if err = ignoreNil(err); err != nil {
			result.Error = err.Error()
		} else {
			result.Value = value
		}

		return result
	})

	return newFanOutResult(results), nil
}

// perNodeCommand runs a command on the selected nodes and converts replies with parse.
// A nil parse encodes replies as they are.
func (s *Service) perNodeCommand(
	ctx context.Context,
	sel NodeSelector,
	defaultTarget Target,
	parse func(reply any) (any, error),
	args ...any,
) (*FanOutResult, error) {
	db := s.db(sel.DB)

	return s.perNode(ctx, sel, defaultTarget, func(ctx context.Context, node redisx.Node) (any, error) {
		reply, err := s.execOnNode(ctx, node, db, false, args)
		if err != nil {
			return nil, err
		}

		if parse != nil {
			return parse(reply)
		}

		return codec.NewEncoder(codec.EncodingAuto, s.limits.MaxResponseItems).Encode(reply), nil
	})
}

// onNode runs a command on one node: the given one or the default chosen by pick.
func (s *Service) onNode(
	ctx context.Context,
	addr string,
	pick func(ctx context.Context) (redisx.Node, error),
	parse func(reply any) (any, error),
	args ...any,
) (*CommandResult, error) {
	var (
		node redisx.Node
		err  error
	)

	if addr != "" {
		node, err = s.redis.Node(ctx, addr)
	} else {
		node, err = pick(ctx)
	}

	if err != nil {
		return nil, err
	}

	reply, err := node.Client.Do(ctx, args...).Result()
	if err = ignoreNil(err); err != nil {
		return nil, err
	}

	result := &CommandResult{Node: node.Addr}

	if parse == nil {
		enc := codec.NewEncoder(codec.EncodingAuto, s.limits.MaxResponseItems)
		result.Value, result.Truncated = enc.Encode(reply), enc.Truncated()
	} else if result.Value, err = parse(reply); err != nil {
		return nil, err
	}

	return result, nil
}

func (s *Service) anyMaster(ctx context.Context) (redisx.Node, error) {
	return s.redis.MasterFor(ctx, "", false)
}
