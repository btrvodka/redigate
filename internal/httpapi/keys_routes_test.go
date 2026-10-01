package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/service"
)

// result decodes the "result" of a successful response into T.
func result[T any](t *testing.T, srv *httptest.Server, method, path, body string) T {
	t.Helper()

	status, env := doBody(t, srv, method, path, "", jsonType, body)
	if status != http.StatusOK {
		t.Fatalf("%s %s: %d %s %s", method, path, status, env.Code, env.Description)
	}

	var out T
	if err := json.Unmarshal(env.Result, &out); err != nil {
		t.Fatalf("%s %s: decode %s: %v", method, path, env.Result, err)
	}

	return out
}

func value(t *testing.T, srv *httptest.Server, method, path, body string) any {
	t.Helper()

	return result[service.CommandResult](t, srv, method, path, body).Value
}

func TestScanKeys(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{})

	for i := range 250 {
		_ = mr.Set("user:"+strconv.Itoa(i), "v")
	}

	mr.HSet("other", "f", "v")

	seen := make(map[string]bool)
	cursor := ""

	for range 10 {
		page := result[service.ScanResult](t, srv, http.MethodGet, "/api/v1/keys?match=user:*&count=100&cursor="+cursor, "")
		for _, key := range page.Keys {
			seen[key.(string)] = true
		}

		if cursor = page.Cursor; cursor == "0" {
			break
		}
	}

	if len(seen) != 250 || cursor != "0" {
		t.Errorf("scanned %d keys, cursor %q", len(seen), cursor)
	}

	page := result[service.ScanResult](t, srv, http.MethodGet, "/api/v1/keys?type=hash&meta", "")
	want := []any{map[string]any{"key": "other", "type": "hash", "ttl_ms": float64(-1)}}

	if !reflect.DeepEqual(page.Keys, want) {
		t.Errorf("hash keys with meta = %#v", page.Keys)
	}
}

func TestKeyValue(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{})
	_ = mr.Set("str", "hello")
	mr.HSet("hash", "f1", "v1", "f2", "v2")
	_, _ = mr.Push("list", "a", "b")
	_, _ = mr.SetAdd("set", "x")
	_, _ = mr.ZAdd("zset", 1.5, "m")
	_ = mr.Set("\xff\x00", "binary key")
	mr.SetTTL("str", 0)

	tests := []struct {
		path string
		typ  string
		want any
	}{
		{"key=str", "string", "hello"},
		{"key=hash", "hash", map[string]any{"f1": "v1", "f2": "v2"}},
		{"key=list", "list", []any{"a", "b"}},
		{"key=set", "set", []any{"x"}},
		{"key=zset", "zset", []any{map[string]any{"member": "m", "score": 1.5}}},
		{"key=%2FwA%3D&key_encoding=base64", "string", "binary key"},
	}

	for _, tt := range tests {
		got := result[map[string]any](t, srv, http.MethodGet, "/api/v1/keys/value?"+tt.path, "")
		if got["type"] != tt.typ || !reflect.DeepEqual(got["value"], tt.want) {
			t.Errorf("%s: type %v, value %#v", tt.path, got["type"], got["value"])
		}
	}

	if status, _ := do(t, srv, http.MethodGet, "/api/v1/keys/value?key=missing", ""); status != http.StatusNotFound {
		t.Errorf("missing key: status %d", status)
	}
}

