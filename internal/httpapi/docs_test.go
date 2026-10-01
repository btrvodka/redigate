package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/httpapi"
	"github.com/btrvodka/redigate/internal/redisx"
	"github.com/btrvodka/redigate/internal/service"
)

// TestReadmeDocumentsAllRoutes keeps the endpoint reference of README.md in sync with the code.
func TestReadmeDocumentsAllRoutes(t *testing.T) {
	t.Parallel()

	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}

	documented := make(map[string]bool)
	for _, match := range regexp.MustCompile("`((?:GET|POST|PUT|DELETE) /api/v1/[^`?]*)`").FindAllStringSubmatch(string(readme), -1) {
		documented[match[1]] = true
	}

	cfg := &config.Config{Limits: config.Limits{MaxStreams: 1}}
	routes := httpapi.New(cfg, nil, slog.New(slog.DiscardHandler), noopMetrics{}, "test").Routes()
	slices.Sort(routes)

	for _, route := range routes {
		if !documented[route] {
			t.Errorf("route is not documented in README.md: %s", route)
		}

		delete(documented, route)
	}

	for route := range documented {
		t.Errorf("README.md documents an unknown route: %s", route)
	}
}

// TestOpenAPIDocumentsAllRoutes keeps the generated specification in sync with the
// routes: run `make swagger` after changing route annotations.
func TestOpenAPIDocumentsAllRoutes(t *testing.T) {
	t.Parallel()

	var spec struct {
		BasePath string                    `json:"basePath"`
		Paths    map[string]map[string]any `json:"paths"`
	}

	if err := json.Unmarshal(httpapi.OpenAPI(), &spec); err != nil {
		t.Fatal(err)
	}

	documented := make(map[string]bool)

	for path, operations := range spec.Paths {
		for method := range operations {
			documented[strings.ToUpper(method)+" "+spec.BasePath+path] = true
		}
	}

	cfg := &config.Config{Limits: config.Limits{MaxStreams: 1}}
	for _, route := range httpapi.New(cfg, nil, slog.New(slog.DiscardHandler), noopMetrics{}, "test").Routes() {
		if !documented[route] {
			t.Errorf("route has no OpenAPI annotation: %s", route)
		}

		delete(documented, route)
	}

	for route := range documented {
		t.Errorf("OpenAPI documents an unknown route: %s", route)
	}
}

func TestOpenAPIIsPublic(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, config.Auth{Token: "secret"})

	for path, contentType := range map[string]string{
		"/api/v1/openapi.json": "application/json",
		"/api/v1/openapi.yaml": "application/yaml",
	} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != contentType || !strings.Contains(string(body), "redigate API") {
			t.Errorf("%s: %d %s", path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}

func TestSwaggerUI(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	cfg := &config.Config{
		Auth:   config.Auth{Token: "secret"},
		Limits: config.Limits{MaxStreams: 1, RequestTimeout: time.Second},
		Redis:  config.Redis{Mode: config.RedisModeAuto, Addrs: []string{mr.Addr()}, Protocol: 3, DialTimeout: time.Second},
	}

	client, err := redisx.New(t.Context(), cfg.Redis, slog.New(slog.DiscardHandler), noopMetrics{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	svc := service.New(client, cfg.Limits)
	srv := httptest.NewServer(httpapi.New(cfg, svc, slog.New(slog.DiscardHandler), noopMetrics{}, "test", httpapi.WithSwaggerUI()))
	t.Cleanup(srv.Close)

	get := func(path string) (int, string, string) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, http.NoBody)

		resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		return resp.StatusCode, resp.Header.Get("Location"), string(body)
	}

	// The documentation is public, like the specification.
	if status, location, _ := get("/api/v1/docs"); status != http.StatusFound || location != "/api/v1/docs/" {
		t.Errorf("/api/v1/docs: %d -> %s", status, location)
	}

	if status, _, body := get("/api/v1/docs/"); status != http.StatusOK || !strings.Contains(body, "openapi.json") || !strings.Contains(body, `"none"`) {
		t.Errorf("index: %d", status)
	}

	if status, _, body := get("/api/v1/docs/swagger-ui-bundle.js"); status != http.StatusOK || len(body) < 100_000 {
		t.Errorf("bundle: %d, %d bytes", status, len(body))
	}

	// Without the option there is no Swagger UI.
	plain := httptest.NewServer(httpapi.New(cfg, svc, slog.New(slog.DiscardHandler), noopMetrics{}, "test"))
	t.Cleanup(plain.Close)

	resp, err := plain.Client().Get(plain.URL + "/api/v1/docs/") //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("docs without the option: %d", resp.StatusCode)
	}
}
