//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/btrvodka/redigate/internal/service"
)

//nolint:maintidx // one scenario per module
func modules(t *testing.T, e *env, capabilities map[string]bool) {
	t.Helper()

	t.Run("modules", func(t *testing.T) {
		if !capabilities["json"] {
			expectStatus(t, e, fullToken, http.MethodGet, "/api/v1/json?key=k", "", http.StatusNotImplemented, "NOT_SUPPORTED")
			expectStatus(t, e, fullToken, http.MethodGet, "/api/v1/search/indexes", "", http.StatusNotImplemented, "NOT_SUPPORTED")
			t.Skip("the server has no modules")
		}

		t.Run("json", func(t *testing.T) {
			value(t, e, http.MethodPut, "/api/v1/json", `{"key": "j:1", "value": {"name": "a", "n": 1, "tags": ["x"]}}`)
			value(t, e, http.MethodPut, "/api/v1/json", `{"key": "j:2", "value": {"name": "b", "n": 2, "tags": []}}`)

			if got := value(t, e, http.MethodGet, "/api/v1/json?key=j:1&path=$.name", ""); !reflect.DeepEqual(got, []any{"a"}) {
				t.Errorf("json.get = %#v", got)
			}

			if got := value(t, e, http.MethodPost, "/api/v1/json/numincr", `{"key": "j:1", "path": "$.n", "by": 5}`); !reflect.DeepEqual(got, []any{float64(6)}) {
				t.Errorf("numincrby = %#v", got)
			}

			value(t, e, http.MethodPost, "/api/v1/json/arrappend", `{"key": "j:1", "path": "$.tags", "values": ["y", {"z": 1}]}`)
			value(t, e, http.MethodPost, "/api/v1/json/merge", `{"key": "j:2", "value": {"name": "B", "extra": true}}`)

			// Keys in different slots.
			mget := get[service.MultiKeyResult](t, e, http.MethodGet, "/api/v1/json/mget?key=j:1&key=j:2&path=$", "")
			want := []any{
				[]any{map[string]any{"name": "a", "n": float64(6), "tags": []any{"x", "y", map[string]any{"z": float64(1)}}}},
				[]any{map[string]any{"name": "B", "n": float64(2), "tags": []any{}, "extra": true}},
			}

			for i, res := range mget.Results {
				if !reflect.DeepEqual(res.Value, want[i]) {
					t.Errorf("json.mget %d = %#v", i, res.Value)
				}
			}
		})

		t.Run("search", func(t *testing.T) {
			value(t, e, http.MethodPost, "/api/v1/search/indexes", `{"index": "users", "on": "hash", "prefixes": ["u:"],
				"schema": [{"field": "name", "type": "text"}, {"field": "age", "type": "numeric", "options": ["SORTABLE"]},
				{"field": "city", "type": "tag"}]}`)

			for i, city := range []string{"paris", "paris", "rome", "oslo", "rome", "paris"} {
				value(t, e, http.MethodPut, "/api/v1/hashes",
					fmt.Sprintf(`{"key": "u:%d", "fields": [{"field": "name", "value": "user%d"}, {"field": "age", "value": "%d"}, {"field": "city", "value": "%s"}]}`, i, i, 20+i, city))
			}

			// Indexing is asynchronous.
			var res map[string]any

			eventually(t, 10*time.Second, "indexing", func() bool {
				res = value(t, e, http.MethodPost, "/api/v1/search/indexes/users/search",
					`{"query": "@city:{paris}", "sort_by": "age", "sort_desc": true, "return": ["name"], "with_scores": true}`).(map[string]any)

				return res["total"] == float64(3)
			})

			results := res["results"].([]any)
			first := results[0].(map[string]any)

			if len(results) != 3 || first["id"] != "u:5" || first["fields"].(map[string]any)["name"] != "user5" || first["score"] == nil {
				t.Errorf("search = %#v", res)
			}

			agg := value(t, e, http.MethodPost, "/api/v1/search/indexes/users/aggregate",
				`{"query": "*", "args": ["GROUPBY", "1", "@city", "REDUCE", "COUNT", "0", "AS", "n", "SORTBY", "2", "@n", "DESC"]}`).(map[string]any)
			if rows := agg["rows"].([]any); len(rows) != 3 || rows[0].(map[string]any)["city"] != "paris" {
				t.Errorf("aggregate = %#v", agg)
			}

			info := value(t, e, http.MethodGet, "/api/v1/search/indexes/users", "").(map[string]any)
			if info["index_name"] != "users" {
				t.Errorf("info = %v", info["index_name"])
			}

			if list := value(t, e, http.MethodGet, "/api/v1/search/indexes", "").([]any); len(list) != 1 {
				t.Errorf("indexes = %v", list)
			}

			value(t, e, http.MethodDelete, "/api/v1/search/indexes/users?delete_documents", "")

			if list := value(t, e, http.MethodGet, "/api/v1/search/indexes", "").([]any); len(list) != 0 {
				t.Errorf("indexes after drop = %v", list)
			}
		})

		t.Run("timeseries", func(t *testing.T) {
			for _, key := range []string{"ts:a", "ts:b"} {
				value(t, e, http.MethodPost, "/api/v1/timeseries", `{"key": "`+key+`", "labels": {"kind": "rg", "name": "`+key+`"}}`)
			}

			madd := get[service.PipelineResult](t, e, http.MethodPost, "/api/v1/timeseries/madd", `{"samples": [
				{"key": "ts:a", "timestamp": 1000, "value": 1.5}, {"key": "ts:a", "timestamp": 2000, "value": 2.5},
				{"key": "ts:b", "timestamp": 1000, "value": 10}]}`)
			for i, res := range madd.Results {
				if res.Error != "" {
					t.Errorf("madd %d: %s", i, res.Error)
				}
			}

			want := []any{map[string]any{"timestamp": float64(1000), "value": 1.5}, map[string]any{"timestamp": float64(2000), "value": 2.5}}
			if got := value(t, e, http.MethodGet, "/api/v1/timeseries/range?key=ts:a", ""); !reflect.DeepEqual(got, want) {
				t.Errorf("range = %#v", got)
			}

			if got := value(t, e, http.MethodGet, "/api/v1/timeseries/get?key=ts:a", ""); !reflect.DeepEqual(got, want[1]) {
				t.Errorf("get = %#v", got)
			}

			mrange := value(t, e, http.MethodGet, "/api/v1/timeseries/mrange?filter=kind=rg&with_labels", "").([]any)
			if len(mrange) != 2 || mrange[0].(map[string]any)["key"] != "ts:a" ||
				mrange[0].(map[string]any)["labels"].(map[string]any)["name"] != "ts:a" {
				t.Errorf("mrange = %#v", mrange)
			}

			if mget := value(t, e, http.MethodGet, "/api/v1/timeseries/mget?filter=kind=rg", "").([]any); len(mget) != 2 {
				t.Errorf("mget = %#v", mget)
			}

			agg := value(t, e, http.MethodGet, "/api/v1/timeseries/range?key=ts:a&aggregation=sum&bucket_ms=10000", "")
			if !reflect.DeepEqual(agg, []any{map[string]any{"timestamp": float64(0), "value": float64(4)}}) {
				t.Errorf("aggregated range = %#v", agg)
			}
		})

		t.Run("probabilistic", func(t *testing.T) {
			value(t, e, http.MethodPost, "/api/v1/bloom/add", `{"key": "bf", "items": ["a", "b"]}`)

			if got := value(t, e, http.MethodGet, "/api/v1/bloom/exists?key=bf&item=a&item=zzz", ""); !reflect.DeepEqual(got, truthyPair(got)) {
				t.Errorf("bf.mexists = %#v", got)
			}

			value(t, e, http.MethodPost, "/api/v1/cuckoo/add", `{"key": "cf", "items": ["a"]}`)

			if got := value(t, e, http.MethodGet, "/api/v1/cuckoo/count?key=cf&item=a", ""); got != float64(1) {
				t.Errorf("cf.count = %#v", got)
			}

			value(t, e, http.MethodPost, "/api/v1/cms", `{"key": "cms", "width": 100, "depth": 5}`)
			value(t, e, http.MethodPost, "/api/v1/cms/incr", `{"key": "cms", "items": [{"item": "a", "increment": 3}]}`)

			if got := value(t, e, http.MethodGet, "/api/v1/cms/query?key=cms&item=a", ""); !reflect.DeepEqual(got, []any{float64(3)}) {
				t.Errorf("cms.query = %#v", got)
			}

			value(t, e, http.MethodPost, "/api/v1/topk", `{"key": "topk", "k": 2}`)
			value(t, e, http.MethodPost, "/api/v1/topk/incr", `{"key": "topk", "items": [{"item": "a", "increment": 5}, {"item": "b", "increment": 1}]}`)

			if got := value(t, e, http.MethodGet, "/api/v1/topk/list?key=topk&with_count", "").([]any); len(got) != 2 ||
				got[0].(map[string]any)["item"] != "a" {
				t.Errorf("topk.list = %#v", got)
			}

			value(t, e, http.MethodPost, "/api/v1/tdigest", `{"key": "td"}`)
			value(t, e, http.MethodPost, "/api/v1/tdigest/add", `{"key": "td", "values": [1, 2, 3, 4, 5]}`)

			if got := value(t, e, http.MethodGet, "/api/v1/tdigest/max?key=td", ""); fmt.Sprint(got) != "5" {
				t.Errorf("tdigest.max = %#v", got)
			}

			if got := value(t, e, http.MethodGet, "/api/v1/tdigest/quantile?key=td&q=0&q=1", "").([]any); len(got) != 2 {
				t.Errorf("tdigest.quantile = %#v", got)
			}
		})

		if !capabilities["vectorset"] {
			return
		}

		t.Run("vectorsets", func(t *testing.T) {
			value(t, e, http.MethodPost, "/api/v1/vectorsets/add", `{"key": "vs", "element": "a", "vector": [1, 0], "attributes": {"color": "red"}}`)
			value(t, e, http.MethodPost, "/api/v1/vectorsets/add", `{"key": "vs", "element": "b", "vector": [0, 1]}`)

			res := value(t, e, http.MethodPost, "/api/v1/vectorsets/search", `{"key": "vs", "vector": [0.9, 0.1], "count": 2}`).([]any)
			if len(res) != 2 || res[0].(map[string]any)["element"] != "a" {
				t.Errorf("vsim = %#v", res)
			}

			if got := value(t, e, http.MethodGet, "/api/v1/vectorsets/attributes?key=vs&element=a", ""); !reflect.DeepEqual(got, map[string]any{"color": "red"}) {
				t.Errorf("vgetattr = %#v", got)
			}

			if emb := value(t, e, http.MethodGet, "/api/v1/vectorsets/embedding?key=vs&element=a", "").([]any); len(emb) != 2 {
				t.Errorf("vemb = %#v", emb)
			}

			filtered := value(t, e, http.MethodPost, "/api/v1/vectorsets/search", `{"key": "vs", "element": "b", "filter": ".color == \"red\""}`).([]any)
			if len(filtered) != 1 || !strings.EqualFold(filtered[0].(map[string]any)["element"].(string), "a") {
				t.Errorf("filtered vsim = %#v", filtered)
			}
		})
	})
}

// truthyPair is the expected BF.MEXISTS reply for one present and one absent item in either protocol.
func truthyPair(got any) any {
	if items, ok := got.([]any); ok && len(items) == 2 {
		if _, isBool := items[0].(bool); isBool {
			return []any{true, false}
		}
	}

	return []any{float64(1), float64(0)}
}
