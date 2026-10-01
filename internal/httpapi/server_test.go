package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/httpapi"
	"github.com/btrvodka/redigate/internal/redisx"
	"github.com/btrvodka/redigate/internal/service"
)

type noopMetrics struct{}

func (noopMetrics) ObserveHTTPRequest(string, int, time.Duration) {}

func (noopMetrics) ObserveRedisCommand(string, string, time.Duration) {}

type envelope struct {
	Code        string          `json:"error_code"`
	Description string          `json:"error_description"`
	RequestID   string          `json:"request_id"`
	Result      json.RawMessage `json:"result"`
}

func newTestServer(t *testing.T, auth config.Auth) (*httptest.Server, *miniredis.Miniredis) {
	t.Helper()

	return newTestServerWith(t, auth, func(*config.Config) {})
}

func newTestServerWith(t *testing.T, auth config.Auth, adjust func(*config.Config)) (*httptest.Server, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)
	logger := slog.New(slog.DiscardHandler)

	cfg := &config.Config{
		Auth: auth,
		Limits: config.Limits{
			MaxBodyBytes:        1 << 20,
			MaxImportBytes:      1 << 30,
			MaxResponseItems:    100,
			MaxPipelineCommands: 100,
			RequestTimeout:      5 * time.Second,
			MaxBlockTimeout:     10 * time.Second,
			MaxStreamDuration:   time.Minute,
			MaxStreams:          10,
		},
		Redis: config.Redis{
			Mode:        config.RedisModeAuto,
			Addrs:       []string{mr.Addr()},
			Protocol:    3,
			DialTimeout: time.Second,
		},
	}

	adjust(cfg)

	client, err := redisx.New(t.Context(), cfg.Redis, logger, noopMetrics{})
	if err != nil {
		t.Fatalf("redisx.New() error = %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	api := httpapi.New(cfg, service.New(client, cfg.Limits), logger, noopMetrics{}, "test")
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)

	return srv, mr
}

func do(t *testing.T, srv *httptest.Server, method, path, token string) (int, envelope) {
	t.Helper()

	return doBody(t, srv, method, path, token, "", "")
}

func doBody(t *testing.T, srv *httptest.Server, method, path, token, contentType, reqBody string) (int, envelope) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	if resp.Header.Get("X-Request-ID") == "" {
		t.Error("X-Request-Id header is missing")
	}

	return resp.StatusCode, env
}

func TestPingAndTopology(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{})

	status, env := do(t, srv, http.MethodGet, "/api/v1/ping", "")
	if status != http.StatusOK || env.Code != httpapi.CodeOK {
		t.Fatalf("ping: status %d, body %+v", status, env)
	}

	var ping service.PingResult
	if err := json.Unmarshal(env.Result, &ping); err != nil {
		t.Fatal(err)
	}

	if !ping.OK || len(ping.Nodes) != 1 || ping.Nodes[0].Addr != mr.Addr() {
		t.Errorf("unexpected ping result %+v", ping)
	}

	status, env = do(t, srv, http.MethodGet, "/api/v1/topology", "")
	if status != http.StatusOK {
		t.Fatalf("topology: status %d, body %+v", status, env)
	}

	var topology service.TopologyInfo
	if err := json.Unmarshal(env.Result, &topology); err != nil {
		t.Fatal(err)
	}

	if topology.Topology != string(redisx.TopologyStandalone) {
		t.Errorf("topology = %q, want standalone", topology.Topology)
	}
}

func TestAuth(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, config.Auth{Token: "full", ReadOnlyToken: "ro"})

	tests := []struct {
		name   string
		token  string
		status int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"wrong token", "nope", http.StatusUnauthorized},
		{"full token", "full", http.StatusOK},
		{"read-only token", "ro", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, env := do(t, srv, http.MethodGet, "/api/v1/ping", tt.token)
			if status != tt.status {
				t.Errorf("status = %d, want %d (%+v)", status, tt.status, env)
			}

			if status == http.StatusUnauthorized && env.Code != httpapi.CodeUnauthorized {
				t.Errorf("error_code = %q, want %q", env.Code, httpapi.CodeUnauthorized)
			}
		})
	}

	// Health checks are always public.
	if status, _ := do(t, srv, http.MethodGet, "/healthz", ""); status != http.StatusOK {
		t.Errorf("healthz status = %d", status)
	}
}

func TestReadyAndNotFound(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{})

	if status, _ := do(t, srv, http.MethodGet, "/readyz", ""); status != http.StatusOK {
		t.Errorf("readyz status = %d, want 200", status)
	}

	status, env := do(t, srv, http.MethodGet, "/api/v1/nope", "")
	if status != http.StatusNotFound || env.Code != httpapi.CodeRouteNotFound {
		t.Errorf("unknown route: status %d, body %+v", status, env)
	}

	mr.Close()

	status, env = do(t, srv, http.MethodGet, "/readyz", "")
	if status != http.StatusServiceUnavailable || env.Code != httpapi.CodeRedisUnavailable {
		t.Errorf("readyz with redis down: status %d, body %+v", status, env)
	}
}
