package webui_test

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/httpapi"
	"github.com/btrvodka/redigate/internal/redisx"
	"github.com/btrvodka/redigate/internal/service"
	"github.com/btrvodka/redigate/internal/webui"
)

type noopMetrics struct{}

func (noopMetrics) ObserveHTTPRequest(string, int, time.Duration) {}

func (noopMetrics) ObserveRedisCommand(string, string, time.Duration) {}

func newServer(t *testing.T, auth config.Auth) (*httptest.Server, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)
	logger := slog.New(slog.DiscardHandler)

	cfg := &config.Config{
		Auth: auth,
		Limits: config.Limits{
			MaxBodyBytes:        1 << 20,
			MaxResponseItems:    1000,
			MaxPipelineCommands: 1000,
			MaxStreams:          4,
			RequestTimeout:      5 * time.Second,
		},
		Redis: config.Redis{Mode: config.RedisModeAuto, Addrs: []string{mr.Addr()}, Protocol: 3, DialTimeout: time.Second},
	}

	client, err := redisx.New(t.Context(), cfg.Redis, logger, noopMetrics{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	svc := service.New(client, cfg.Limits)

	ui, err := webui.New(cfg, svc, logger, "test")
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(httpapi.New(cfg, svc, logger, noopMetrics{}, "test", httpapi.WithUI(ui)))
	t.Cleanup(srv.Close)

	return srv, mr
}

type request struct {
	method string
	path   string
	form   url.Values
	htmx   bool
	cookie string
}

type response struct {
	StatusCode int
	Header     http.Header
	cookies    []*http.Cookie
}

func (r response) Cookies() []*http.Cookie {
	return r.cookies
}

func send(t *testing.T, srv *httptest.Server, req request) (response, string) {
	t.Helper()

	var body io.Reader
	if req.form != nil {
		body = strings.NewReader(req.form.Encode())
	}

	r, err := http.NewRequestWithContext(t.Context(), cmpOr(req.method, http.MethodGet), srv.URL+req.path, body)
	if err != nil {
		t.Fatal(err)
	}

	if req.form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	if req.htmx {
		r.Header.Set("Hx-Request", "true")
	}

	if req.cookie != "" {
		r.AddCookie(&http.Cookie{Name: "redigate_token", Value: req.cookie})
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return response{StatusCode: resp.StatusCode, Header: resp.Header, cookies: resp.Cookies()}, string(raw)
}

func cmpOr(a, b string) string {
	if a == "" {
		return b
	}

	return a
}

func keyParam(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

func TestPages(t *testing.T) {
	t.Parallel()

	srv, mr := newServer(t, config.Auth{})
	_ = mr.Set("a", "1")

	if resp, _ := send(t, srv, request{path: "/"}); resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ui/" {
		t.Errorf("/ redirects to %q with %d", resp.Header.Get("Location"), resp.StatusCode)
	}

	for path, want := range map[string]string{
		"/ui/":                   mr.Addr(),
		"/ui/keys":               `hx-get="/ui/keys/rows"`,
		"/ui/console":            `hx-post="/ui/console"`,
		"/ui/static/htmx.min.js": "htmx",
	} {
		resp, body := send(t, srv, request{path: path})
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("%s: %d, %q not found", path, resp.StatusCode, want)
		}
	}

	if resp, _ := send(t, srv, request{path: "/ui/"}); resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("the UI must not be framed")
	}

	// The HTTP API is untouched.
	if resp, _ := send(t, srv, request{path: "/api/v1/ping"}); resp.StatusCode != http.StatusOK {
		t.Errorf("API ping: %d", resp.StatusCode)
	}
}

func TestKeyPages(t *testing.T) {
	t.Parallel()

	srv, mr := newServer(t, config.Auth{})

	for i := range 150 {
		_ = mr.Set("k:"+strings.Repeat("x", i%3)+string(rune('a'+i%26))+strings.Repeat("0", i/26), "v")
	}

	ids := regexp.MustCompile(`<tr id="(key-[0-9a-f]+)"`)
	next := regexp.MustCompile(`hx-get="(/ui/keys/rows\?[^"]+)" hx-trigger="revealed"`)

	seen := make(map[string]bool)
	path := "/ui/keys/rows?match=k:*&type="

	for range 10 {
		resp, body := send(t, srv, request{path: path, htmx: true})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, body)
		}

		for _, m := range ids.FindAllStringSubmatch(body, -1) {
			seen[m[1]] = true
		}

		m := next.FindStringSubmatch(body)
		if m == nil {
			break
		}

		path = strings.ReplaceAll(m[1], "&amp;", "&")
	}

	if len(seen) != 150 {
		t.Errorf("rows = %d, want 150", len(seen))
	}

	if _, body := send(t, srv, request{path: "/ui/keys/rows?match=nothing*", htmx: true}); !strings.Contains(body, "No keys found") {
		t.Errorf("empty result: %s", body)
	}
}

func TestKeyView(t *testing.T) {
	t.Parallel()

	srv, mr := newServer(t, config.Auth{})
	mr.HSet("h", "field", "value")
	_ = mr.Set("\xff\x00", "binary")
	_ = mr.Set("<script>alert(1)</script>", "xss")

	_, body := send(t, srv, request{path: "/ui/key?k=" + keyParam("h"), htmx: true})
	if !strings.Contains(body, "<td>field</td>") || !strings.Contains(body, "value") || !strings.Contains(body, `hx-delete=`) {
		t.Errorf("hash view: %s", body)
	}

	_, body = send(t, srv, request{path: "/ui/key?k=" + keyParam("\xff\x00"), htmx: true})
	if !strings.Contains(body, "/wA=") || !strings.Contains(body, "binary") {
		t.Errorf("binary key view: %s", body)
	}

	_, body = send(t, srv, request{path: "/ui/keys/rows?match=*", htmx: true})
	if strings.Contains(body, "<script>alert") || !strings.Contains(body, "&lt;script&gt;") {
		t.Error("key names must be escaped")
	}

	if _, body := send(t, srv, request{path: "/ui/key?k=" + keyParam("missing"), htmx: true}); !strings.Contains(body, "does not exist") {
		t.Errorf("missing key: %s", body)
	}
}

