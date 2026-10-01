package redisx

import (
	"reflect"
	"testing"
)

func TestParseInfo(t *testing.T) {
	t.Parallel()

	raw := "# Server\r\nredis_version:7.4.0\r\nredis_mode:cluster\r\n\r\n# Cluster\r\ncluster_enabled:1\r\n"

	want := map[string]string{
		"redis_version":   "7.4.0",
		"redis_mode":      "cluster",
		"cluster_enabled": "1",
	}

	if got := ParseInfo(raw); !reflect.DeepEqual(got, want) {
		t.Errorf("ParseInfo() = %v, want %v", got, want)
	}
}

func TestReplyToStringMap(t *testing.T) {
	t.Parallel()

	want := map[string]string{"ip": "10.0.0.1", "port": "6379"}

	resp2 := []any{"ip", "10.0.0.1", "port", "6379"}
	resp3 := map[any]any{"ip": "10.0.0.1", "port": int64(6379)}

	for _, reply := range []any{resp2, resp3} {
		if got := replyToStringMap(reply); !reflect.DeepEqual(got, want) {
			t.Errorf("replyToStringMap(%v) = %v, want %v", reply, got, want)
		}
	}
}