func TestKeyOperations(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{})

	for i := range 30 {
		_ = mr.Set(fmt.Sprintf("tmp:%d", i), "v")
	}

	_ = mr.Set("a", "1")
	_ = mr.Set("b", "2")

	exists := result[service.MultiKeyResult](t, srv, http.MethodGet, "/api/v1/keys/exists?key=a&key=b&key=c", "")
	if exists.Count != 2 {
		t.Errorf("exists count = %d", exists.Count)
	}

	dry := result[service.DeleteByPatternResult](t, srv, http.MethodPost, "/api/v1/keys/delete", `{"match": "tmp:*", "dry_run": true}`)
	if dry.Matched != 30 || dry.Deleted != 0 || len(mr.Keys()) != 32 {
		t.Errorf("dry run = %+v", dry)
	}

	deleted := result[service.DeleteByPatternResult](t, srv, http.MethodPost, "/api/v1/keys/delete", `{"match": "tmp:*"}`)
	if deleted.Deleted != 30 || deleted.Cursor != "0" || len(mr.Keys()) != 2 {
		t.Errorf("delete = %+v, keys %v", deleted, mr.Keys())
	}

	value(t, srv, http.MethodPut, "/api/v1/keys/ttl", `{"key": "a", "ttl_ms": 60000}`)

	if ttl, ok := value(t, srv, http.MethodGet, "/api/v1/keys/ttl?key=a", "").(float64); !ok || ttl <= 0 || ttl > 60000 {
		t.Errorf("ttl = %v", ttl)
	}

	value(t, srv, http.MethodPost, "/api/v1/keys/rename", `{"key": "a", "new_key": "c"}`)

	if !mr.Exists("c") || mr.Exists("a") {
		t.Error("rename failed")
	}

	del := result[service.MultiKeyResult](t, srv, http.MethodDelete, "/api/v1/keys?key=b&key=c&unlink", "")
	if del.Count != 2 || len(mr.Keys()) != 0 {
		t.Errorf("delete = %+v", del)
	}
}

func TestDataTypes(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{})

	steps := []struct {
		method, path, body string
		want               any
	}{
		{http.MethodPut, "/api/v1/strings", `{"key": "s", "value": "1"}`, "OK"},
		{http.MethodPost, "/api/v1/strings/incr", `{"key": "s", "by": 5}`, float64(6)},
		{http.MethodPost, "/api/v1/strings/incr", `{"key": "s", "by": 0.5}`, "6.5"},
		{http.MethodGet, "/api/v1/strings?key=s", "", "6.5"},

		{http.MethodPut, "/api/v1/hashes", `{"key": "h", "fields": [{"field": "a", "value": "1"}, {"field": "b", "value": "2"}]}`, float64(2)},
		{http.MethodPost, "/api/v1/hashes/incr", `{"key": "h", "field": "a", "by": 10}`, float64(11)},
		{http.MethodGet, "/api/v1/hashes?key=h", "", map[string]any{"a": "11", "b": "2"}},
		{http.MethodGet, "/api/v1/hashes/fields?key=h&field=b&field=x", "", []any{"2", nil}},
		{http.MethodDelete, "/api/v1/hashes/fields?key=h&field=b", "", float64(1)},

		{http.MethodPost, "/api/v1/lists/push", `{"key": "l", "values": ["a", "b", "c"], "side": "right"}`, float64(3)},
		{http.MethodPost, "/api/v1/lists/pop", `{"key": "l"}`, "a"},
		{http.MethodGet, "/api/v1/lists?key=l", "", []any{"b", "c"}},

		{http.MethodPost, "/api/v1/sets/add", `{"key": "{s}1", "members": ["a", "b"]}`, float64(2)},
		{http.MethodPost, "/api/v1/sets/add", `{"key": "{s}2", "members": ["b", "c"]}`, float64(2)},
		{http.MethodPost, "/api/v1/sets/inter", `{"keys": ["{s}1", "{s}2"]}`, []any{"b"}},

		{http.MethodPost, "/api/v1/zsets/add", `{"key": "z", "members": [{"member": "a", "score": 1}, {"member": "b", "score": 2}]}`, float64(2)},
		{http.MethodPost, "/api/v1/zsets/incr", `{"key": "z", "member": "a", "by": 5}`, float64(6)}, // RESP3 double
		{http.MethodGet, "/api/v1/zsets?key=z", "", []any{map[string]any{"member": "b", "score": float64(2)}, map[string]any{"member": "a", "score": float64(6)}}},

		{http.MethodPost, "/api/v1/hll/add", `{"key": "hll", "elements": ["a", "b", "a"]}`, float64(1)},
		{http.MethodGet, "/api/v1/hll/count?key=hll", "", float64(2)},
	}

	for _, step := range steps {
		if got := value(t, srv, step.method, step.path, step.body); !reflect.DeepEqual(got, step.want) {
			t.Errorf("%s %s %s = %#v, want %#v", step.method, step.path, step.body, got, step.want)
		}
	}

	if got, _ := mr.Get("s"); got != "6.5" {
		t.Errorf("s = %q", got)
	}
}

