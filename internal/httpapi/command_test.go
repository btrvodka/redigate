package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/httpapi"
	"github.com/btrvodka/redigate/internal/service"
)

const jsonType = "application/json"

func command(t *testing.T, srv *httptest.Server, path, token, body string) (int, envelope, service.CommandResult) {
	t.Helper()

	status, env := doBody(t, srv, http.MethodPost, path, token, "", body)

	var result service.CommandResult
	if status == http.StatusOK {
		if err := json.Unmarshal(env.Result, &result); err != nil {
			t.Fatalf("decode %s: %v", env.Result, err)
		}
	}

	return status, env, result
}

func TestCommandFormats(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{})

	// JSON args with a binary argument.
	status, env, _ := command(t, srv, "/api/v1/command", "", `{"args": ["SET", "bin", {"base64": "/wA="}]}`)
	if status != http.StatusOK {
		t.Fatalf("SET: %d %+v", status, env)
	}

	if got, _ := mr.Get("bin"); got != "\xff\x00" {
		t.Errorf("stored %q", got)
	}

	// Binary values are returned as base64 objects in the auto encoding.
	_, _, res := command(t, srv, "/api/v1/command", "", `{"command": "GET bin"}`)
	if !reflect.DeepEqual(res.Value, map[string]any{"base64": "/wA="}) {
		t.Errorf("GET bin = %#v", res.Value)
	}

	// redis-cli style body with quotes.
	command(t, srv, "/api/v1/command", "", `SET "a key" "a value"`)

	_, _, res = command(t, srv, "/api/v1/command?encoding=base64", "", `GET "a key"`)
	if res.Value != "YSB2YWx1ZQ==" {
		t.Errorf("GET in base64 = %#v", res.Value)
	}

	// A nil reply is a successful null.
	status, _, res = command(t, srv, "/api/v1/command", "", "GET missing")
	if status != http.StatusOK || res.Value != nil {
		t.Errorf("GET missing: %d %#v", status, res.Value)
	}
}

func TestCommandErrors(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{})
	_ = mr.Set("str", "v")

	tests := []struct {
		name   string
		path   string
		body   string
		status int
		code   string
	}{
		{"wrong type", "/api/v1/command", "LPUSH str x", http.StatusBadRequest, httpapi.CodeWrongType},
		{"empty", "/api/v1/command", "  ", http.StatusBadRequest, httpapi.CodeBadRequest},
		{"bad quotes", "/api/v1/command", `GET "x`, http.StatusBadRequest, httpapi.CodeBadRequest},
		{"bad json", "/api/v1/command", `{"args": [true]}`, http.StatusBadRequest, httpapi.CodeBadRequest},
		{"unknown field", "/api/v1/command", `{"argz": []}`, http.StatusBadRequest, httpapi.CodeBadRequest},
		{"subscribe", "/api/v1/command", "SUBSCRIBE ch", http.StatusBadRequest, httpapi.CodeUnsupportedCmd},
		{"unknown target", "/api/v1/command?target=moon", "PING", http.StatusBadRequest, httpapi.CodeBadRequest},
		{"unknown node", "/api/v1/command?node=1.2.3.4:5", "PING", http.StatusNotFound, httpapi.CodeNotFound},
		{"bad timeout", "/api/v1/command?timeout=forever", "PING", http.StatusBadRequest, httpapi.CodeBadRequest},
		{"too long timeout", "/api/v1/command?timeout=1h", "PING", http.StatusBadRequest, httpapi.CodeBadRequest},
		{"session cmd in fan-out", "/api/v1/command?target=all", "SELECT 1", http.StatusBadRequest, httpapi.CodeBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, env := doBody(t, srv, http.MethodPost, tt.path, "", "", tt.body)
			if status != tt.status || env.Code != tt.code {
				t.Errorf("got %d %s (%s), want %d %s", status, env.Code, env.Description, tt.status, tt.code)
			}
		})
	}
}

func TestCommandSessionAndDB(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{})
	_ = mr.Set("k", "db0")
	_ = mr.DB(1).Set("k", "db1")

	// SELECT runs on a dedicated connection and must not affect pooled ones.
	if status, env, _ := command(t, srv, "/api/v1/command", "", "SELECT 1"); status != http.StatusOK {
		t.Fatalf("SELECT: %d %+v", status, env)
	}

	for range 5 {
		if _, _, res := command(t, srv, "/api/v1/command", "", "GET k"); res.Value != "db0" {
			t.Fatalf("GET k after SELECT = %#v, pool is corrupted", res.Value)
		}
	}

	if _, _, res := command(t, srv, "/api/v1/command?db=1", "", "GET k"); res.Value != "db1" {
		t.Errorf("GET k in db 1 = %#v", res.Value)
	}

	// db on a node goes through a dedicated connection as well.
	if _, _, res := command(t, srv, "/api/v1/command?db=1&node="+mr.Addr(), "", "GET k"); res.Value != "db1" || res.Node != mr.Addr() {
		t.Errorf("GET k on node in db 1 = %+v", res)
	}
}

