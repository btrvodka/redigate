// Package config loads redigate configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	HTTP    HTTP
	UI      UI
	Auth    Auth
	Metrics Metrics
	Log     Log
	Limits  Limits
	Redis   Redis
}

type HTTP struct {
	Addr              string        `envconfig:"HTTP_ADDR"                default:"127.0.0.1:8080"`
	ReadHeaderTimeout time.Duration `envconfig:"HTTP_READ_HEADER_TIMEOUT" default:"10s"`
	IdleTimeout       time.Duration `envconfig:"HTTP_IDLE_TIMEOUT"        default:"120s"`
	ShutdownTimeout   time.Duration `envconfig:"HTTP_SHUTDOWN_TIMEOUT"    default:"15s"`
}

type UI struct {
	// Enabled serves the web UI at /ui/ and Swagger UI at /api/v1/docs/ on the API address.
	Enabled bool `envconfig:"UI_ENABLED" default:"true"`
}

type Auth struct {
	// Token grants full access. Empty Token and ReadOnlyToken disable authentication.
	Token string `envconfig:"API_TOKEN"`
	// ReadOnlyToken grants access to commands without write/admin/dangerous flags.
	ReadOnlyToken string `envconfig:"API_READONLY_TOKEN"`
	// ReadOnly forces read-only access for every request regardless of the token.
	ReadOnly bool `envconfig:"READ_ONLY" default:"false"`
	// AllowInsecure allows to listen on a non-loopback address without authentication.
	AllowInsecure bool `envconfig:"ALLOW_INSECURE" default:"false"`
}

func (a Auth) Enabled() bool {
	return a.Token != "" || a.ReadOnlyToken != ""
}

type Metrics struct {
	// Addr of the metrics server. Empty value disables it.
	Addr  string `envconfig:"METRICS_ADDR"  default:":9090"`
	Pprof bool   `envconfig:"PPROF_ENABLED" default:"false"`
}

type Log struct {
	Level  string `envconfig:"LOG_LEVEL"  default:"info"`
	Format string `envconfig:"LOG_FORMAT" default:"text"`
}

type Limits struct {
	MaxBodyBytes        int64         `envconfig:"MAX_BODY_BYTES"      default:"33554432"`
	MaxImportBytes      int64         `envconfig:"MAX_IMPORT_BYTES" default:"1073741824"`
	MaxResponseItems    int           `envconfig:"MAX_RESPONSE_ITEMS"  default:"10000"`
	MaxPipelineCommands int           `envconfig:"MAX_PIPELINE_COMMANDS" default:"10000"`
	MaxStreamDuration   time.Duration `envconfig:"MAX_STREAM_DURATION" default:"1h"`
	MaxStreams          int           `envconfig:"MAX_STREAMS" default:"64"`
	MaxBlockTimeout     time.Duration `envconfig:"MAX_BLOCK_TIMEOUT"   default:"60s"`
	RequestTimeout      time.Duration `envconfig:"REQUEST_TIMEOUT"     default:"30s"`
}

const (
	RedisModeAuto       = "auto"
	RedisModeStandalone = "standalone"
	RedisModeCluster    = "cluster"
	RedisModeSentinel   = "sentinel"
)

