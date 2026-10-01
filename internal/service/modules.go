package service

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/btrvodka/redigate/internal/redisx"
)

// RequireCapability fails with ErrNotSupported when the server lacks a module
// (JSON, Search, TimeSeries, probabilistic structures, vector sets).
func (s *Service) RequireCapability(name string) error {
	command, ok := capabilityCommands[name]
	if !ok {
		return fmt.Errorf("unknown capability %q", name)
	}

	if !s.redis.Commands().Has(command) {
		return fmt.Errorf("%w: %s is not available, the server has no %s command",
			redisx.ErrNotSupported, name, command)
	}

	return nil
}

// Module replies differ between RESP2 (flat arrays) and RESP3 (maps); shapes
// below normalize both forms.

type searchShape struct {
	withScores bool
	noContent  bool
}

// ShapeSearch converts FT.SEARCH to {"total", "results": [{"id", "score", "fields"}]}.
func ShapeSearch(withScores, noContent bool) func(any) any {
	return searchShape{withScores: withScores, noContent: noContent}.shape
}

func (sh searchShape) shape(reply any) any {
	if m, ok := reply.(map[any]any); ok {
		results, _ := m["results"].([]any)
		out := make([]any, 0, len(results))

		for _, item := range results {
			doc, _ := item.(map[any]any)
			entry := map[any]any{"id": doc["id"], "fields": ShapeMap(doc["extra_attributes"])}

			if score, ok := doc["score"]; ok {
				entry["score"] = toScore(score)
			}

			out = append(out, entry)
		}

		return map[any]any{"total": m["total_results"], "results": out}
	}

	items, ok := reply.([]any)
	if !ok || len(items) == 0 {
		return reply
	}

	out := make([]any, 0)

	for i := 1; i < len(items); {
		entry := map[any]any{"id": items[i]}
		i++

		if sh.withScores && i < len(items) {
			entry["score"] = toScore(items[i])
			i++
		}

		if !sh.noContent && i < len(items) {
			entry["fields"] = ShapeMap(items[i])
			i++
		}

		out = append(out, entry)
	}

	return map[any]any{"total": items[0], "results": out}
}

// ShapeAggregate converts FT.AGGREGATE to {"total", "rows": [{field: value}]}.
func ShapeAggregate(reply any) any {
	if m, ok := reply.(map[any]any); ok {
		results, _ := m["results"].([]any)
		rows := make([]any, 0, len(results))

		for _, item := range results {
			row, _ := item.(map[any]any)
			rows = append(rows, ShapeMap(row["extra_attributes"]))
		}

		return map[any]any{"total": m["total_results"], "rows": rows}
	}

	items, ok := reply.([]any)
	if !ok || len(items) == 0 {
		return reply
	}

	rows := make([]any, 0, len(items)-1)
	for _, item := range items[1:] {
		rows = append(rows, ShapeMap(item))
	}

	return map[any]any{"total": items[0], "rows": rows}
}

// ShapeSample converts a [timestamp, value] pair to {"timestamp", "value"}.
func ShapeSample(reply any) any {
	pair, ok := reply.([]any)
	if !ok || len(pair) != 2 { //nolint:mnd // timestamp and value
		// TS.GET of an empty series.
		if ok && len(pair) == 0 {
			return nil
		}

		return reply
	}

	return map[any]any{"timestamp": pair[0], "value": toScore(pair[1])}
}

// ShapeSamples converts [[timestamp, value], ...].
func ShapeSamples(reply any) any {
	items, ok := reply.([]any)
	if !ok {
		return reply
	}

	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, ShapeSample(item))
	}

	return out
}

// ShapeTSMulti converts TS.MRANGE (single=false) and TS.MGET (single=true) to
// [{"key", "labels", "samples" | "sample"}].
func ShapeTSMulti(single bool) func(any) any {
	return func(reply any) any {
		var out []any

		add := func(key, labels, data any) {
			entry := map[any]any{"key": key, "labels": labelMap(labels)}
			if single {
				entry["sample"] = ShapeSample(data)
			} else {
				entry["samples"] = ShapeSamples(data)
			}

			out = append(out, entry)
		}

		switch v := reply.(type) {
		case []any:
			// RESP2: [[key, labels, samples], ...].
			for _, item := range v {
				if series, ok := item.([]any); ok && len(series) >= 3 { //nolint:mnd // key, labels, data
					add(series[0], series[1], series[len(series)-1])
				}
			}
		case map[any]any:
			// RESP3: {key: [labels, (metadata,) data]}.
			for key, item := range v {
				if series, ok := item.([]any); ok && len(series) >= 2 { //nolint:mnd // labels and data
					add(key, series[0], series[len(series)-1])
				}
			}
		default:
			return reply
		}

		// The order of series is unspecified (RESP3 maps, shards of a cluster): sort by key.
		slices.SortFunc(out, func(a, b any) int {
			return cmp.Compare(fmt.Sprint(a.(map[any]any)["key"]), fmt.Sprint(b.(map[any]any)["key"])) //nolint:forcetypeassert // built above
		})

		return out
	}
}

// labelMap converts labels: RESP2 [[name, value], ...] or a RESP3 map.
func labelMap(labels any) any {
	items, ok := labels.([]any)
	if !ok {
		return labels
	}

	out := make(map[any]any, len(items))

	for _, item := range items {
		if pair, ok := item.([]any); ok && len(pair) == 2 { //nolint:mnd // name and value
			out[pair[0]] = pair[1]
		}
	}

	return out
}

// ShapeElementScores converts VSIM WITHSCORES (RESP2 flat list, RESP3 map) to
// [{"element", "score"}] ordered by score descending.
func ShapeElementScores(reply any) any {
	var out []any

	add := func(element, score any) {
		out = append(out, map[any]any{"element": element, "score": toScore(score)})
	}

	switch v := reply.(type) {
	case []any:
		for i := 0; i+1 < len(v); i += 2 {
			add(v[i], v[i+1])
		}

		return out
	case map[any]any:
		for element, score := range v {
			add(element, score)
		}
	default:
		return reply
	}

	slices.SortFunc(out, func(a, b any) int {
		sa, _ := a.(map[any]any)["score"].(float64)
		sb, _ := b.(map[any]any)["score"].(float64)

		return cmp.Compare(sb, sa)
	})

	return out
}

// ShapeJSON decodes JSON replies (JSON.GET, JSON.NUMINCRBY, VGETATTR).
func ShapeJSON(reply any) any {
	return decodeJSONValue(reply)
}
