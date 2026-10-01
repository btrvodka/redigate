package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/redis/go-redis/v9"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/redisx"
)

// Target selects the servers a command is sent to.
type Target string

const (
	// TargetAuto routes by key: the slot owner in cluster, the master otherwise.
	TargetAuto Target = "auto"
	// TargetNode sends the command to the node given by address.
	TargetNode      Target = "node"
	TargetMasters   Target = "masters"
	TargetReplicas  Target = "replicas"
	TargetAll       Target = "all"
	TargetSentinels Target = "sentinels"
)

func ParseTarget(s string) (Target, error) {
	switch t := Target(s); t {
	case "":
		return TargetAuto, nil
	case TargetAuto, TargetNode, TargetMasters, TargetReplicas, TargetAll, TargetSentinels:
		return t, nil
	default:
		return "", fmt.Errorf("%w: unknown target %q, expected auto, node, masters, replicas, all or sentinels",
			ErrInvalidRequest, s)
	}
}

func (t Target) fanOut() bool {
	return t != TargetAuto && t != TargetNode
}

type ExecOptions struct {
	Target   Target
	Node     string
	DB       *int
	Encoding codec.Encoding
	ReadOnly bool
	// Shape converts a raw reply to a friendlier form before encoding.
	Shape func(reply any) any
}

func (o ExecOptions) shape(reply any) any {
	if o.Shape == nil || reply == nil {
		return reply
	}

	return o.Shape(reply)
}

type CommandResult struct {
	Node      string `json:"node,omitempty"`
	Value     any    `json:"value"`
	Truncated bool   `json:"truncated,omitempty"`
}

type NodeResult struct {
	Addr  string `json:"addr"`
	Role  string `json:"role"`
	Value any    `json:"value"`
	Error string `json:"error,omitempty"`
}

type FanOutResult struct {
	// OK is false if the command failed on at least one node.
	OK        bool         `json:"ok"`
	Nodes     []NodeResult `json:"nodes"`
	Truncated bool         `json:"truncated,omitempty"`
}

func newFanOutResult(nodes []NodeResult) *FanOutResult {
	result := &FanOutResult{OK: true, Nodes: nodes}
	for _, node := range nodes {
		result.OK = result.OK && node.Error == ""
	}

	return result
}

// Exec runs a single command. It returns *CommandResult or, for fan-out targets, *FanOutResult.
func (s *Service) Exec(ctx context.Context, args codec.Args, opts ExecOptions) (any, error) {
	spec, err := s.checkCommand(args, opts.ReadOnly)
	if err != nil {
		return nil, err
	}

	if err := s.checkTarget(opts); err != nil {
		return nil, err
	}

	db := s.db(opts.DB)
	session := sessionCommands.match(args)
	enc := codec.NewEncoder(opts.Encoding, s.limits.MaxResponseItems)

	if opts.Target.fanOut() {
		if session {
			return nil, fmt.Errorf("%w: %s changes connection state and can't be sent to several nodes",
				ErrInvalidRequest, commandTitle(spec, args))
		}

		return s.execFanOut(ctx, args, opts, db, enc)
	}

	var (
		node  redisx.Node
		reply any
	)

	switch {
	case opts.Target == TargetNode:
		if node, err = s.redis.Node(ctx, opts.Node); err != nil {
			return nil, err
		}

		reply, err = s.execOnNode(ctx, node, db, session, args)
	case session:
		if node, err = s.autoNode(ctx, [][]string{s.clusterKeys(ctx, spec, args)}); err != nil {
			return nil, err
		}

		reply, err = s.execOnNode(ctx, node, db, true, args)
	default:
		var client redis.UniversalClient
		if client, err = s.redis.DB(ctx, db); err != nil {
			return nil, err
		}

		reply, err = client.Do(ctx, args...).Result()
	}

	if err = ignoreNil(err); err != nil {
		return nil, err
	}

	return &CommandResult{Node: node.Addr, Value: enc.Encode(opts.shape(reply)), Truncated: enc.Truncated()}, nil
}

