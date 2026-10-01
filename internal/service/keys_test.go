package service

import (
	"reflect"
	"testing"
	"time"
)

func TestScanCursor(t *testing.T) {
	t.Parallel()

	for _, pos := range []scanPosition{{}, {cursor: 42}, {node: "10.0.0.1:6379", cursor: 7}, {node: "[::1]:7000", cursor: 0}} {
		got, err := parseScanCursor(pos.String())
		if err != nil || got != pos {
			t.Errorf("round trip of %+v = %+v, %v", pos, got, err)
		}
	}

	if _, err := parseScanCursor("not a cursor!"); err == nil {
		t.Error("expected error for an invalid cursor")
	}
}

func TestShapes(t *testing.T) {
	t.Parallel()

	scored := []any{map[any]any{"member": "a", "score": 1.5}, map[any]any{"member": "b", "score": 2.0}}

	// RESP2 returns scores as strings in a flat array, RESP3 as doubles in pairs.
	if got := ShapeScored([]any{"a", "1.5", "b", "2"}); !reflect.DeepEqual(got, scored) {
		t.Errorf("RESP2 scored = %#v", got)
	}

	if got := ShapeScored([]any{[]any{"a", 1.5}, []any{"b", 2.0}}); !reflect.DeepEqual(got, scored) {
		t.Errorf("RESP3 scored = %#v", got)
	}

	read := ShapeStreamRead(map[any]any{"s": []any{[]any{"1-0", []any{"f", "v"}}}})
	want := []any{map[any]any{"stream": "s", "entries": []any{map[any]any{"id": "1-0", "fields": map[any]any{"f": "v"}}}}}

	if !reflect.DeepEqual(read, want) {
		t.Errorf("RESP3 stream read = %#v", read)
	}

	if got := ShapeScanPage(ShapeMap)([]any{"0", []any{"f", "v"}}); !reflect.DeepEqual(got, map[any]any{"cursor": "0", "items": map[any]any{"f": "v"}}) {
		t.Errorf("scan page = %#v", got)
	}

	if ttlMS(-1) != -1 || ttlMS(-2) != -2 || ttlMS(1500*time.Millisecond) != 1500 {
		t.Error("unexpected ttl conversion")
	}
}
