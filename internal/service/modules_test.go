package service

import (
	"reflect"
	"testing"
)

func TestShapeSearch(t *testing.T) {
	t.Parallel()

	want := map[any]any{"total": int64(2), "results": []any{
		map[any]any{"id": "doc:1", "score": 1.0, "fields": map[any]any{"age": "30", "name": "alice"}},
		map[any]any{"id": "doc:2", "score": 1.0, "fields": map[any]any{"age": "40", "name": "bob"}},
	}}

	resp2 := []any{int64(2), "doc:1", "1", []any{"age", "30", "name", "alice"}, "doc:2", "1", []any{"age", "40", "name", "bob"}}
	resp3 := map[any]any{
		"attributes": []any{}, "format": "STRING", "total_results": int64(2), "warning": []any{},
		"results": []any{
			map[any]any{"id": "doc:1", "score": 1.0, "extra_attributes": map[any]any{"age": "30", "name": "alice"}, "values": []any{}},
			map[any]any{"id": "doc:2", "score": 1.0, "extra_attributes": map[any]any{"age": "40", "name": "bob"}, "values": []any{}},
		},
	}

	for name, reply := range map[string]any{"RESP2": resp2, "RESP3": resp3} {
		if got := ShapeSearch(true, false)(reply); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %#v", name, got)
		}
	}

	noContent := ShapeSearch(false, true)([]any{int64(1), "doc:1"})
	if !reflect.DeepEqual(noContent, map[any]any{"total": int64(1), "results": []any{map[any]any{"id": "doc:1"}}}) {
		t.Errorf("NOCONTENT: %#v", noContent)
	}
}

func TestShapeAggregate(t *testing.T) {
	t.Parallel()

	want := map[any]any{"total": int64(2), "rows": []any{map[any]any{"name": "alice"}, map[any]any{"name": "bob"}}}
	resp2 := []any{int64(2), []any{"name", "alice"}, []any{"name", "bob"}}
	resp3 := map[any]any{"total_results": int64(2), "results": []any{
		map[any]any{"extra_attributes": map[any]any{"name": "alice"}, "values": []any{}},
		map[any]any{"extra_attributes": map[any]any{"name": "bob"}, "values": []any{}},
	}}

	for name, reply := range map[string]any{"RESP2": resp2, "RESP3": resp3} {
		if got := ShapeAggregate(reply); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %#v", name, got)
		}
	}
}

func TestShapeTimeSeries(t *testing.T) {
	t.Parallel()

	samples := []any{map[any]any{"timestamp": int64(1000), "value": 1.5}, map[any]any{"timestamp": int64(2000), "value": 2.5}}

	if got := ShapeSamples([]any{[]any{int64(1000), "1.5"}, []any{int64(2000), "2.5"}}); !reflect.DeepEqual(got, samples) {
		t.Errorf("RESP2 range: %#v", got)
	}

	if got := ShapeSamples([]any{[]any{int64(1000), 1.5}, []any{int64(2000), 2.5}}); !reflect.DeepEqual(got, samples) {
		t.Errorf("RESP3 range: %#v", got)
	}

	if got := ShapeSample([]any{}); got != nil {
		t.Errorf("empty TS.GET: %#v", got)
	}

	want := []any{map[any]any{"key": "ts:a", "labels": map[any]any{"sensor": "1"}, "samples": samples}}

	resp2 := []any{[]any{"ts:a", []any{[]any{"sensor", "1"}}, []any{[]any{int64(1000), "1.5"}, []any{int64(2000), "2.5"}}}}
	resp3 := map[any]any{"ts:a": []any{
		map[any]any{"sensor": "1"},
		map[any]any{"aggregators": []any{}},
		[]any{[]any{int64(1000), 1.5}, []any{int64(2000), 2.5}},
	}}

	for name, reply := range map[string]any{"RESP2": resp2, "RESP3": resp3} {
		if got := ShapeTSMulti(false)(reply); !reflect.DeepEqual(got, want) {
			t.Errorf("%s mrange: %#v", name, got)
		}
	}

	mget := ShapeTSMulti(true)(map[any]any{"ts:a": []any{map[any]any{}, []any{int64(2000), 2.5}}})
	if !reflect.DeepEqual(mget, []any{map[any]any{"key": "ts:a", "labels": map[any]any{}, "sample": samples[1]}}) {
		t.Errorf("RESP3 mget: %#v", mget)
	}
}

func TestShapeElementScores(t *testing.T) {
	t.Parallel()

	want := []any{map[any]any{"element": "a", "score": 0.99}, map[any]any{"element": "b", "score": 0.5}}

	if got := ShapeElementScores([]any{"a", "0.99", "b", "0.5"}); !reflect.DeepEqual(got, want) {
		t.Errorf("RESP2: %#v", got)
	}

	if got := ShapeElementScores(map[any]any{"b": 0.5, "a": 0.99}); !reflect.DeepEqual(got, want) {
		t.Errorf("RESP3 must be ordered by score: %#v", got)
	}
}
