package redisx

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
)

type Role string

const (
	RoleMaster   Role = "master"
	RoleReplica  Role = "replica"
	RoleSentinel Role = "sentinel"
)

// Node is a single server of the deployment.
type Node struct {
	Addr   string
	Role   Role
	Client *redis.Client
}

// Nodes returns all known servers: masters, replicas and, in sentinel mode, sentinels.
func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var (
		nodes []Node
		err   error
	)

	switch c.topology {
	case TopologyCluster:
		nodes, err = c.clusterNodes(ctx)
	case TopologySentinel:
		nodes, err = c.sentinelNodes(ctx)
	default:
		nodes = c.standaloneNodes(ctx)
	}

	if err != nil {
		return nil, err
	}

	slices.SortFunc(nodes, func(a, b Node) int {
		return cmp.Or(cmp.Compare(roleOrder(a.Role), roleOrder(b.Role)), strings.Compare(a.Addr, b.Addr))
	})

	return nodes, nil
}

// Node returns the server with the given address.
func (c *Client) Node(ctx context.Context, addr string) (Node, error) {
	nodes, err := c.Nodes(ctx)
	if err != nil {
		return Node{}, err
	}

	for _, node := range nodes {
		if node.Addr == addr {
			return node, nil
		}
	}

	return Node{}, fmt.Errorf("%w: %s", ErrNodeNotFound, addr)
}

func roleOrder(role Role) int {
	switch role {
	case RoleMaster:
		return 0
	case RoleReplica:
		return 1
	default:
		return 2 //nolint:mnd // sentinels go last
	}
}

func (c *Client) clusterNodes(ctx context.Context) ([]Node, error) {
	var (
		mu    sync.Mutex
		nodes []Node
	)

	collect := func(role Role) func(context.Context, *redis.Client) error {
		return func(_ context.Context, client *redis.Client) error {
			mu.Lock()
			defer mu.Unlock()

			nodes = append(nodes, Node{Addr: client.Options().Addr, Role: role, Client: client})

			return nil
		}
	}

	if err := c.cluster.ForEachMaster(ctx, collect(RoleMaster)); err != nil {
		return nil, fmt.Errorf("cluster masters: %w", err)
	}

	if err := c.cluster.ForEachSlave(ctx, collect(RoleReplica)); err != nil {
		return nil, fmt.Errorf("cluster replicas: %w", err)
	}

	return nodes, nil
}

// standaloneNodes returns the configured server and its replication peers from INFO replication.
func (c *Client) standaloneNodes(ctx context.Context) []Node {
	main, _ := c.main.(*redis.Client)
	self := Node{Addr: main.Options().Addr, Role: RoleMaster, Client: main}

	raw, err := main.Info(ctx, "replication").Result()
	if err != nil {
		c.log.DebugContext(ctx, "cannot get replication info", slog.Any("error", err))

		return []Node{self}
	}

	info := ParseInfo(raw)
	nodes := []Node{self}

	if info["role"] == "slave" {
		nodes[0].Role = RoleReplica

		if host, port := info["master_host"], info["master_port"]; host != "" && port != "" {
			addr := net.JoinHostPort(host, port)
			nodes = append(nodes, Node{Addr: addr, Role: RoleMaster, Client: c.directClient(addr, RoleMaster)})
		}

		return nodes
	}

	for key, value := range info {
		if !strings.HasPrefix(key, "slave") || strings.Contains(key, "_") {
			continue
		}

		// slave0:ip=127.0.0.1,port=6380,state=online,offset=1,lag=0
		fields := make(map[string]string)

		for field := range strings.SplitSeq(value, ",") {
			if k, v, ok := strings.Cut(field, "="); ok {
				fields[k] = v
			}
		}

		if fields["ip"] != "" && fields["port"] != "" {
			addr := net.JoinHostPort(fields["ip"], fields["port"])
			nodes = append(nodes, Node{Addr: addr, Role: RoleReplica, Client: c.directClient(addr, RoleReplica)})
		}
	}

	return nodes
}

func (c *Client) sentinelNodes(ctx context.Context) ([]Node, error) {
	reply, err := c.sentinelDo(ctx, "sentinel", "get-master-addr-by-name", c.sentinelMaster)
	if err != nil {
		return nil, fmt.Errorf("get master address: %w", err)
	}

	hostPort, _ := reply.([]any)
	if len(hostPort) != 2 { //nolint:mnd // host and port
		return nil, fmt.Errorf("unexpected master address reply %v", reply)
	}

	masterAddr := net.JoinHostPort(fmt.Sprint(hostPort[0]), fmt.Sprint(hostPort[1]))
	nodes := []Node{{Addr: masterAddr, Role: RoleMaster, Client: c.directClient(masterAddr, RoleMaster)}}

	replicas, err := c.sentinelPeers(ctx, "replicas")
	if err != nil {
		return nil, fmt.Errorf("get replicas: %w", err)
	}

	for _, addr := range replicas {
		nodes = append(nodes, Node{Addr: addr, Role: RoleReplica, Client: c.directClient(addr, RoleReplica)})
	}

	sentinels, err := c.sentinelPeers(ctx, "sentinels")
	if err != nil {
		return nil, fmt.Errorf("get sentinels: %w", err)
	}

	// SENTINEL SENTINELS does not include the sentinel that answered.
	for _, addr := range slices.Compact(slices.Sorted(slices.Values(append(sentinels, c.cfg.Addrs...)))) {
		nodes = append(nodes, Node{Addr: addr, Role: RoleSentinel, Client: c.directClient(addr, RoleSentinel)})
	}

	return nodes, nil
}

// sentinelPeers returns addresses from SENTINEL REPLICAS or SENTINEL SENTINELS.
func (c *Client) sentinelPeers(ctx context.Context, subcommand string) ([]string, error) {
	reply, err := c.sentinelDo(ctx, "sentinel", subcommand, c.sentinelMaster)
	if err != nil {
		return nil, err
	}

	entries, _ := reply.([]any)
	addrs := make([]string, 0, len(entries))

	for _, entry := range entries {
		fields := replyToStringMap(entry)
		if fields["ip"] != "" && fields["port"] != "" {
			addrs = append(addrs, net.JoinHostPort(fields["ip"], fields["port"]))
		}
	}

	return addrs, nil
}

// directClient returns a cached connection to a single server outside of the topology-aware client.
func (c *Client) directClient(addr string, role Role) *redis.Client {
	if main, ok := c.main.(*redis.Client); ok && c.topology == TopologyStandalone && main.Options().Addr == addr {
		return main
	}

	key := addr
	if role == RoleSentinel {
		key = "sentinel|" + addr
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if client, ok := c.direct[key]; ok {
		return client
	}

	opt := c.nodeOptions(addr, c.cfg.DB)
	if role == RoleSentinel {
		opt = c.sentinelOptions(addr)
	}

	client := c.newClient(opt)
	c.direct[key] = client

	return client
}
