package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ParseRedisURL applies a connection URL on top of cfg.
//
// Supported forms:
//
//	redis://[user[:password]@]host:port[,host:port...][/db][?options]
//	rediss://...                                   — the same with TLS
//	redis+sentinel://[user[:password]@]host:port[,host:port...]/master[/db][?options]
//	rediss+sentinel://...                          — the same with TLS
//
// Options: mode, addr (repeatable), db, master, sentinel_username, sentinel_password,
// protocol, dial_timeout, read_timeout, write_timeout, pool_size, max_retries,
// max_redirects, read_from_replicas, tls_server_name, tls_insecure_skip_verify,
// tls_ca_file, tls_cert_file, tls_key_file.
func ParseRedisURL(raw string, cfg *Redis) error {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return fmt.Errorf("missing scheme in %q", raw)
	}

	if err := applyScheme(scheme, cfg); err != nil {
		return err
	}

	rest, rawQuery, _ := strings.Cut(rest, "?")
	authority, path, _ := strings.Cut(rest, "/")

	if at := strings.LastIndex(authority, "@"); at >= 0 {
		if err := parseUserInfo(authority[:at], cfg); err != nil {
			return err
		}

		authority = authority[at+1:]
	}

	var addrs []string

	for host := range strings.SplitSeq(authority, ",") {
		if host = strings.TrimSpace(host); host != "" {
			addrs = append(addrs, host)
		}
	}

	if err := parsePath(path, cfg); err != nil {
		return err
	}

	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return fmt.Errorf("parse query: %w", err)
	}

	addrs = append(addrs, query["addr"]...)
	delete(query, "addr")

	if len(addrs) > 0 {
		cfg.Addrs = addrs
	}

	for key, values := range query {
		if err := applyOption(cfg, key, values[len(values)-1]); err != nil {
			return fmt.Errorf("option %q: %w", key, err)
		}
	}

	return nil
}

func applyScheme(scheme string, cfg *Redis) error {
	switch strings.ToLower(scheme) {
	case "redis":
	case "rediss":
		cfg.TLS.Enabled = true
	case "redis+sentinel":
		cfg.Mode = RedisModeSentinel
	case "rediss+sentinel":
		cfg.Mode = RedisModeSentinel
		cfg.TLS.Enabled = true
	default:
		return fmt.Errorf("unsupported scheme %q", scheme)
	}

	return nil
}

func parseUserInfo(userInfo string, cfg *Redis) error {
	user, pass, hasPass := strings.Cut(userInfo, ":")

	user, err := url.PathUnescape(user)
	if err != nil {
		return fmt.Errorf("unescape username: %w", err)
	}

	cfg.Username = user

	if hasPass {
		if cfg.Password, err = url.PathUnescape(pass); err != nil {
			return fmt.Errorf("unescape password: %w", err)
		}
	}

	return nil
}

func parsePath(path string, cfg *Redis) error {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' })

	if cfg.Mode == RedisModeSentinel && len(parts) > 0 {
		master, err := url.PathUnescape(parts[0])
		if err != nil {
			return fmt.Errorf("unescape master name: %w", err)
		}

		cfg.SentinelMaster = master
		parts = parts[1:]
	}

	switch len(parts) {
	case 0:
		return nil
	case 1:
		db, err := strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("invalid db %q", parts[0])
		}

		cfg.DB = db

		return nil
	default:
		return fmt.Errorf("unexpected path %q", path)
	}
}

//nolint:cyclop // flat list of options
func applyOption(cfg *Redis, key, value string) error {
	var err error

	switch key {
	case "mode":
		cfg.Mode = value
	case "db":
		cfg.DB, err = strconv.Atoi(value)
	case "master":
		cfg.SentinelMaster = value
	case "sentinel_username":
		cfg.SentinelUsername = value
	case "sentinel_password":
		cfg.SentinelPassword = value
	case "protocol":
		cfg.Protocol, err = strconv.Atoi(value)
	case "dial_timeout":
		cfg.DialTimeout, err = time.ParseDuration(value)
	case "read_timeout":
		cfg.ReadTimeout, err = time.ParseDuration(value)
	case "write_timeout":
		cfg.WriteTimeout, err = time.ParseDuration(value)
	case "pool_size":
		cfg.PoolSize, err = strconv.Atoi(value)
	case "max_retries":
		cfg.MaxRetries, err = strconv.Atoi(value)
	case "max_redirects":
		cfg.MaxRedirects, err = strconv.Atoi(value)
	case "read_from_replicas":
		cfg.ReadFromReplicas, err = strconv.ParseBool(value)
	case "tls_server_name":
		cfg.TLS.ServerName = value
	case "tls_insecure_skip_verify":
		cfg.TLS.InsecureSkipVerify, err = strconv.ParseBool(value)
	case "tls_ca_file":
		cfg.TLS.CAFile = value
	case "tls_cert_file":
		cfg.TLS.CertFile = value
	case "tls_key_file":
		cfg.TLS.KeyFile = value
	default:
		return errors.New("unknown option")
	}

	return err
}
