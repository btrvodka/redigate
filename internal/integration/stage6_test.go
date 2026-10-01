//go:build integration

package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/btrvodka/redigate/internal/service"
)

// export returns the NDJSON body of an export.
func (e *env) export(t *testing.T, query string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, e.srv.URL+"/api/v1/keys/export?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer "+fullToken)

	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("export: %d %s %v", resp.StatusCode, body, err)
	}

	// The summary line must report a complete export.
	scanner := bufio.NewScanner(strings.NewReader(string(body)))

	var last map[string]any

	for scanner.Scan() {
		_ = json.Unmarshal(scanner.Bytes(), &last)
	}

	if summary, _ := last["summary"].(map[string]any); summary["complete"] != true {
		t.Fatalf("export is incomplete: %v", last)
	}

	return string(body)
}

func scriptsAndTransfer(t *testing.T, e *env, capabilities map[string]bool) {
	t.Helper()

	t.Run("transfer", func(t *testing.T) {
		formats := []string{"dump", "value"}

		for _, format := range formats {
			prefix := "x" + format + ":"

			// Keys of every core type in different slots.
			value(t, e, http.MethodPut, "/api/v1/strings", `{"key": "`+prefix+`s", "value": {"base64": "/wA="}, "ttl_ms": 3600000}`)
			value(t, e, http.MethodPost, "/api/v1/lists/push", `{"key": "`+prefix+`l", "values": ["a", "b"], "side": "right"}`)
			value(t, e, http.MethodPost, "/api/v1/sets/add", `{"key": "`+prefix+`set", "members": ["m"]}`)
			value(t, e, http.MethodPut, "/api/v1/hashes", `{"key": "`+prefix+`h", "fields": [{"field": "f", "value": "v"}]}`)
			value(t, e, http.MethodPost, "/api/v1/zsets/add", `{"key": "`+prefix+`z", "members": [{"member": "m", "score": 1.5}]}`)
			value(t, e, http.MethodPost, "/api/v1/streams/add", `{"key": "`+prefix+`st", "id": "1-1", "fields": [{"field": "f", "value": "v"}]}`)

			keys := []string{"s", "l", "set", "h", "z", "st"}

			if capabilities["json"] {
				value(t, e, http.MethodPost, "/api/v1/command", `JSON.SET `+prefix+`j $ "{\"a\":1}"`)

				keys = append(keys, "j")
			}

			before := make(map[string]any)
			for _, key := range keys {
				before[key] = get[map[string]any](t, e, http.MethodGet, "/api/v1/keys/value?key="+prefix+key, "")["value"]
			}

			body := e.export(t, "format="+format+"&match="+prefix+"*")

			deleted := get[service.DeleteByPatternResult](t, e, http.MethodPost, "/api/v1/keys/delete", `{"match": "`+prefix+`*"}`)
			if deleted.Deleted != int64(len(keys)) {
				t.Fatalf("%s: deleted %d keys", format, deleted.Deleted)
			}

			res := get[service.ImportResult](t, e, http.MethodPost, "/api/v1/keys/import", body)
			if res.Imported != int64(len(keys)) || res.Failed != 0 {
				t.Fatalf("%s: import = %+v", format, res)
			}

			for _, key := range keys {
				after := get[map[string]any](t, e, http.MethodGet, "/api/v1/keys/value?key="+prefix+key, "")
				if !reflect.DeepEqual(after["value"], before[key]) {
					t.Errorf("%s: %s = %#v, want %#v", format, key, after["value"], before[key])
				}

				if key == "s" && after["ttl_ms"].(float64) <= 0 {
					t.Errorf("%s: ttl is lost: %v", format, after["ttl_ms"])
				}
			}
		}
	})

	t.Run("scripts", func(t *testing.T) {
		load := get[service.FanOutResult](t, e, http.MethodPost, "/api/v1/scripts/load",
			`{"script": "return redis.call('GET', KEYS[1])"}`)
		sha, _ := load.Nodes[0].Value.(string)

		exists := get[service.ScriptExistsResult](t, e, http.MethodGet, "/api/v1/scripts/exists?sha="+sha, "")
		if !load.OK || !exists.Everywhere[sha] {
			t.Fatalf("load = %+v, exists = %+v", load, exists)
		}

		// In cluster EVALSHA goes to the slot owner: the script must be loaded everywhere.
		for i := range 10 {
			key := fmt.Sprintf("script:%d", i)
			value(t, e, http.MethodPost, "/api/v1/command", "SET "+key+" "+key)

			if got := value(t, e, http.MethodPost, "/api/v1/scripts/eval", `{"sha": "`+sha+`", "keys": ["`+key+`"], "read_only": true}`); got != key {
				t.Errorf("evalsha_ro on %s = %v", key, got)
			}
		}
	})

	t.Run("functions", func(t *testing.T) {
		code := "#!lua name=rglib\\nredis.register_function('rgset', function(keys, args) return redis.call('SET', keys[1], args[1]) end)"

		if res := get[service.FanOutResult](t, e, http.MethodPost, "/api/v1/functions", `{"code": "`+code+`", "replace": true}`); !res.OK {
			t.Fatalf("function load = %+v", res)
		}

		for i := range 10 {
			key := fmt.Sprintf("fn:%d", i)
			if got := value(t, e, http.MethodPost, "/api/v1/functions/call", `{"function": "rgset", "keys": ["`+key+`"], "args": ["v"]}`); got != "OK" {
				t.Errorf("fcall on %s = %v", key, got)
			}
		}

		list := get[service.FanOutResult](t, e, http.MethodGet, "/api/v1/functions?library=rglib", "")
		for _, node := range list.Nodes {
			libs, _ := node.Value.([]any)
			if len(libs) != 1 || libs[0].(map[string]any)["library_name"] != "rglib" {
				t.Errorf("functions on %s = %v", node.Addr, node.Value)
			}
		}

		if res := get[service.FanOutResult](t, e, http.MethodDelete, "/api/v1/functions?library=rglib", ""); !res.OK {
			t.Errorf("function delete = %+v", res)
		}
	})

	t.Run("acl", func(t *testing.T) {
		set := get[service.FanOutResult](t, e, http.MethodPut, "/api/v1/acl/users/rgtest", `{"rules": ["on", ">secret", "~*", "+@read"], "reset": true}`)
		if !set.OK {
			t.Fatalf("setuser = %+v", set)
		}

		user := get[service.FanOutResult](t, e, http.MethodGet, "/api/v1/acl/users/rgtest", "")
		for _, node := range user.Nodes {
			if flags, _ := node.Value.(map[string]any)["flags"].([]any); len(flags) == 0 || flags[0] != "on" {
				t.Errorf("user on %s = %v", node.Addr, node.Value)
			}
		}

		if got := value(t, e, http.MethodPost, "/api/v1/acl/dryrun", `{"user": "rgtest", "command": ["GET", "k"]}`); got != "OK" {
			t.Errorf("dryrun GET = %v", got)
		}

		if got := value(t, e, http.MethodPost, "/api/v1/acl/dryrun", `{"user": "rgtest", "command": ["SET", "k", "v"]}`); got == "OK" {
			t.Error("dryrun SET must be denied")
		}

		if res := get[service.FanOutResult](t, e, http.MethodDelete, "/api/v1/acl/users/rgtest", ""); !res.OK {
			t.Errorf("deluser = %+v", res)
		}

		// Changes go to every data node, sentinels are left out.
		if len(set.Nodes) != len(user.Nodes) || len(set.Nodes) == 0 {
			t.Errorf("setuser nodes = %d, getuser nodes = %d", len(set.Nodes), len(user.Nodes))
		}
	})
}
