package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/httpapi"
	"github.com/btrvodka/redigate/internal/service"
)

func TestServerRoutes(t *testing.T) {
	t.Parallel()

	srv, mr := newTestServer(t, config.Auth{Token: "full", ReadOnlyToken: "ro"})
	_ = mr.Set("a", "1")
	_ = mr.Set("b", "2")

	status, env := do(t, srv, http.MethodGet, "/api/v1/server/dbsize", "ro")
	if status != http.StatusOK {
		t.Fatalf("dbsize: %d %+v", status, env)
	}

	var size service.DBSizeResult
	if err := json.Unmarshal(env.Result, &size); err != nil {
		t.Fatal(err)
	}

	if size.Total != 2 || len(size.Nodes) != 1 {
		t.Errorf("dbsize = %+v", size)
	}

	if status, env := do(t, srv, http.MethodGet, "/api/v1/server/info", "ro"); status != http.StatusOK {
		t.Errorf("info: %d %+v", status, env)
	}

	status, env = do(t, srv, http.MethodGet, "/api/v1/server/commands?prefix=hge", "ro")

	var commands []service.CommandInfo
	if err := json.Unmarshal(env.Result, &commands); err != nil || status != http.StatusOK {
		t.Fatalf("commands: %d %v", status, err)
	}

	if len(commands) != 2 || commands[0].Name != "hget" || commands[1].Name != "hgetall" {
		t.Errorf("commands = %+v", commands)
	}

	// Write operations require full access.
	if status, _ := do(t, srv, http.MethodPost, "/api/v1/server/flushall", "ro"); status != http.StatusForbidden {
		t.Errorf("flushall with read-only token: %d", status)
	}

	status, env = do(t, srv, http.MethodPost, "/api/v1/server/flushall", "full")

	var flush service.FanOutResult
	if err := json.Unmarshal(env.Result, &flush); err != nil || status != http.StatusOK || !flush.OK {
		t.Errorf("flushall: %d %+v %+v", status, env, flush)
	}

	if len(mr.Keys()) != 0 {
		t.Errorf("keys after flushall: %v", mr.Keys())
	}

	// Shutdown never picks nodes implicitly.
	status, env = doBody(t, srv, http.MethodPost, "/api/v1/server/shutdown", "full", "", `{"save": "nosave"}`)
	if status != http.StatusBadRequest || env.Code != httpapi.CodeBadRequest {
		t.Errorf("shutdown without node: %d %+v", status, env)
	}

	status, env = doBody(t, srv, http.MethodPost, "/api/v1/server/replicaof?node="+mr.Addr(), "full", "", `{}`)
	if status != http.StatusBadRequest {
		t.Errorf("replicaof without host: %d %+v", status, env)
	}
}

func TestClusterRoutesRequireCluster(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, config.Auth{})

	for _, path := range []string{"/api/v1/cluster/info", "/api/v1/cluster/nodes", "/api/v1/cluster/keyslot?key=a"} {
		status, env := do(t, srv, http.MethodGet, path, "")
		if status != http.StatusNotImplemented || env.Code != httpapi.CodeNotSupported {
			t.Errorf("%s: %d %+v", path, status, env)
		}
	}
}
