package service

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/redisx"
)

type PipelineOptions struct {
	ExecOptions

	// Atomic wraps commands into MULTI/EXEC.
	Atomic bool
	// Session runs commands on a dedicated connection, which allows
	// SELECT, AUTH, MULTI, WATCH and other connection-scoped commands.
	Session bool
}

type PipelineEntry struct {
	Value any    `json:"value"`
	Error string `json:"error,omitempty"`
}

type PipelineResult struct {
	Node      string          `json:"node,omitempty"`
	Results   []PipelineEntry `json:"results"`
	Truncated bool            `json:"truncated,omitempty"`
}

// Pipeline sends commands in one round trip and returns a result for each of them.
// In cluster mode non-atomic pipelines are split by slot, atomic pipelines and
// sessions require all keys to hash to the same slot.
func (s *Service) Pipeline(ctx context.Context, commands []codec.Args, opts PipelineOptions) (*PipelineResult, error) {
	if len(commands) == 0 {
		return nil, fmt.Errorf("%w: no commands", ErrInvalidRequest)
	}

	if len(commands) > s.limits.MaxPipelineCommands {
		return nil, fmt.Errorf("%w: %d commands exceed the limit of %d",
			ErrInvalidRequest, len(commands), s.limits.MaxPipelineCommands)
	}

	if opts.Target.fanOut() {
		return nil, fmt.Errorf("%w: pipelines support only target=auto and target=node", ErrInvalidRequest)
	}

	if err := s.checkTarget(opts.ExecOptions); err != nil {
		return nil, err
	}

	keys := make([][]string, len(commands))

	for i, args := range commands {
		spec, err := s.checkCommand(args, opts.ReadOnly)
		if err != nil {
			return nil, fmt.Errorf("command %d: %w", i, err)
		}

		if !opts.Session && sessionCommands.match(args) {
			return nil, fmt.Errorf("%w: command %d: %s changes connection state, set session=true",
				ErrInvalidRequest, i, commandTitle(spec, args))
		}

		// Keys pick the slot owner for transactions and sessions.
		if opts.Atomic || opts.Session {
			keys[i] = s.clusterKeys(ctx, spec, args)
		}
	}

	db := s.db(opts.DB)

	node, pipe, cleanup, err := s.openPipeline(ctx, keys, db, opts)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	cmds := make([]*redis.Cmd, len(commands))
	for i, args := range commands {
		cmds[i] = pipe.Do(ctx, args...)
	}

	// Errors are reported per command.
	_, _ = pipe.Exec(ctx)

	enc := codec.NewEncoder(opts.Encoding, s.limits.MaxResponseItems)
	result := &PipelineResult{Node: node.Addr, Results: make([]PipelineEntry, len(cmds))}

	for i, cmd := range cmds {
		reply, err := cmd.Result()
		if err = ignoreNil(err); err != nil {
			result.Results[i].Error = err.Error()

			continue
		}

		result.Results[i].Value = enc.Encode(reply)
	}

	result.Truncated = enc.Truncated()

	return result, nil
}

// openPipeline chooses where the pipeline runs. Node is empty for pipelines
// routed by the topology-aware client.
func (s *Service) openPipeline(
	ctx context.Context,
	keys [][]string,
	db int,
	opts PipelineOptions,
) (redisx.Node, redis.Pipeliner, func(), error) {
	cluster := s.redis.Topology() == redisx.TopologyCluster
	// go-redis splits cluster transactions by slot, a dedicated connection keeps them atomic.
	session := opts.Session || (opts.Atomic && cluster)

	var (
		node redisx.Node
		err  error
	)

	switch {
	case opts.Target == TargetNode:
		if node, err = s.redis.Node(ctx, opts.Node); err != nil {
			return redisx.Node{}, nil, nil, err
		}

		session = session || (node.Role != redisx.RoleSentinel && db != s.redis.DefaultDB())
	case session:
		if node, err = s.autoNode(ctx, keys); err != nil {
			return redisx.Node{}, nil, nil, err
		}
	default:
		client, err := s.redis.DB(ctx, db)
		if err != nil {
			return redisx.Node{}, nil, nil, err
		}

		return redisx.Node{}, newPipe(client, opts.Atomic), func() {}, nil
	}

	if !session {
		return node, newPipe(node.Client, opts.Atomic), func() {}, nil
	}

	sess, err := s.redis.OpenSession(ctx, node, db)
	if err != nil {
		return redisx.Node{}, nil, nil, err
	}

	return node, newPipe(sess.Conn(), opts.Atomic), func() { _ = sess.Close() }, nil
}

type pipeliner interface {
	Pipeline() redis.Pipeliner
	TxPipeline() redis.Pipeliner
}

func newPipe(client pipeliner, atomic bool) redis.Pipeliner {
	if atomic {
		return client.TxPipeline()
	}

	return client.Pipeline()
}
