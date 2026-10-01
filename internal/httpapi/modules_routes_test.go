package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/btrvodka/redigate/internal/config"
	"github.com/btrvodka/redigate/internal/httpapi"
)

func TestModulesNotSupported(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t, config.Auth{})

	for _, tt := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/json?key=k", ""},
		{http.MethodGet, "/api/v1/search/indexes", ""},
		{http.MethodGet, "/api/v1/timeseries/get?key=k", ""},
		{http.MethodPost, "/api/v1/bloom/add", `{"key": "k", "items": ["a"]}`},
		{http.MethodGet, "/api/v1/cuckoo/info?key=k", ""},
		{http.MethodGet, "/api/v1/cms/info?key=k", ""},
		{http.MethodGet, "/api/v1/topk/list?key=k", ""},
		{http.MethodGet, "/api/v1/tdigest/min?key=k", ""},
		{http.MethodGet, "/api/v1/vectorsets/card?key=k", ""},
	} {
		status, env := doBody(t, srv, tt.method, tt.path, "", jsonType, tt.body)
		if status != http.StatusNotImplemented || env.Code != httpapi.CodeNotSupported {
			t.Errorf("%s %s: %d %s", tt.method, tt.path, status, env.Code)
		}
	}
}
