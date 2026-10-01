package httpapi_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/service"
)

func exportLines(t *testing.T, url string) []map[string]any {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/x-ndjson" {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("export: %d %s", resp.StatusCode, body)
	}

	var lines []map[string]any

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("decode %s: %v", scanner.Bytes(), err)
		}

		lines = append(lines, line)
	}

	return lines
}

func fillTypes(t *testing.T, mr *miniredis.Miniredis) {
	t.Helper()

	_ = mr.Set("str", "value")
	_ = mr.Set("\xff\x00", "binary key")
	_, _ = mr.Push("list", "a", "b", "c")
	_, _ = mr.SetAdd("set", "x", "y")
	mr.HSet("hash", "f1", "v1", "f2", "\xff")
	_, _ = mr.ZAdd("zset", 1.5, "m1")
	_, _ = mr.ZAdd("zset", 2, "m2")
	_, _ = mr.XAdd("stream", "1-1", []string{"f", "v"})
	mr.SetTTL("str", time.Hour)
}

func TestExportImportValueFormat(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{})
	fillTypes(t, mr)

	lines := exportLines(t, srv.URL+"/api/v1/keys/export?format=value")

	summary := lines[len(lines)-1]["summary"].(map[string]any)
	if summary["keys"] != float64(7) || summary["complete"] != true {
		t.Fatalf("summary = %v", summary)
	}

	var body strings.Builder
	for _, line := range lines {
		raw, _ := json.Marshal(line)
		body.Write(raw)
		body.WriteString("\n")
	}

	// Import into another database and compare with the source.
	res := result[service.ImportResult](t, srv, http.MethodPost, "/api/v1/keys/import?db=5", body.String())
	if res.Imported != 7 || res.Failed != 0 {
		t.Fatalf("import = %+v", res)
	}

	src, dst := mr.DB(0), mr.DB(5)

	for _, key := range src.Keys() {
		if srcType, dstType := src.Type(key), dst.Type(key); srcType != dstType {
			t.Errorf("type of %q: %s != %s", key, srcType, dstType)
		}
	}

	if got, _ := dst.Get("\xff\x00"); got != "binary key" {
		t.Errorf("binary key = %q", got)
	}

	if got, _ := dst.List("list"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("list = %v", got)
	}

	if got := dst.HGet("hash", "f2"); got != "\xff" {
		t.Errorf("binary hash value = %q", got)
	}

	if got, _ := dst.ZScore("zset", "m1"); got != 1.5 {
		t.Errorf("zset score = %v", got)
	}

	if ttl := dst.TTL("str"); ttl <= 0 || ttl > time.Hour {
		t.Errorf("ttl = %v", ttl)
	}

	// Existing keys are skipped without replace and overwritten with it.
	again := result[service.ImportResult](t, srv, http.MethodPost, "/api/v1/keys/import?db=5", body.String())
	if again.Skipped != 7 || again.Imported != 0 {
		t.Errorf("second import = %+v", again)
	}

	_ = dst.Set("str", "changed")

	replaced := result[service.ImportResult](t, srv, http.MethodPost, "/api/v1/keys/import?db=5&replace", body.String())
	if got, _ := dst.Get("str"); replaced.Imported != 7 || got != "value" {
		t.Errorf("replace import = %+v, str = %q", replaced, got)
	}
}

func TestImportErrors(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{Token: "full", ReadOnlyToken: "ro"})

	body := strings.Join([]string{
		`{"key": "ok", "type": "string", "ttl_ms": -1, "value": "v"}`,
		`not json`,
		`{"type": "string", "value": "no key"}`,
		`{"key": "unknown", "type": "vectorset", "value": []}`,
		`{"key": "skipped", "type": "TSDB-TYPE", "error": "not supported"}`,
		``,
		`{"summary": {"keys": 5, "complete": true}}`,
	}, "\n")

	status, env := doBody(t, srv, http.MethodPost, "/api/v1/keys/import", "full", "", body)
	if status != http.StatusOK {
		t.Fatalf("import: %d %+v", status, env)
	}

	var res service.ImportResult
	_ = json.Unmarshal(env.Result, &res)

	if res.Imported != 1 || res.Failed != 3 || res.Skipped != 1 || len(res.Errors) != 3 || res.Errors[0].Line != 2 {
		t.Errorf("import = %+v", res)
	}

	if !mr.Exists("ok") {
		t.Error("valid line was not imported")
	}

	if status, _ := doBody(t, srv, http.MethodPost, "/api/v1/keys/import", "ro", "", body); status != http.StatusForbidden {
		t.Errorf("read-only import: %d", status)
	}
}

func TestImportBodyLimit(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServerWith(t, config.Auth{}, func(cfg *config.Config) { cfg.Limits.MaxImportBytes = 64 })

	body := strings.Repeat(`{"key": "k", "type": "string", "value": "v"}`+"\n", 10)
	if status, env := doBody(t, srv, http.MethodPost, "/api/v1/keys/import", "", "", body); status != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d %+v", status, env)
	}
}

func TestScripts(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, config.Auth{})

	load := result[service.FanOutResult](t, srv, http.MethodPost, "/api/v1/scripts/load",
		`{"script": "return redis.call('SET', KEYS[1], ARGV[1])"}`)

	sha, _ := load.Nodes[0].Value.(string)
	if !load.OK || len(sha) != 40 {
		t.Fatalf("load = %+v", load)
	}

	exists := result[service.ScriptExistsResult](t, srv, http.MethodGet, "/api/v1/scripts/exists?sha="+sha+"&sha=0000", "")
	if !exists.Everywhere[sha] || exists.Everywhere["0000"] {
		t.Errorf("exists = %+v", exists)
	}

	if got := value(t, srv, http.MethodPost, "/api/v1/scripts/eval", `{"sha": "`+sha+`", "keys": ["k"], "args": ["v"]}`); got != "OK" {
		t.Errorf("evalsha = %v", got)
	}

	if got := value(t, srv, http.MethodPost, "/api/v1/scripts/eval", `{"script": "return KEYS[1] .. ARGV[1]", "keys": ["a"], "args": ["b"]}`); got != "ab" {
		t.Errorf("eval = %v", got)
	}

	if status, _ := doBody(t, srv, http.MethodPost, "/api/v1/scripts/eval", "", jsonType, `{"script": "x", "sha": "y"}`); status != http.StatusBadRequest {
		t.Errorf("script and sha: %d", status)
	}
}
