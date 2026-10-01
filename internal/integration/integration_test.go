//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/btrvodka/redigate/internal/service"
)

type expectation struct {
	topology  string
	masters   int
	replicas  int
	sentinels int
}

func TestStandalone(t *testing.T) {
	e := newEnv(t, "REDIGATE_TEST_STANDALONE")
	topology := common(t, e, expectation{topology: "standalone", masters: 1, replicas: 1})

	t.Run("databases", func(t *testing.T) {
		value(t, e, http.MethodPost, "/api/v1/command?db=3", "SET db:key three")

		if got := value(t, e, http.MethodPost, "/api/v1/command?db=3", "GET db:key"); got != "three" {
			t.Errorf("GET in db 3 = %v", got)
		}

		if got := value(t, e, http.MethodPost, "/api/v1/command", "GET db:key"); got != nil {
			t.Errorf("GET in db 0 = %v", got)
		}

		// SELECT runs on a dedicated connection and leaves the pool in db 0.
		value(t, e, http.MethodPost, "/api/v1/command", "SELECT 3")

		if got := value(t, e, http.MethodPost, "/api/v1/command", "GET db:key"); got != nil {
			t.Errorf("GET in db 0 after SELECT = %v", got)
		}

		expectStatus(t, e, fullToken, http.MethodPost, "/api/v1/command?db=100000", "PING", http.StatusBadRequest, "BAD_REQUEST")
	})

	t.Run("replica reads", func(t *testing.T) {
		value(t, e, http.MethodPost, "/api/v1/command", "SET replicated yes")

		replica := nodesByRole(topology, "replica")[0]
		eventually(t, 5*time.Second, "replication", func() bool {
			return value(t, e, http.MethodPost, "/api/v1/command?node="+replica, "GET replicated") == "yes"
		})
	})
}

func TestCluster(t *testing.T) {
	e := newEnv(t, "REDIGATE_TEST_CLUSTER")
	common(t, e, expectation{topology: "cluster", masters: 3, replicas: 3})

	t.Run("pipelines and slots", func(t *testing.T) {
		// A plain pipeline is split by slot.
		res := get[service.PipelineResult](t, e, http.MethodPost, "/api/v1/pipeline", "SET a 1\nSET b 2\nSET c 3\nMGET {a}x {a}y")
		for i, entry := range res.Results {
			if entry.Error != "" {
				t.Errorf("command %d failed: %s", i, entry.Error)
			}
		}

		// Atomic pipelines and sessions require a single slot.
		expectStatus(t, e, fullToken, http.MethodPost, "/api/v1/pipeline?atomic", "SET a 1\nSET b 2", http.StatusBadRequest, "CROSS_SLOT")

		tx := get[service.PipelineResult](t, e, http.MethodPost, "/api/v1/pipeline?atomic", "SET {t}a 1\nINCR {t}a")
		if tx.Node == "" || tx.Results[1].Value != float64(2) {
			t.Errorf("atomic pipeline = %+v", tx)
		}

		watch := get[service.PipelineResult](t, e, http.MethodPost, "/api/v1/pipeline?session",
			"WATCH {w}a\nMULTI\nSET {w}a 1\nINCR {w}a\nEXEC")
		if got := watch.Results[4].Value; !reflect.DeepEqual(got, []any{"OK", float64(2)}) {
			t.Errorf("WATCH/MULTI/EXEC = %#v", got)
		}

		// Movable keys are extracted with COMMAND GETKEYS.
		value(t, e, http.MethodPost, "/api/v1/pipeline?atomic", `EVAL "return redis.call('SET', KEYS[1], ARGV[1])" 1 {t}lua v`)
		expectStatus(t, e, fullToken, http.MethodPost, "/api/v1/pipeline?atomic",
			`EVAL "return 1" 2 x y`, http.StatusBadRequest, "CROSS_SLOT")

		expectStatus(t, e, fullToken, http.MethodPost, "/api/v1/command?db=1", "PING", http.StatusNotImplemented, "NOT_SUPPORTED")
	})

	t.Run("cluster info", func(t *testing.T) {
		info := value(t, e, http.MethodGet, "/api/v1/cluster/info", "").(map[string]any)
		if info["cluster_state"] != "ok" || info["cluster_known_nodes"] != "6" {
			t.Errorf("cluster info = %v", info)
		}

		nodes := value(t, e, http.MethodGet, "/api/v1/cluster/nodes", "").([]any)
		if len(nodes) != 6 {
			t.Errorf("cluster nodes = %d", len(nodes))
		}

		slot := get[service.KeySlotResult](t, e, http.MethodGet, "/api/v1/cluster/keyslot?key=foo", "")
		if slot.Slot != 12182 || slot.Master == "" {
			t.Errorf("keyslot = %+v", slot)
		}

		value(t, e, http.MethodPost, "/api/v1/command", "SET foo bar")

		keys := get[service.SlotKeysResult](t, e, http.MethodGet, "/api/v1/cluster/slots/12182/keys", "")
		if keys.Count != 1 || !slices.Contains(keys.Keys, "foo") || keys.Node != slot.Master {
			t.Errorf("slot keys = %+v, master %s", keys, slot.Master)
		}
	})

	t.Run("replica reads", func(t *testing.T) {
		value(t, e, http.MethodPost, "/api/v1/command", "SET {r}x replicated")

		replica := replicaOf(t, e, get[service.KeySlotResult](t, e, http.MethodGet, "/api/v1/cluster/keyslot?key={r}x", "").Master)

		// Pooled replica connections are not in READONLY mode: the replica redirects to the master.
		expectStatus(t, e, fullToken, http.MethodPost, "/api/v1/command?node="+replica, "GET {r}x", http.StatusMisdirectedRequest, "REDIS_REDIRECT")

		// Sessions enable READONLY on replicas.
		eventually(t, 5*time.Second, "replica read", func() bool {
			res := get[service.PipelineResult](t, e, http.MethodPost, "/api/v1/pipeline?session&node="+replica, "GET {r}x")

			return res.Results[0].Value == "replicated"
		})
	})

	t.Run("failover", func(t *testing.T) {
		master := get[service.KeySlotResult](t, e, http.MethodGet, "/api/v1/cluster/keyslot?key={f}k", "").Master
		replica := replicaOf(t, e, master)

		value(t, e, http.MethodPost, "/api/v1/cluster/failover?node="+replica, "")

		eventually(t, 30*time.Second, "failover", func() bool {
			return get[service.KeySlotResult](t, e, http.MethodGet, "/api/v1/cluster/keyslot?key={f}k", "").Master == replica
		})

		// Routing follows the new master.
		value(t, e, http.MethodPost, "/api/v1/command", "SET {f}k after-failover")

		if got := value(t, e, http.MethodPost, "/api/v1/command", "GET {f}k"); got != "after-failover" {
			t.Errorf("GET after failover = %v", got)
		}
	})
}

