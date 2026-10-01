// Package service implements redigate operations on top of redisx.
package service

import (
	"context"
	"sync"
	"time"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/redisx"
)

type Service struct {
	redis  *redisx.Client
	limits config.Limits
}

func New(redis *redisx.Client, limits config.Limits) *Service {
	return &Service{redis: redis, limits: limits}
}

// TopologyName returns standalone, cluster or sentinel.
func (s *Service) TopologyName() string {
	return string(s.redis.Topology())
}

type NodeInfo struct {
	Addr string `json:"addr"`
	Role string `json:"role"`
}

type TopologyInfo struct {
	Topology       string     `json:"topology"`
	SentinelMaster string     `json:"sentinel_master,omitempty"`
	DefaultDB      int        `json:"default_db"`
	Commands       int        `json:"commands"`
	Nodes          []NodeInfo `json:"nodes"`
}

func (s *Service) Topology(ctx context.Context) (*TopologyInfo, error) {
	nodes, err := s.redis.Nodes(ctx)
	if err != nil {
		return nil, err
	}

	result := &TopologyInfo{
		Topology:       string(s.redis.Topology()),
		SentinelMaster: s.redis.SentinelMaster(),
		DefaultDB:      s.redis.DefaultDB(),
		Commands:       s.redis.Commands().Len(),
		Nodes:          make([]NodeInfo, 0, len(nodes)),
	}

	for _, node := range nodes {
		result.Nodes = append(result.Nodes, NodeInfo{Addr: node.Addr, Role: string(node.Role)})
	}

	return result, nil
}

type NodePing struct {
	Addr      string  `json:"addr"`
	Role      string  `json:"role"`
	OK        bool    `json:"ok"`
	LatencyMS float64 `json:"latency_ms"`
	Error     string  `json:"error,omitempty"`
}

type PingResult struct {
	OK    bool       `json:"ok"`
	Nodes []NodePing `json:"nodes"`
}

// Ping pings every node of the deployment concurrently.
func (s *Service) Ping(ctx context.Context) (*PingResult, error) {
	nodes, err := s.redis.Nodes(ctx)
	if err != nil {
		return nil, err
	}

	pings := forEachNode(ctx, nodes, func(ctx context.Context, node redisx.Node) NodePing {
		start := time.Now()
		err := node.Client.Ping(ctx).Err()

		ping := NodePing{
			Addr:      node.Addr,
			Role:      string(node.Role),
			OK:        err == nil,
			LatencyMS: float64(time.Since(start).Microseconds()) / 1000, //nolint:mnd // µs to ms
		}
		if err != nil {
			ping.Error = err.Error()
		}

		return ping
	})

	result := &PingResult{OK: true, Nodes: pings}
	for _, ping := range pings {
		result.OK = result.OK && ping.OK
	}

	return result, nil
}

// Ready checks that the default database is reachable.
func (s *Service) Ready(ctx context.Context) error {
	client, err := s.redis.DB(ctx, s.redis.DefaultDB())
	if err != nil {
		return err
	}

	return client.Ping(ctx).Err()
}

// forEachNode runs fn on every node concurrently and returns results in the order of nodes.
func forEachNode[T any](ctx context.Context, nodes []redisx.Node, fn func(context.Context, redisx.Node) T) []T {
	results := make([]T, len(nodes))

	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Go(func() {
			results[i] = fn(ctx, node)
		})
	}

	wg.Wait()

	return results
}