func TestCommandFanOut(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{})

	status, env := doBody(t, srv, http.MethodPost, "/api/v1/command?target=masters", "", "", "PING")
	if status != http.StatusOK {
		t.Fatalf("fan-out: %d %+v", status, env)
	}

	var res service.FanOutResult
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatal(err)
	}

	want := []service.NodeResult{{Addr: mr.Addr(), Role: "master", Value: "PONG"}}
	if !res.OK || !reflect.DeepEqual(res.Nodes, want) {
		t.Errorf("nodes = %+v, want %+v", res.Nodes, want)
	}
}

func TestReadOnlyAccess(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{Token: "full", ReadOnlyToken: "ro"})
	_ = mr.Set("k", "v")

	tests := []struct {
		token  string
		body   string
		status int
	}{
		{"ro", "GET k", http.StatusOK},
		{"ro", "INFO", http.StatusOK},
		{"ro", "SET k v2", http.StatusForbidden},
		{"ro", "FLUSHALL", http.StatusForbidden},
		{"ro", "EVAL \"return 1\" 0", http.StatusForbidden},
		{"ro", "PUBLISH ch msg", http.StatusForbidden},
		{"ro", "SHUTDOWN", http.StatusForbidden},
		{"ro", "NOSUCHCOMMAND", http.StatusForbidden},
		{"full", "SET k v2", http.StatusOK},
	}

	for _, tt := range tests {
		status, env := doBody(t, srv, http.MethodPost, "/api/v1/command", tt.token, "", tt.body)
		if status != tt.status {
			t.Errorf("%s with %s token: status %d (%s), want %d", tt.body, tt.token, status, env.Description, tt.status)
		}
	}

	// Every command of a pipeline is checked.
	status, _ := doBody(t, srv, http.MethodPost, "/api/v1/pipeline", "ro", "", "GET k\nDEL k")
	if status != http.StatusForbidden {
		t.Errorf("read-only pipeline with DEL: status %d", status)
	}
}

func pipeline(t *testing.T, srv *httptest.Server, path, contentType, body string) (int, envelope, service.PipelineResult) {
	t.Helper()

	status, env := doBody(t, srv, http.MethodPost, path, "", contentType, body)

	var result service.PipelineResult
	if status == http.StatusOK {
		if err := json.Unmarshal(env.Result, &result); err != nil {
			t.Fatalf("decode %s: %v", env.Result, err)
		}
	}

	return status, env, result
}

func values(res service.PipelineResult) []any {
	out := make([]any, 0, len(res.Results))
	for _, r := range res.Results {
		if r.Error != "" {
			out = append(out, "ERR:"+r.Error)
		} else {
			out = append(out, r.Value)
		}
	}

	return out
}

func TestPipeline(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{})

	status, env, res := pipeline(t, srv, "/api/v1/pipeline", jsonType,
		`{"commands": [["SET", "n", "1"], "INCR n", ["GET", "n"], "LPUSH n x"]}`)
	if status != http.StatusOK {
		t.Fatalf("pipeline: %d %+v", status, env)
	}

	got := values(res)
	if len(got) != 4 || got[0] != "OK" || got[1] != float64(2) || got[2] != "2" {
		t.Errorf("pipeline results = %#v", got)
	}

	if s, _ := got[3].(string); len(s) < 4 || s[:4] != "ERR:" {
		t.Errorf("LPUSH on a string must fail, got %#v", got[3])
	}

	// Text body, atomic.
	status, env, res = pipeline(t, srv, "/api/v1/pipeline?atomic", "",
		"# comment\nSET a 1\n\nINCRBY a 10\n")
	if status != http.StatusOK || !reflect.DeepEqual(values(res), []any{"OK", float64(11)}) {
		t.Errorf("atomic pipeline: %d %+v %#v", status, env, values(res))
	}

	// Connection-scoped commands require a session.
	status, env, _ = pipeline(t, srv, "/api/v1/pipeline", "", "SELECT 2\nSET s 1")
	if status != http.StatusBadRequest || env.Code != httpapi.CodeBadRequest {
		t.Errorf("SELECT without session: %d %+v", status, env)
	}

	status, env, res = pipeline(t, srv, "/api/v1/pipeline?session", "", "SELECT 2\nSET s 1\nGET s")
	if status != http.StatusOK || !reflect.DeepEqual(values(res), []any{"OK", "OK", "1"}) {
		t.Errorf("session pipeline: %d %+v %#v", status, env, values(res))
	}

	if got, _ := mr.DB(2).Get("s"); got != "1" {
		t.Errorf("value in db 2 = %q", got)
	}

	if mr.Exists("s") {
		t.Error("session must not write to db 0")
	}

	// MULTI/EXEC by hand inside a session.
	_, _, res = pipeline(t, srv, "/api/v1/pipeline", jsonType,
		`{"session": true, "commands": ["MULTI", "INCR m", "INCR m", "EXEC"]}`)
	if got := values(res); !reflect.DeepEqual(got, []any{"OK", "QUEUED", "QUEUED", []any{float64(1), float64(2)}}) {
		t.Errorf("manual transaction = %#v", got)
	}

	if status, _, _ := pipeline(t, srv, "/api/v1/pipeline?target=all", "", "PING"); status != http.StatusBadRequest {
		t.Errorf("fan-out pipeline: status %d", status)
	}
}
