package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/btrvodka/redigate/internal/redisx"
)

func (s *Service) requireCluster() error {
	if s.redis.Topology() != redisx.TopologyCluster {
		return fmt.Errorf("%w: the server is not a cluster", redisx.ErrNotSupported)
	}

	return nil
}

func requireNode(context.Context) (redisx.Node, error) {
	return redisx.Node{}, fmt.Errorf("%w: node is required for this operation", ErrInvalidRequest)
}

// clusterCall runs a cluster command on the node, or on any master if node is empty.
func (s *Service) clusterCall(
	ctx context.Context,
	node string,
	pick func(context.Context) (redisx.Node, error),
	parse func(any) (any, error),
	args ...any,
) (*CommandResult, error) {
	if err := s.requireCluster(); err != nil {
		return nil, err
	}

	return s.onNode(ctx, node, pick, parse, args...)
}

// clusterChange runs a command that changes the cluster layout and reloads the routing table.
func (s *Service) clusterChange(ctx context.Context, node string, args ...any) (*CommandResult, error) {
	result, err := s.clusterCall(ctx, node, requireNode, nil, args...)
	if err != nil {
		return nil, err
	}

	s.redis.ReloadState(ctx)

	return result, nil
}

func (s *Service) ClusterInfo(ctx context.Context, node string) (*CommandResult, error) {
	return s.clusterCall(ctx, node, s.anyMaster, parseColonLines, "cluster", "info")
}

func (s *Service) ClusterNodes(ctx context.Context, node string) (*CommandResult, error) {
	return s.clusterCall(ctx, node, s.anyMaster, parseClusterNodes, "cluster", "nodes")
}

func (s *Service) ClusterShards(ctx context.Context, node string) (*CommandResult, error) {
	return s.clusterCall(ctx, node, s.anyMaster, nil, "cluster", "shards")
}

func (s *Service) ClusterSlots(ctx context.Context, node string) (*CommandResult, error) {
	return s.clusterCall(ctx, node, s.anyMaster, nil, "cluster", "slots")
}

type KeySlotResult struct {
	Key    string `json:"key"`
	Slot   int    `json:"slot"`
	Master string `json:"master"`
}

func (s *Service) KeySlot(ctx context.Context, key string) (*KeySlotResult, error) {
	if err := s.requireCluster(); err != nil {
		return nil, err
	}

	// The server is the source of truth: the client routing table may lag behind a failover.
	slot := redisx.Slot(key)

	master, err := s.slotOwnerAddr(ctx, slot)
	if err != nil {
		return nil, err
	}

	return &KeySlotResult{Key: key, Slot: slot, Master: master}, nil
}

type SlotKeysResult struct {
	Slot  int      `json:"slot"`
	Node  string   `json:"node"`
	Count int64    `json:"count"`
	Keys  []string `json:"keys"`
}

// SlotKeys returns the number of keys in the slot and up to count of them from the slot owner.
func (s *Service) SlotKeys(ctx context.Context, slot, count int) (*SlotKeysResult, error) {
	if err := s.requireCluster(); err != nil {
		return nil, err
	}

	if slot < 0 || slot >= redisx.SlotCount {
		return nil, fmt.Errorf("%w: slot must be in [0, %d)", ErrInvalidRequest, redisx.SlotCount)
	}

	count = min(max(count, 1), s.limits.MaxResponseItems)

	addr, err := s.slotOwnerAddr(ctx, slot)
	if err != nil {
		return nil, err
	}

	owner, err := s.redis.Node(ctx, addr)
	if err != nil {
		return nil, err
	}

	total, err := owner.Client.ClusterCountKeysInSlot(ctx, slot).Result()
	if err != nil {
		return nil, err //nolint:wrapcheck // mapped by the API
	}

	keys, err := owner.Client.ClusterGetKeysInSlot(ctx, slot, count).Result()
	if err != nil {
		return nil, err //nolint:wrapcheck // mapped by the API
	}

	return &SlotKeysResult{Slot: slot, Node: owner.Addr, Count: total, Keys: keys}, nil
}

// slotOwnerAddr returns the master serving the slot according to CLUSTER NODES.
// During a failover it may name a node the client routing table doesn't know yet.
func (s *Service) slotOwnerAddr(ctx context.Context, slot int) (string, error) {
	res, err := s.ClusterNodes(ctx, "")
	if err != nil {
		return "", err
	}

	nodes, _ := res.Value.([]ClusterNode)
	for _, node := range nodes {
		if node.Role == "master" && slotInRanges(slot, node.Slots) {
			return node.Addr, nil
		}
	}

	return "", fmt.Errorf("%w: slot %d is not served by any master", redisx.ErrNodeNotFound, slot)
}