type Redis struct {
	// URL overrides connection settings below, see ParseRedisURL.
	URL string `envconfig:"REDIS_URL"`

	Mode     string   `envconfig:"REDIS_MODE"     default:"auto"`
	Addrs    []string `envconfig:"REDIS_ADDRS"    default:"127.0.0.1:6379"`
	Username string   `envconfig:"REDIS_USERNAME"`
	Password string   `envconfig:"REDIS_PASSWORD"`
	DB       int      `envconfig:"REDIS_DB"       default:"0"`

	SentinelMaster   string `envconfig:"REDIS_SENTINEL_MASTER"`
	SentinelUsername string `envconfig:"REDIS_SENTINEL_USERNAME"`
	SentinelPassword string `envconfig:"REDIS_SENTINEL_PASSWORD"`

	// ReadFromReplicas routes read-only commands to replicas in cluster and sentinel modes.
	ReadFromReplicas bool `envconfig:"REDIS_READ_FROM_REPLICAS" default:"false"`

	TLS TLS

	Protocol     int           `envconfig:"REDIS_PROTOCOL"      default:"3"`
	DialTimeout  time.Duration `envconfig:"REDIS_DIAL_TIMEOUT"  default:"5s"`
	ReadTimeout  time.Duration `envconfig:"REDIS_READ_TIMEOUT"  default:"10s"`
	WriteTimeout time.Duration `envconfig:"REDIS_WRITE_TIMEOUT" default:"10s"`
	PoolSize     int           `envconfig:"REDIS_POOL_SIZE"     default:"0"`
	MaxRetries   int           `envconfig:"REDIS_MAX_RETRIES"   default:"3"`
	MaxRedirects int           `envconfig:"REDIS_MAX_REDIRECTS" default:"8"`
}

type TLS struct {
	Enabled            bool   `envconfig:"REDIS_TLS_ENABLED"              default:"false"`
	CAFile             string `envconfig:"REDIS_TLS_CA_FILE"`
	CertFile           string `envconfig:"REDIS_TLS_CERT_FILE"`
	KeyFile            string `envconfig:"REDIS_TLS_KEY_FILE"`
	ServerName         string `envconfig:"REDIS_TLS_SERVER_NAME"`
	InsecureSkipVerify bool   `envconfig:"REDIS_TLS_INSECURE_SKIP_VERIFY" default:"false"`
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := envconfig.Process("", cfg); err != nil {
		return nil, fmt.Errorf("process env: %w", err)
	}

	if cfg.Redis.URL != "" {
		if err := ParseRedisURL(cfg.Redis.URL, &cfg.Redis); err != nil {
			return nil, fmt.Errorf("parse REDIS_URL: %w", err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	errs := append(c.Redis.validate(), c.Limits.validate()...)

	if c.Auth.Token != "" && c.Auth.Token == c.Auth.ReadOnlyToken {
		errs = append(errs, errors.New("API_TOKEN and API_READONLY_TOKEN must differ"))
	}

	if !c.Auth.Enabled() && !c.Auth.AllowInsecure && !IsLoopback(c.HTTP.Addr) {
		errs = append(errs, fmt.Errorf(
			"refusing to listen on %q without authentication: set API_TOKEN, "+
				"listen on a loopback address or set ALLOW_INSECURE=true", c.HTTP.Addr))
	}

	return errors.Join(errs...)
}

func (r Redis) validate() []error {
	var errs []error

	switch r.Mode {
	case RedisModeAuto, RedisModeStandalone, RedisModeCluster, RedisModeSentinel:
	default:
		errs = append(errs, fmt.Errorf("unknown redis mode %q", r.Mode))
	}

	if len(r.Addrs) == 0 {
		errs = append(errs, errors.New("redis addresses are not set"))
	}

	if r.Mode == RedisModeCluster && r.DB != 0 {
		errs = append(errs, errors.New("redis cluster supports only db 0"))
	}

	if r.Protocol != 2 && r.Protocol != 3 {
		errs = append(errs, fmt.Errorf("unsupported redis protocol %d", r.Protocol))
	}

	return errs
}

func (l Limits) validate() []error {
	var errs []error

	for name, value := range map[string]int{
		"MAX_RESPONSE_ITEMS":    l.MaxResponseItems,
		"MAX_PIPELINE_COMMANDS": l.MaxPipelineCommands,
		"MAX_STREAMS":           l.MaxStreams,
	} {
		if value <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive", name))
		}
	}

	return errs
}

// IsLoopback reports whether addr is bound to a loopback interface only.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}

	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}