// replicaOf returns a replica of the master from CLUSTER NODES.
func replicaOf(t *testing.T, e *env, master string) string {
	t.Helper()

	nodes := get[service.CommandResult](t, e, http.MethodGet, "/api/v1/cluster/nodes", "").Value.([]any)

	var masterID string

	for _, n := range nodes {
		if node := n.(map[string]any); node["addr"] == master {
			masterID = node["id"].(string)
		}
	}

	for _, n := range nodes {
		if node := n.(map[string]any); node["master_id"] == masterID && node["link_state"] == "connected" {
			return node["addr"].(string)
		}
	}

	t.Fatalf("no replica of %s", master)

	return ""
}

func TestSentinel(t *testing.T) {
	e := newEnv(t, "REDIGATE_TEST_SENTINEL")
	topology := common(t, e, expectation{topology: "sentinel", masters: 1, replicas: 2, sentinels: 3})

	t.Run("sentinel commands and failover", func(t *testing.T) {
		sentinel := nodesByRole(topology, "sentinel")[0]
		master := nodesByRole(topology, "master")[0]

		addr := value(t, e, http.MethodPost, "/api/v1/command?node="+sentinel, "SENTINEL get-master-addr-by-name mymaster")
		if got := fmt.Sprintf("%v:%v", addr.([]any)[0], addr.([]any)[1]); got != master {
			t.Fatalf("sentinel master = %s, topology master = %s", got, master)
		}

		// Sentinel commands are unknown to the data nodes command table: read-only access is denied.
		expectStatus(t, e, readOnlyToken, http.MethodPost, "/api/v1/command?node="+sentinel, "SENTINEL masters", http.StatusForbidden, "FORBIDDEN")

		value(t, e, http.MethodPost, "/api/v1/command?node="+sentinel, "SENTINEL failover mymaster")

		eventually(t, 30*time.Second, "sentinel failover", func() bool {
			current := get[service.TopologyInfo](t, e, http.MethodGet, "/api/v1/topology", "")
			masters := nodesByRole(current, "master")

			return len(masters) == 1 && masters[0] != master
		})

		// The failover client follows the new master.
		eventually(t, 30*time.Second, "writes after failover", func() bool {
			status, _ := e.do(t, http.MethodPost, "/api/v1/command", fullToken, "SET after-failover 1")

			return status == http.StatusOK
		})
	})
}

