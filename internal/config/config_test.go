package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseRedisURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want Redis
	}{
		{
			name: "standalone with db",
			url:  "redis://user:p%40ss@localhost:6380/2",
			want: Redis{Mode: RedisModeAuto, Addrs: []string{"localhost:6380"}, Username: "user", Password: "p@ss", DB: 2},
		},
		{
			name: "password only",
			url:  "redis://:secret@localhost:6379",
			want: Redis{Mode: RedisModeAuto, Addrs: []string{"localhost:6379"}, Password: "secret"},
		},
		{
			name: "cluster with several hosts and options",
			url:  "rediss://n1:7000,n2:7001?addr=n3:7002&mode=cluster&max_redirects=3&read_timeout=2s&read_from_replicas=true",
			want: Redis{
				Mode:             RedisModeCluster,
				Addrs:            []string{"n1:7000", "n2:7001", "n3:7002"},
				MaxRedirects:     3,
				ReadTimeout:      2 * time.Second,
				ReadFromReplicas: true,
				TLS:              TLS{Enabled: true},
			},
		},
		{
			name: "sentinel with master and db",
			url:  "redis+sentinel://s1:26379,s2:26379/mymaster/1?sentinel_password=sp",
			want: Redis{
				Mode:             RedisModeSentinel,
				Addrs:            []string{"s1:26379", "s2:26379"},
				SentinelMaster:   "mymaster",
				SentinelPassword: "sp",
				DB:               1,
			},
		},
		{
			name: "ipv6",
			url:  "redis://[::1]:6379",
			want: Redis{Mode: RedisModeAuto, Addrs: []string{"[::1]:6379"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Redis{Mode: RedisModeAuto}
			if err := ParseRedisURL(tt.url, &got); err != nil {
				t.Fatalf("ParseRedisURL() error = %v", err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseRedisURL()\n got = %+v\nwant = %+v", got, tt.want)
			}
		})
	}
}

func TestParseRedisURLErrors(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"localhost:6379",
		"http://localhost",
		"redis://localhost/abc",
		"redis://localhost/1/2",
		"redis://localhost?unknown=1",
		"redis://localhost?db=x",
	} {
		if err := ParseRedisURL(raw, &Redis{}); err == nil {
			t.Errorf("ParseRedisURL(%q) expected error", raw)
		}
	}
}

func TestIsLoopback(t *testing.T) {
	t.Parallel()

	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true,
		"localhost:8080": true,
		"[::1]:8080":     true,
		":8080":          false,
		"0.0.0.0:8080":   false,
		"10.0.0.1:8080":  false,
		"garbage":        false,
	} {
		if got := IsLoopback(addr); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestValidateRequiresAuthOnPublicAddr(t *testing.T) {
	t.Parallel()

	cfg := Config{
		HTTP:   HTTP{Addr: ":8080"},
		Limits: Limits{MaxResponseItems: 1, MaxPipelineCommands: 1, MaxStreams: 1},
		Redis:  Redis{Mode: RedisModeAuto, Addrs: []string{"localhost:6379"}, Protocol: 3},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "without authentication") {
		t.Fatalf("Validate() error = %v, want authentication error", err)
	}

	cfg.Auth.Token = "secret"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with token error = %v", err)
	}

	cfg.Auth = Auth{AllowInsecure: true}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with ALLOW_INSECURE error = %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://example:6390/3")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTP.Addr != "127.0.0.1:8080" || cfg.Redis.Protocol != 3 || cfg.Redis.DB != 3 ||
		!reflect.DeepEqual(cfg.Redis.Addrs, []string{"example:6390"}) {
		t.Errorf("unexpected config %+v", cfg)
	}
}