func TestKeyActions(t *testing.T) {
	t.Parallel()

	srv, mr := newServer(t, config.Auth{})
	_ = mr.Set("k", "v")

	// Changes must come from htmx.
	if resp, _ := send(t, srv, request{method: http.MethodDelete, path: "/ui/key?k=" + keyParam("k")}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("delete without HX-Request: %d", resp.StatusCode)
	}

	resp, body := send(t, srv, request{method: http.MethodPut, path: "/ui/key/ttl?k=" + keyParam("k"), htmx: true, form: url.Values{"ttl": {"1h"}}})
	if resp.StatusCode != http.StatusOK || mr.TTL("k") != time.Hour || !strings.Contains(body, "TTL updated") {
		t.Errorf("set ttl: %d, ttl %v", resp.StatusCode, mr.TTL("k"))
	}

	send(t, srv, request{method: http.MethodPut, path: "/ui/key/ttl?k=" + keyParam("k"), htmx: true, form: url.Values{"ttl": {""}}})

	if mr.TTL("k") != 0 {
		t.Errorf("persist: ttl %v", mr.TTL("k"))
	}

	resp, body = send(t, srv, request{method: http.MethodPut, path: "/ui/key/ttl?k=" + keyParam("k"), htmx: true, form: url.Values{"ttl": {"soon"}}})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "class=\"error\"") {
		t.Errorf("invalid ttl: %d %s", resp.StatusCode, body)
	}

	resp, body = send(t, srv, request{method: http.MethodDelete, path: "/ui/key?k=" + keyParam("k"), htmx: true})
	if resp.StatusCode != http.StatusOK || mr.Exists("k") || !strings.Contains(body, "<hx-partial") {
		t.Errorf("delete: %d %s", resp.StatusCode, body)
	}
}

func TestConsole(t *testing.T) {
	t.Parallel()

	srv, mr := newServer(t, config.Auth{})

	_, body := send(t, srv, request{method: http.MethodPost, path: "/ui/console", htmx: true, form: url.Values{"command": {`SET "my key" "a value"`}, "target": {"auto"}}})
	if got, _ := mr.Get("my key"); got != "a value" || !strings.Contains(body, "<pre>OK</pre>") {
		t.Errorf("SET: stored %q, body %s", got, body)
	}

	_, body = send(t, srv, request{method: http.MethodPost, path: "/ui/console", htmx: true, form: url.Values{"command": {"PING"}, "target": {"masters"}}})
	if !strings.Contains(body, "PONG") || !strings.Contains(body, mr.Addr()) {
		t.Errorf("fan-out: %s", body)
	}

	_, body = send(t, srv, request{method: http.MethodPost, path: "/ui/console", htmx: true, form: url.Values{"command": {`LPUSH "my key" x`}}})
	if !strings.Contains(body, `class="error"`) {
		t.Errorf("an error reply must be shown: %s", body)
	}
}

func TestAuth(t *testing.T) {
	t.Parallel()

	srv, mr := newServer(t, config.Auth{Token: "full", ReadOnlyToken: "ro"})
	_ = mr.Set("k", "v")

	if resp, _ := send(t, srv, request{path: "/ui/keys"}); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/ui/login" {
		t.Errorf("unauthenticated page: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	if resp, _ := send(t, srv, request{path: "/ui/keys/rows", htmx: true}); resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("Hx-Redirect") != "/ui/login" {
		t.Errorf("unauthenticated htmx request: %d", resp.StatusCode)
	}

	if resp, _ := send(t, srv, request{method: http.MethodPost, path: "/ui/login", form: url.Values{"token": {"nope"}}}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", resp.StatusCode)
	}

	resp, _ := send(t, srv, request{method: http.MethodPost, path: "/ui/login", form: url.Values{"token": {"ro"}}})

	var cookie *http.Cookie

	for _, c := range resp.Cookies() {
		if c.Name == "redigate_token" {
			cookie = c
		}
	}

	if resp.StatusCode != http.StatusSeeOther || cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("login: %d, cookie %+v", resp.StatusCode, cookie)
	}

	// Read-only access: no actions, writes are denied.
	_, body := send(t, srv, request{path: "/ui/key?k=" + keyParam("k"), htmx: true, cookie: cookie.Value})
	if strings.Contains(body, "hx-delete") {
		t.Error("read-only view must not offer actions")
	}

	resp, body = send(t, srv, request{method: http.MethodDelete, path: "/ui/key?k=" + keyParam("k"), htmx: true, cookie: cookie.Value})
	if resp.StatusCode != http.StatusForbidden || !mr.Exists("k") {
		t.Errorf("read-only delete: %d %s", resp.StatusCode, body)
	}

	_, body = send(t, srv, request{method: http.MethodPost, path: "/ui/console", htmx: true, cookie: cookie.Value, form: url.Values{"command": {"FLUSHALL"}}})
	if !strings.Contains(body, "read-only") || len(mr.Keys()) != 1 {
		t.Errorf("read-only console: %s", body)
	}

	if _, body := send(t, srv, request{path: "/ui/", cookie: cookie.Value}); !strings.Contains(body, "badge warn") {
		t.Error("read-only badge is missing")
	}
}
