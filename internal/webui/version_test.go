package webui

import "testing"

func TestServerVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		server map[string]any
		want   string
	}{
		{map[string]any{"redis_version": "8.10.2"}, "redis 8.10.2"},
		// Valkey keeps redis_version for compatibility.
		{map[string]any{"redis_version": "7.2.4", "server_name": "valkey", "valkey_version": "9.1.2"}, "valkey 9.1.2"},
		{map[string]any{"redis_version": "7.4.1", "server_name": "keydb"}, "keydb 7.4.1"},
		{map[string]any{}, ""},
	}

	for _, tt := range tests {
		if got := serverVersion(tt.server); got != tt.want {
			t.Errorf("serverVersion(%v) = %q, want %q", tt.server, got, tt.want)
		}
	}
}
