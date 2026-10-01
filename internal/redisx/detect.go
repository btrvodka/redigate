package redisx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/btrvodka/redigate/internal/config"
)

func (c *Client) detectTopology(ctx context.Context) (Topology, error) {
	switch c.cfg.Mode {
	case config.RedisModeStandalone:
		return TopologyStandalone, nil
	case config.RedisModeCluster:
		return TopologyCluster, nil
	case config.RedisModeSentinel:
		return TopologySentinel, nil
	}

	if c.cfg.SentinelMaster != "" || c.cfg.SentinelUsername != "" || c.cfg.SentinelPassword != "" {
		return TopologySentinel, nil
	}

	var errs []error

	for _, addr := range c.cfg.Addrs {
		topology, err := c.probe(ctx, addr)
		if err == nil {
			return topology, nil
		}

		errs = append(errs, fmt.Errorf("%s: %w", addr, err))
	}

	return "", errors.Join(errs...)
}

func (c *Client) probe(ctx context.Context, addr string) (Topology, error) {
	opt := c.nodeOptions(addr, 0)
	opt.DialerRetries = 1

	info, err := probeInfo(ctx, opt)
	if err != nil && isAuthError(err) && (opt.Username != "" || opt.Password != "") {
		// Sentinels often run without authentication while data nodes require it.
		opt.Username, opt.Password = "", ""
		info, err = probeInfo(ctx, opt)
	}

	if err != nil {
		return "", err
	}

	switch {
	case info["redis_mode"] == "sentinel" || info["server_mode"] == "sentinel":
		return TopologySentinel, nil
	case info["cluster_enabled"] == "1":
		return TopologyCluster, nil
	default:
		return TopologyStandalone, nil
	}
}

func probeInfo(ctx context.Context, opt *redis.Options) (map[string]string, error) {
	client := redis.NewClient(opt)
	defer client.Close()

	raw, err := client.Info(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("INFO: %w", err)
	}

	return ParseInfo(raw), nil
}

func isAuthError(err error) bool {
	switch ReplyErrorPrefix(err) {
	case "NOAUTH", "WRONGPASS":
		return true
	}

	return strings.Contains(err.Error(), "without any password configured")
}

// ParseInfo parses the INFO reply into a flat key-value map, section headers are skipped.
func ParseInfo(raw string) map[string]string {
	info := make(map[string]string)

	for line := range strings.Lines(raw) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if key, value, ok := strings.Cut(line, ":"); ok {
			info[key] = value
		}
	}

	return info
}

func (c *Client) resolveSentinelMaster(ctx context.Context) (string, error) {
	if c.cfg.SentinelMaster != "" {
		return c.cfg.SentinelMaster, nil
	}

	reply, err := c.sentinelDo(ctx, "sentinel", "masters")
	if err != nil {
		return "", err
	}

	entries, _ := reply.([]any)
	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		if name := replyToStringMap(entry)["name"]; name != "" {
			names = append(names, name)
		}
	}

	if len(names) != 1 {
		return "", fmt.Errorf("%w: sentinel monitors %d masters %v, set REDIS_SENTINEL_MASTER",
			ErrMasterUnknown, len(names), names)
	}

	return names[0], nil
}

// sentinelDo runs a command on the first reachable sentinel.
func (c *Client) sentinelDo(ctx context.Context, args ...any) (any, error) {
	errs := []error{ErrNoSentinel}

	for _, addr := range c.cfg.Addrs {
		reply, err := c.directClient(addr, RoleSentinel).Do(ctx, args...).Result()
		if err == nil {
			return reply, nil
		}

		if _, ok := ReplyError(err); ok {
			return nil, err
		}

		errs = append(errs, fmt.Errorf("%s: %w", addr, err))
	}

	return nil, errors.Join(errs...)
}

// replyToStringMap converts a RESP2 flat key-value array or a RESP3 map to map[string]string.
func replyToStringMap(reply any) map[string]string {
	result := make(map[string]string)

	switch v := reply.(type) {
	case []any:
		for i := 0; i+1 < len(v); i += 2 {
			result[fmt.Sprint(v[i])] = fmt.Sprint(v[i+1])
		}
	case map[any]any:
		for key, value := range v {
			result[fmt.Sprint(key)] = fmt.Sprint(value)
		}
	case map[string]any:
		for key, value := range v {
			result[key] = fmt.Sprint(value)
		}
	}

	return result
}