func TestStreams(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, config.Auth{})

	id := value(t, srv, http.MethodPost, "/api/v1/streams/add", `{"key": "st", "id": "1-0", "fields": [{"field": "f", "value": "v"}]}`)
	if id != "1-0" {
		t.Fatalf("xadd id = %v", id)
	}

	entries := value(t, srv, http.MethodGet, "/api/v1/streams?key=st", "")
	want := []any{map[string]any{"id": "1-0", "fields": map[string]any{"f": "v"}}}

	if !reflect.DeepEqual(entries, want) {
		t.Errorf("xrange = %#v", entries)
	}

	value(t, srv, http.MethodPost, "/api/v1/streams/groups", `{"key": "st", "group": "g", "id": "0"}`)

	read := value(t, srv, http.MethodPost, "/api/v1/streams/read-group", `{"keys": ["st"], "group": "g", "consumer": "c"}`)
	wantRead := []any{map[string]any{"stream": "st", "entries": want}}

	if !reflect.DeepEqual(read, wantRead) {
		t.Errorf("xreadgroup = %#v", read)
	}

	if acked := value(t, srv, http.MethodPost, "/api/v1/streams/ack", `{"key": "st", "group": "g", "ids": ["1-0"]}`); acked != float64(1) {
		t.Errorf("xack = %v", acked)
	}
}

func TestTypedRoutesRespectReadOnly(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{Token: "full", ReadOnlyToken: "ro"})
	_ = mr.Set("k", "v")

	tests := []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/api/v1/strings?key=k", "", http.StatusOK},
		{http.MethodGet, "/api/v1/keys/value?key=k", "", http.StatusOK},
		{http.MethodPut, "/api/v1/strings", `{"key": "k", "value": "x"}`, http.StatusForbidden},
		{http.MethodDelete, "/api/v1/keys?key=k", "", http.StatusForbidden},
		{http.MethodPost, "/api/v1/keys/delete", `{"match": "*"}`, http.StatusForbidden},
		{http.MethodPost, "/api/v1/streams/read-group", `{"keys": ["s"], "group": "g", "consumer": "c"}`, http.StatusForbidden},
	}

	for _, tt := range tests {
		if status, env := doBody(t, srv, tt.method, tt.path, "ro", jsonType, tt.body); status != tt.status {
			t.Errorf("%s %s: status %d (%s), want %d", tt.method, tt.path, status, env.Description, tt.status)
		}
	}

	if got, _ := mr.Get("k"); got != "v" {
		t.Errorf("value changed to %q", got)
	}
}

func TestTypedRouteValidation(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, config.Auth{})

	for _, tt := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/strings", ""},
		{http.MethodPut, "/api/v1/strings", `{"value": "x"}`},
		{http.MethodPut, "/api/v1/strings", ``},
		{http.MethodPost, "/api/v1/lists/push", `{"key": "l", "values": ["a"], "side": "up"}`},
		{http.MethodPut, "/api/v1/keys/ttl", `{"key": "k"}`},
		{http.MethodGet, "/api/v1/keys/value?key=!!&key_encoding=base64", ""},
		{http.MethodGet, "/api/v1/keys?cursor=garbage!", ""},
	} {
		if status, env := doBody(t, srv, tt.method, tt.path, "", jsonType, tt.body); status != http.StatusBadRequest {
			t.Errorf("%s %s %s: status %d %s", tt.method, tt.path, tt.body, status, env.Description)
		}
	}
}
