//go:build integration

// Package integration runs the HTTP API against real deployments started by compose.yaml.
// Run it with `make test-integration`.
package integration

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/httpapi"
	"github.com/btrvodka/redigate/internal/redisx"
	"github.com/btrvodka/redigate/internal/service"
)

const (
	fullToken     = "full"
	readOnlyToken = "ro"
	jsonType      = "application/json"
)

type noopMetrics struct{}

func (noopMetrics) ObserveHTTPRequest(string, int, time.Duration) {}

func (noopMetrics) ObserveRedisCommand(string, string, time.Duration) {}

type envelope struct {
	Code        string          `json:"error_code"`
	Description string          `json:"error_description"`
	Result      json.RawMessage `json:"result"`
}

// env is redigate served in-process against a real deployment.
type env struct {
	srv *httptest.Server
}

func newEnv(t *testing.T, variable string) *env {
	t.Helper()

	url := os.Getenv(variable)
	if url == "" {
		t.Skipf("%s is not set, run the tests with `make test-integration`", variable)
	}

	cfg := &config.Config{
		Auth: config.Auth{Token: fullToken, ReadOnlyToken: readOnlyToken},
		Limits: config.Limits{
			MaxBodyBytes:        1 << 20,
			MaxImportBytes:      1 << 30,
			MaxResponseItems:    10000,
			MaxPipelineCommands: 10000,
			RequestTimeout:      10 * time.Second,
			MaxBlockTimeout:     30 * time.Second,
			MaxStreamDuration:   time.Minute,
			MaxStreams:          10,
		},
		Redis: config.Redis{
			Mode:         config.RedisModeAuto,
			Protocol:     3,
			DialTimeout:  5 * time.Second,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			MaxRetries:   3,
			MaxRedirects: 8,
		},
	}

	if err := config.ParseRedisURL(url, &cfg.Redis); err != nil {
		t.Fatal(err)
	}

	if os.Getenv("REDIGATE_TEST_PROTOCOL") == "2" {
		cfg.Redis.Protocol = 2
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	client, err := redisx.New(t.Context(), cfg.Redis, logger, noopMetrics{})
	if err != nil {
		t.Fatalf("connect to %s: %v", url, err)
	}

	t.Cleanup(func() { _ = client.Close() })

	srv := httptest.NewServer(httpapi.New(cfg, service.New(client, cfg.Limits), logger, noopMetrics{}, "test"))
	t.Cleanup(srv.Close)

	return &env{srv: srv}
}

func (e *env) do(t *testing.T, method, path, token, body string) (int, envelope) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		req.Header.Set("Content-Type", jsonType)
	}

	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	var out envelope
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
	}

	return resp.StatusCode, out
}

// get performs a request with the full token, requires 200 and decodes the result.
func get[T any](t *testing.T, e *env, method, path, body string) T {
	t.Helper()

	status, out := e.do(t, method, path, fullToken, body)
	if status != http.StatusOK {
		t.Fatalf("%s %s %s: %d %s: %s", method, path, body, status, out.Code, out.Description)
	}

	var result T
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("%s %s: decode %s: %v", method, path, out.Result, err)
	}

	return result
}

// value runs a command-like request and returns the value of the result.
func value(t *testing.T, e *env, method, path, body string) any {
	t.Helper()

	return get[service.CommandResult](t, e, method, path, body).Value
}

// expectStatus checks the status and the error code of a request.
func expectStatus(t *testing.T, e *env, token, method, path, body string, status int, code string) {
	t.Helper()

	got, out := e.do(t, method, path, token, body)
	if got != status || (code != "" && out.Code != code) {
		t.Errorf("%s %s %s: got %d %s (%s), want %d %s", method, path, body, got, out.Code, out.Description, status, code)
	}
}

// eventually retries check until it returns true or the timeout expires.
func eventually(t *testing.T, timeout time.Duration, what string, check func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}

		time.Sleep(200 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", what)
}

func nodesByRole(topology service.TopologyInfo, role string) []string {
	var addrs []string

	for _, node := range topology.Nodes {
		if node.Role == role {
			addrs = append(addrs, node.Addr)
		}
	}

	return addrs
}