// slotInRanges checks CLUSTER NODES slot entries: "5", "0-5460"; "[5->-id]" (migrating) is skipped.
func slotInRanges(slot int, ranges []string) bool {
	for _, r := range ranges {
		if strings.HasPrefix(r, "[") {
			continue
		}

		from, to, isRange := strings.Cut(r, "-")
		if !isRange {
			to = from
		}

		lo, err1 := strconv.Atoi(from)
		hi, err2 := strconv.Atoi(to)

		if err1 == nil && err2 == nil && slot >= lo && slot <= hi {
			return true
		}
	}

	return false
}

// Failover promotes the replica given by node. Mode is "", "force" or "takeover".
func (s *Service) Failover(ctx context.Context, node, mode string) (*CommandResult, error) {
	args := []any{"cluster", "failover"}

	switch mode = strings.ToLower(mode); mode {
	case "":
	case "force", "takeover":
		args = append(args, mode)
	default:
		return nil, fmt.Errorf("%w: mode must be force or takeover", ErrInvalidRequest)
	}

	return s.clusterChange(ctx, node, args...)
}

// Meet connects the node (any master by default) with a node at ip:port.
func (s *Service) Meet(ctx context.Context, node, ip string, port, busPort int) (*CommandResult, error) {
	if ip == "" || port <= 0 {
		return nil, fmt.Errorf("%w: ip and port are required", ErrInvalidRequest)
	}

	args := []any{"cluster", "meet", ip, port}
	if busPort > 0 {
		args = append(args, busPort)
	}

	result, err := s.clusterCall(ctx, node, s.anyMaster, nil, args...)
	if err != nil {
		return nil, err
	}

	s.redis.ReloadState(ctx)

	return result, nil
}

// Forget removes a node from the cluster: CLUSTER FORGET is sent to every other node.
func (s *Service) Forget(ctx context.Context, nodeID string) (*FanOutResult, error) {
	if err := s.requireCluster(); err != nil {
		return nil, err
	}

	if nodeID == "" {
		return nil, fmt.Errorf("%w: node_id is required", ErrInvalidRequest)
	}

	result, err := s.perNodeCommand(ctx, NodeSelector{}, TargetAll, nil, "cluster", "forget", nodeID)
	if err != nil {
		return nil, err
	}

	s.redis.ReloadState(ctx)

	return result, nil
}

func (s *Service) Replicate(ctx context.Context, node, masterID string) (*CommandResult, error) {
	if masterID == "" {
		return nil, fmt.Errorf("%w: master_id is required", ErrInvalidRequest)
	}

	return s.clusterChange(ctx, node, "cluster", "replicate", masterID)
}

func (s *Service) ClusterReset(ctx context.Context, node string, hard bool) (*CommandResult, error) {
	mode := "soft"
	if hard {
		mode = "hard"
	}

	return s.clusterChange(ctx, node, "cluster", "reset", mode)
}

// ChangeSlots assigns (add=true) or removes slots and slot ranges on the node.
func (s *Service) ChangeSlots(ctx context.Context, node string, add bool, slots []int, ranges [][2]int) (*CommandResult, error) {
	if len(slots) == 0 && len(ranges) == 0 {
		return nil, fmt.Errorf("%w: slots or ranges are required", ErrInvalidRequest)
	}

	if len(slots) > 0 && len(ranges) > 0 {
		return nil, fmt.Errorf("%w: slots and ranges are mutually exclusive", ErrInvalidRequest)
	}

	cmd := "delslots"
	if add {
		cmd = "addslots"
	}

	args := []any{"cluster", cmd}

	for _, slot := range slots {
		args = append(args, slot)
	}

	if len(ranges) > 0 {
		args[1] = cmd + "range"

		for _, r := range ranges {
			args = append(args, r[0], r[1])
		}
	}

	return s.clusterChange(ctx, node, args...)
}

// SetSlot changes the state of a slot: importing, migrating, stable or node.
func (s *Service) SetSlot(ctx context.Context, node string, slot int, state, nodeID string) (*CommandResult, error) {
	args := []any{"cluster", "setslot", slot}

	switch state = strings.ToLower(state); state {
	case "stable":
		args = append(args, state)
	case "importing", "migrating", "node":
		if nodeID == "" {
			return nil, fmt.Errorf("%w: node_id is required for state %s", ErrInvalidRequest, state)
		}

		args = append(args, state, nodeID)
	default:
		return nil, fmt.Errorf("%w: state must be importing, migrating, stable or node", ErrInvalidRequest)
	}

	return s.clusterChange(ctx, node, args...)
}
