package redisx

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Session is a dedicated connection that is never shared with other requests.
// It allows commands that change connection state (SELECT, AUTH, HELLO, MULTI,
// WATCH, CLIENT REPLY, ...) without corrupting pooled connections.
type Session struct {
	Node Node

	client *redis.Client
	conn   *redis.Conn
}

func (s *Session) Conn() *redis.Conn {
	return s.conn
}

func (s *Session) Close() error {
	return errors.Join(s.conn.Close(), s.client.Close())
}

// OpenSession opens a dedicated connection to the node and selects db.
func (c *Client) OpenSession(ctx context.Context, node Node, db int) (*Session, error) {
	if c.topology == TopologyCluster && db != 0 {
		return nil, ErrClusterDB
	}

	opt := c.nodeOptions(node.Addr, db)
	if node.Role == RoleSentinel {
		opt = c.sentinelOptions(node.Addr)
	}

	opt.PoolSize = 1
	// A retry would silently run the rest of the session on a new connection.
	opt.MaxRetries = -1

	client := c.newClient(opt)
	session := &Session{Node: node, client: client, conn: client.Conn()}

	if err := session.conn.Ping(ctx).Err(); err != nil {
		_ = session.Close()

		return nil, fmt.Errorf("open session to %s: %w", node.Addr, err)
	}

	// Replicas of a cluster answer with MOVED unless READONLY is enabled.
	if c.topology == TopologyCluster && node.Role == RoleReplica {
		if err := session.conn.ReadOnly(ctx).Err(); err != nil {
			_ = session.Close()

			return nil, fmt.Errorf("enable READONLY on %s: %w", node.Addr, err)
		}
	}

	return session, nil
}

// MasterFor returns the master serving the key. Without a key it returns any master.
func (c *Client) MasterFor(ctx context.Context, key string, hasKey bool) (Node, error) {
	if c.topology == TopologyCluster && hasKey {
		client, err := c.cluster.MasterForKey(ctx, key)
		if err != nil {
			return Node{}, fmt.Errorf("find master for key: %w", err)
		}

		return Node{Addr: client.Options().Addr, Role: RoleMaster, Client: client}, nil
	}

	if c.topology == TopologyStandalone {
		main, _ := c.main.(*redis.Client)

		return Node{Addr: main.Options().Addr, Role: RoleMaster, Client: main}, nil
	}

	nodes, err := c.Nodes(ctx)
	if err != nil {
		return Node{}, err
	}

	for _, node := range nodes {
		if node.Role == RoleMaster {
			return node, nil
		}
	}

	return Node{}, fmt.Errorf("%w: no master is known", ErrNodeNotFound)
}
