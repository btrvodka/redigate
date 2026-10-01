package redisx

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"

	"github.com/btrvodka/redigate/internal/config"
)

const (
	clientName = "redigate"

	clusterStateReloadInterval = 2 * time.Second
)

func buildTLSConfig(cfg config.TLS) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil //nolint:nilnil // nil config disables TLS
	}

	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         cfg.ServerName,
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // explicitly requested by user
	}

	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}

		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("no certificates found in CA file")
		}

		tlsCfg.RootCAs = pool
	}

	if cfg.CertFile != "" || cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}

		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
}

func disabledMaintNotifications() *maintnotifications.Config {
	return &maintnotifications.Config{Mode: maintnotifications.ModeDisabled}
}

// nodeOptions returns options for a direct connection to a single data node.
func (c *Client) nodeOptions(addr string, db int) *redis.Options {
	return &redis.Options{
		Addr:                     addr,
		ClientName:               clientName,
		Protocol:                 c.cfg.Protocol,
		Username:                 c.cfg.Username,
		Password:                 c.cfg.Password,
		DB:                       db,
		MaxRetries:               c.cfg.MaxRetries,
		DialTimeout:              c.cfg.DialTimeout,
		ReadTimeout:              c.cfg.ReadTimeout,
		WriteTimeout:             c.cfg.WriteTimeout,
		PoolSize:                 c.cfg.PoolSize,
		TLSConfig:                c.tlsConfig,
		ContextTimeoutEnabled:    true,
		MaintNotificationsConfig: disabledMaintNotifications(),
	}
}

// sentinelOptions returns options for a direct connection to a sentinel.
func (c *Client) sentinelOptions(addr string) *redis.Options {
	opt := c.nodeOptions(addr, 0)
	opt.Username = c.cfg.SentinelUsername
	opt.Password = c.cfg.SentinelPassword

	return opt
}

func (c *Client) clusterOptions() *redis.ClusterOptions {
	return &redis.ClusterOptions{
		Addrs:         c.cfg.Addrs,
		ClientName:    clientName,
		NewClient:     c.newClient,
		MaxRedirects:  c.cfg.MaxRedirects,
		ReadOnly:      c.cfg.ReadFromReplicas,
		RouteRandomly: c.cfg.ReadFromReplicas,
		Protocol:      c.cfg.Protocol,
		Username:      c.cfg.Username,
		Password:      c.cfg.Password,
		MaxRetries:    c.cfg.MaxRetries,
		DialTimeout:   c.cfg.DialTimeout,
		ReadTimeout:   c.cfg.ReadTimeout,
		WriteTimeout:  c.cfg.WriteTimeout,
		PoolSize:      c.cfg.PoolSize,
		TLSConfig:     c.tlsConfig,
		// Cancel commands when the HTTP request is canceled or times out.
		ContextTimeoutEnabled: true,
		// MasterForKey uses the cached routing table, keep it fresh after failovers.
		ClusterStateReloadInterval: clusterStateReloadInterval,
		// Fan-out is controlled explicitly by the API, see the "target" parameter.
		DisableRoutingPolicies:   true,
		MaintNotificationsConfig: disabledMaintNotifications(),
	}
}

func (c *Client) failoverOptions(db int) *redis.FailoverOptions {
	return &redis.FailoverOptions{
		MasterName:            c.sentinelMaster,
		SentinelAddrs:         c.cfg.Addrs,
		SentinelUsername:      c.cfg.SentinelUsername,
		SentinelPassword:      c.cfg.SentinelPassword,
		ClientName:            clientName,
		Protocol:              c.cfg.Protocol,
		Username:              c.cfg.Username,
		Password:              c.cfg.Password,
		DB:                    db,
		MaxRetries:            c.cfg.MaxRetries,
		DialTimeout:           c.cfg.DialTimeout,
		ReadTimeout:           c.cfg.ReadTimeout,
		WriteTimeout:          c.cfg.WriteTimeout,
		PoolSize:              c.cfg.PoolSize,
		TLSConfig:             c.tlsConfig,
		ContextTimeoutEnabled: true,
	}
}

// newClient creates a node client with the observability hook attached.
// Every *redis.Client in the package must be created through it.
func (c *Client) newClient(opt *redis.Options) *redis.Client {
	client := redis.NewClient(opt)
	client.AddHook(c.hook)

	return client
}

func (c *Client) newFailoverClient(db int) *redis.Client {
	client := redis.NewFailoverClient(c.failoverOptions(db))
	client.AddHook(c.hook)

	return client
}