func (s *Service) execFanOut(ctx context.Context, args codec.Args, opts ExecOptions, db int, enc *codec.Encoder) (*FanOutResult, error) {
	nodes, err := s.targetNodes(ctx, opts.Target)
	if err != nil {
		return nil, err
	}

	type nodeReply struct {
		reply any
		err   error
	}

	replies := forEachNode(ctx, nodes, func(ctx context.Context, node redisx.Node) nodeReply {
		reply, err := s.execOnNode(ctx, node, db, false, args)

		return nodeReply{reply: reply, err: ignoreNil(err)}
	})

	items := make([]NodeResult, 0, len(nodes))

	// Encoding is sequential: the encoder shares the item budget between nodes.
	for i, node := range nodes {
		item := NodeResult{Addr: node.Addr, Role: string(node.Role)}
		if replies[i].err != nil {
			item.Error = replies[i].err.Error()
		} else {
			item.Value = enc.Encode(opts.shape(replies[i].reply))
		}

		items = append(items, item)
	}

	result := newFanOutResult(items)
	result.Truncated = enc.Truncated()

	return result, nil
}

// execOnNode runs a command on the node, on a dedicated connection if required.
func (s *Service) execOnNode(ctx context.Context, node redisx.Node, db int, session bool, args codec.Args) (any, error) {
	needDB := node.Role != redisx.RoleSentinel && db != s.redis.DefaultDB()

	if !session && !needDB {
		return node.Client.Do(ctx, args...).Result() //nolint:wrapcheck // redis errors are mapped by the API
	}

	sess, err := s.redis.OpenSession(ctx, node, db)
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	return sess.Conn().Do(ctx, args...).Result() //nolint:wrapcheck // redis errors are mapped by the API
}

func (s *Service) targetNodes(ctx context.Context, target Target) ([]redisx.Node, error) {
	nodes, err := s.redis.Nodes(ctx)
	if err != nil {
		return nil, err
	}

	return slices.DeleteFunc(nodes, func(node redisx.Node) bool {
		switch target {
		case TargetMasters:
			return node.Role != redisx.RoleMaster
		case TargetReplicas:
			return node.Role != redisx.RoleReplica
		case TargetSentinels:
			return node.Role != redisx.RoleSentinel
		default:
			return node.Role == redisx.RoleSentinel
		}
	}), nil
}

// autoNode picks the node for a dedicated connection: the master owning the
// slot of all keys in cluster, the master otherwise.
func (s *Service) autoNode(ctx context.Context, keys [][]string) (redisx.Node, error) {
	var all []string
	for _, k := range keys {
		all = append(all, k...)
	}

	if s.redis.Topology() == redisx.TopologyCluster {
		if err := sameSlot(all); err != nil {
			return redisx.Node{}, err
		}
	}

	if len(all) == 0 {
		return s.redis.MasterFor(ctx, "", false)
	}

	return s.redis.MasterFor(ctx, all[0], true)
}

func sameSlot(keys []string) error {
	for _, key := range keys[min(1, len(keys)):] {
		if redisx.Slot(key) != redisx.Slot(keys[0]) {
			return fmt.Errorf("%w: %q and %q", ErrCrossSlot, keys[0], key)
		}
	}

	return nil
}

// clusterKeys returns key arguments of the command. Keys matter only for slot
// routing, so outside of cluster it returns nil without asking the server.
func (s *Service) clusterKeys(ctx context.Context, spec *redisx.CommandSpec, args codec.Args) []string {
	if spec == nil || s.redis.Topology() != redisx.TopologyCluster {
		return nil
	}

	if spec.MovableKeys() {
		getKeys := append([]any{"command", "getkeys"}, args...)

		// Fails for commands without keys, that is fine.
		keys, err := s.redis.Default().Do(ctx, getKeys...).StringSlice()
		if err != nil {
			return nil
		}

		return keys
	}

	indexes := spec.KeyIndexes(len(args))
	keys := make([]string, 0, len(indexes))

	for _, i := range indexes {
		keys = append(keys, args.String(i))
	}

	return keys
}

func (s *Service) checkTarget(opts ExecOptions) error {
	if opts.Target == TargetNode && opts.Node == "" {
		return fmt.Errorf("%w: node is required for target=node", ErrInvalidRequest)
	}

	if opts.Target != TargetNode && opts.Node != "" {
		return fmt.Errorf("%w: node is allowed only with target=node", ErrInvalidRequest)
	}

	if opts.DB != nil && *opts.DB < 0 {
		return fmt.Errorf("%w: db must not be negative", ErrInvalidRequest)
	}

	return nil
}

func (s *Service) db(db *int) int {
	if db == nil {
		return s.redis.DefaultDB()
	}

	return *db
}

// ignoreNil treats a nil reply as a successful empty result.
func ignoreNil(err error) error {
	if errors.Is(err, redis.Nil) {
		return nil
	}

	return err
}