// common runs checks that must pass on every topology and returns the topology.
//
//nolint:maintidx // a single scenario
func common(t *testing.T, e *env, want expectation) service.TopologyInfo {
	t.Helper()

	topology := get[service.TopologyInfo](t, e, http.MethodGet, "/api/v1/topology", "")

	t.Run("topology", func(t *testing.T) {
		got := expectation{
			topology:  topology.Topology,
			masters:   len(nodesByRole(topology, "master")),
			replicas:  len(nodesByRole(topology, "replica")),
			sentinels: len(nodesByRole(topology, "sentinel")),
		}

		if got != want {
			t.Fatalf("topology = %+v, want %+v (nodes %+v)", got, want, topology.Nodes)
		}

		if ping := get[service.PingResult](t, e, http.MethodGet, "/api/v1/ping", ""); !ping.OK {
			t.Errorf("ping = %+v", ping)
		}
	})

	t.Run("flushall", func(t *testing.T) {
		if res := get[service.FanOutResult](t, e, http.MethodPost, "/api/v1/server/flushall", ""); !res.OK || len(res.Nodes) != want.masters {
			t.Fatalf("flushall = %+v", res)
		}
	})

	t.Run("commands", func(t *testing.T) {
		value(t, e, http.MethodPost, "/api/v1/command", `{"args": ["SET", "bin", {"base64": "/wA="}]}`)

		if got := value(t, e, http.MethodPost, "/api/v1/command", "GET bin"); !reflect.DeepEqual(got, map[string]any{"base64": "/wA="}) {
			t.Errorf("GET bin = %#v", got)
		}

		if got := value(t, e, http.MethodPost, "/api/v1/command", "GET missing"); got != nil {
			t.Errorf("GET missing = %#v", got)
		}

		pings := get[service.FanOutResult](t, e, http.MethodPost, "/api/v1/command?target=all", "PING")
		if !pings.OK || len(pings.Nodes) != want.masters+want.replicas {
			t.Errorf("PING on all data nodes = %+v", pings)
		}
	})

	const total = 300

	t.Run("keys across slots", func(t *testing.T) {
		lines := make([]string, 0, total)
		for i := range total {
			lines = append(lines, fmt.Sprintf("SET k:%d %d", i, i))
		}

		res := get[service.PipelineResult](t, e, http.MethodPost, "/api/v1/pipeline", strings.Join(lines, "\n"))
		for i, entry := range res.Results {
			if entry.Error != "" {
				t.Fatalf("SET %d: %s", i, entry.Error)
			}
		}

		seen := make(map[string]bool)
		cursor := ""

		for range 100 {
			page := get[service.ScanResult](t, e, http.MethodGet, "/api/v1/keys?match=k:*&count=50&cursor="+cursor, "")
			for _, key := range page.Keys {
				seen[key.(string)] = true
			}

			if cursor = page.Cursor; cursor == "0" {
				break
			}
		}

		if len(seen) != total {
			t.Errorf("scan found %d keys, want %d", len(seen), total)
		}

		size := get[service.DBSizeResult](t, e, http.MethodGet, "/api/v1/server/dbsize", "")
		if size.Total < total {
			t.Errorf("dbsize total = %d", size.Total)
		}

		query := make([]string, 0, total)
		for i := range total {
			query = append(query, fmt.Sprintf("key=k:%d", i))
		}

		exists := get[service.MultiKeyResult](t, e, http.MethodGet, "/api/v1/keys/exists?"+strings.Join(query, "&"), "")
		if exists.Count != total {
			t.Errorf("exists = %d", exists.Count)
		}

		deleted := get[service.MultiKeyResult](t, e, http.MethodDelete, "/api/v1/keys?"+strings.Join(query[:10], "&"), "")
		if deleted.Count != 10 {
			t.Errorf("deleted = %d", deleted.Count)
		}

		byPattern := get[service.DeleteByPatternResult](t, e, http.MethodPost, "/api/v1/keys/delete", `{"match": "k:*"}`)
		if byPattern.Deleted != total-10 || byPattern.Cursor != "0" {
			t.Errorf("delete by pattern = %+v", byPattern)
		}
	})

	t.Run("values", func(t *testing.T) {
		value(t, e, http.MethodPut, "/api/v1/hashes", `{"key": "h", "fields": [{"field": "f", "value": "v"}]}`)
		value(t, e, http.MethodPost, "/api/v1/zsets/add", `{"key": "z", "members": [{"member": "m", "score": 2.5}]}`)
		value(t, e, http.MethodPost, "/api/v1/streams/add", `{"key": "s", "id": "1-1", "fields": [{"field": "f", "value": "v"}]}`)

		for key, want := range map[string]any{
			"h": map[string]any{"f": "v"},
			"z": []any{map[string]any{"member": "m", "score": 2.5}},
			"s": []any{map[string]any{"id": "1-1", "fields": map[string]any{"f": "v"}}},
		} {
			got := get[map[string]any](t, e, http.MethodGet, "/api/v1/keys/value?key="+key, "")
			if !reflect.DeepEqual(got["value"], want) {
				t.Errorf("value of %s = %#v", key, got["value"])
			}
		}

		meta := get[map[string]any](t, e, http.MethodGet, "/api/v1/keys/meta?key=h", "")
		if meta["encoding"] == "" || meta["memory_bytes"] == nil || meta["length"] != float64(1) {
			t.Errorf("meta = %v", meta)
		}
	})

	t.Run("read-only policy", func(t *testing.T) {
		for body, status := range map[string]int{
			"GET bin":                http.StatusOK,
			"INFO server":            http.StatusOK,
			"KEYS *":                 http.StatusOK,
			"CONFIG GET maxmemory":   http.StatusOK,
			"CLIENT LIST":            http.StatusOK,
			"EVAL_RO \"return 1\" 0": http.StatusOK,
			"SET h x":                http.StatusForbidden,
			"CONFIG SET maxmemory 0": http.StatusForbidden,
			"CLIENT KILL ID 1":       http.StatusForbidden,
			"FLUSHALL":               http.StatusForbidden,
			"EVAL \"return 1\" 0":    http.StatusForbidden,
			"FUNCTION FLUSH":         http.StatusForbidden,
			"PUBLISH ch m":           http.StatusForbidden,
		} {
			expectStatus(t, e, readOnlyToken, http.MethodPost, "/api/v1/command", body, status, "")
		}
	})

	t.Run("server", func(t *testing.T) {
		info := get[service.FanOutResult](t, e, http.MethodGet, "/api/v1/server/info?section=server", "")
		for _, node := range info.Nodes {
			server, _ := node.Value.(map[string]any)["server"].(map[string]any)
			if server["redis_version"] == nil {
				t.Errorf("INFO of %s has no redis_version: %v", node.Addr, node.Value)
			}
		}

		for _, path := range []string{"/api/v1/server/config?pattern=maxmemory", "/api/v1/server/slowlog", "/api/v1/server/clients", "/api/v1/server/memory"} {
			if res := get[service.FanOutResult](t, e, http.MethodGet, path, ""); !res.OK {
				t.Errorf("%s = %+v", path, res)
			}
		}

		commands := get[[]service.CommandInfo](t, e, http.MethodGet, "/api/v1/server/commands?prefix=config", "")
		if len(commands) != 1 || !slices.ContainsFunc(commands[0].Subcommands, func(c service.CommandInfo) bool { return c.Name == "config|set" }) {
			t.Errorf("config command = %+v", commands)
		}

		modules := get[service.ModulesResult](t, e, http.MethodGet, "/api/v1/server/modules", "")
		t.Logf("capabilities: %v", modules.Capabilities)

		if modules.Capabilities["json"] {
			value(t, e, http.MethodPost, "/api/v1/command", `JSON.SET doc $ "{\"a\":[1,2]}"`)

			doc := get[map[string]any](t, e, http.MethodGet, "/api/v1/keys/value?key=doc", "")
			if !reflect.DeepEqual(doc["value"], map[string]any{"a": []any{float64(1), float64(2)}}) {
				t.Errorf("JSON value = %#v", doc)
			}
		}
	})

	streams(t, e)
	capabilities := get[service.ModulesResult](t, e, http.MethodGet, "/api/v1/server/modules", "").Capabilities
	scriptsAndTransfer(t, e, capabilities)
	modules(t, e, capabilities)

	if want.topology != "cluster" {
		t.Run("cluster routes", func(t *testing.T) {
			expectStatus(t, e, fullToken, http.MethodGet, "/api/v1/cluster/nodes", "", http.StatusNotImplemented, "NOT_SUPPORTED")
		})
	}

	return topology
}
