// Package redisx wraps go-redis clients for every supported deployment topology:
// standalone, cluster and sentinel.
package redisx

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/btrvodka/redigate/internal/config"
)

type Topology string

const (
	TopologyStandalone Topology = "standalone"
	TopologyCluster    Topology = "cluster"
	TopologySentinel   Topology = "sentinel"
)

type Client struct {
	cfg       config.Redis
	log       *slog.Logger
	tlsConfig *tls.Config
	hook      *hook
	commands  *CommandTable

	topology       Topology
	sentinelMaster string

	// main serves the default DB: *redis.Client for standalone and sentinel,
	// *redis.ClusterClient for cluster.
	main    redis.UniversalClient
	cluster *redis.ClusterClient

	mu     sync.Mutex
	dbs    map[int]*redis.Client
	direct map[string]*redis.Client
}

// New detects the deployment topology, connects to it and loads the command table.
func New(ctx context.Context, cfg config.Redis, logger *slog.Logger, observer Observer) (*Client, error) {
	tlsConfig, err := buildTLSConfig(cfg.TLS)
	if err != nil {
		return nil, fmt.Errorf("tls config: %w", err)
	}

	commands := &CommandTable{}
	c := &Client{
		cfg:       cfg,
		log:       logger,
		tlsConfig: tlsConfig,
		hook:      &hook{observer: observer, commands: commands},
		commands:  commands,
		dbs:       make(map[int]*redis.Client),
		direct:    make(map[string]*redis.Client),
	}

	if c.topology, err = c.detectTopology(ctx); err != nil {
		return nil, fmt.Errorf("detect topology: %w", err)
	}

	if c.topology == TopologySentinel {
		if c.sentinelMaster, err = c.resolveSentinelMaster(ctx); err != nil {
			_ = c.Close()

			return nil, fmt.Errorf("resolve sentinel master: %w", err)
		}
	}

	switch c.topology {
	case TopologyCluster:
		c.cluster = redis.NewClusterClient(c.clusterOptions())
		c.cluster.AddHook(c.hook)
		c.main = c.cluster
	case TopologySentinel:
		c.main = c.newFailoverClient(cfg.DB)
	case TopologyStandalone:
		c.main = c.newClient(c.nodeOptions(cfg.Addrs[0], cfg.DB))
	}

	if err := c.main.Ping(ctx).Err(); err != nil {
		_ = c.Close()

		return nil, fmt.Errorf("ping: %w", err)
	}

	if err := c.commands.Load(ctx, c.main); err != nil {
		logger.WarnContext(ctx, "cannot load command table, command metadata is unavailable", slog.Any("error", err))
	}

	logger.InfoContext(ctx, "connected to redis",
		slog.String("topology", string(c.topology)),
		slog.Any("addrs", cfg.Addrs),
		slog.String("sentinel_master", c.sentinelMaster),
		slog.Int("commands", c.commands.Len()),
	)

	return c, nil
}

func (c *Client) Topology() Topology {
	return c.topology
}

func (c *Client) SentinelMaster() string {
	return c.sentinelMaster
}

func (c *Client) Commands() *CommandTable {
	return c.commands
}

// Default returns the client of the default database.
func (c *Client) Default() redis.UniversalClient {
	return c.main
}

// DefaultDB returns the database configured at startup.
func (c *Client) DefaultDB() int {
	return c.cfg.DB
}

// DB returns a client for the given logical database. Cluster supports only db 0.
func (c *Client) DB(ctx context.Context, db int) (redis.UniversalClient, error) {
	if db == c.cfg.DB {
		return c.main, nil
	}

	if c.topology == TopologyCluster {
		return nil, ErrClusterDB
	}

	if db < 0 {
		return nil, fmt.Errorf("%w: negative db %d", ErrInvalidDB, db)
	}

	c.mu.Lock()
	client, ok := c.dbs[db]
	c.mu.Unlock()

	if ok {
		return client, nil
	}

	if c.topology == TopologySentinel {
		client = c.newFailoverClient(db)
	} else {
		client = c.newClient(c.nodeOptions(c.cfg.Addrs[0], db))
	}

	// Validates the db index: SELECT fails for indexes the server does not have.
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()

		return nil, fmt.Errorf("%w %d: %w", ErrInvalidDB, db, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.dbs[db]; ok {
		_ = client.Close()

		return existing, nil
	}

	c.dbs[db] = client

	return client, nil
}

// ReloadState refreshes the cluster routing table after topology changes.
func (c *Client) ReloadState(ctx context.Context) {
	if c.cluster != nil {
		c.cluster.ReloadState(ctx)
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error

	if c.main != nil {
		errs = append(errs, c.main.Close())
	}

	for _, client := range c.dbs {
		errs = append(errs, client.Close())
	}

	for _, client := range c.direct {
		errs = append(errs, client.Close())
	}

	clear(c.dbs)
	clear(c.direct)

	return errors.Join(errs...)
}
