package service

import (
	"math"
	"strconv"

	"github.com/btrvodka/redigate/internal/codec"
)

// Shapes convert raw replies before encoding. Maps are built as map[any]any so
// the encoder keeps binary-safe keys and falls back to pairs when needed.

// ShapeMap converts a RESP2 flat [k, v, k, v] array to a map. RESP3 maps pass through.
func ShapeMap(reply any) any {
	items, ok := reply.([]any)
	if !ok {
		return reply
	}

	out := make(map[any]any, len(items)/2) //nolint:mnd // pairs
	for i := 0; i+1 < len(items); i += 2 {
		out[items[i]] = items[i+1]
	}

	return out
}

// ShapeScanPage converts a [cursor, items] SCAN-family reply to {"cursor", "items"}.
func ShapeScanPage(items func(any) any) func(any) any {
	return func(reply any) any {
		page, ok := reply.([]any)
		if !ok || len(page) != 2 { //nolint:mnd // cursor and items
			return reply
		}

		if items != nil {
			page[1] = items(page[1])
		}

		return map[any]any{"cursor": page[0], "items": page[1]}
	}
}

// ShapeScored converts member-score replies (ZRANGE WITHSCORES, ZSCAN, ZPOPMIN)
// in RESP2 flat or RESP3 nested form to [{"member", "score"}].
func ShapeScored(reply any) any {
	items, ok := reply.([]any)
	if !ok {
		return reply
	}

	out := make([]any, 0, len(items))

	appendPair := func(member, score any) {
		out = append(out, map[any]any{"member": member, "score": toScore(score)})
	}

	if len(items) > 0 {
		if _, nested := items[0].([]any); nested {
			for _, item := range items {
				if pair, ok := item.([]any); ok && len(pair) == 2 { //nolint:mnd // member and score
					appendPair(pair[0], pair[1])
				}
			}

			return out
		}
	}

	for i := 0; i+1 < len(items); i += 2 {
		appendPair(items[i], items[i+1])
	}

	return out
}

func toScore(v any) any {
	if s, ok := v.(string); ok {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}

	return v
}

// ShapeStreamEntries converts [[id, [field, value, ...]], ...] to [{"id", "fields"}].
func ShapeStreamEntries(reply any) any {
	items, ok := reply.([]any)
	if !ok {
		return reply
	}

	out := make([]any, 0, len(items))

	for _, item := range items {
		entry, ok := item.([]any)
		if !ok || len(entry) != 2 { //nolint:mnd // id and fields
			out = append(out, item)

			continue
		}

		out = append(out, map[any]any{"id": entry[0], "fields": ShapeMap(entry[1])})
	}

	return out
}

// ShapeStreamRead converts XREAD/XREADGROUP replies (RESP2 [[key, entries]], RESP3 {key: entries})
// to [{"stream", "entries"}].
func ShapeStreamRead(reply any) any {
	var out []any

	add := func(key, entries any) {
		out = append(out, map[any]any{"stream": key, "entries": ShapeStreamEntries(entries)})
	}

	switch v := reply.(type) {
	case []any:
		for _, item := range v {
			if pair, ok := item.([]any); ok && len(pair) == 2 { //nolint:mnd // key and entries
				add(pair[0], pair[1])
			}
		}
	case map[any]any:
		for key, entries := range v {
			add(key, entries)
		}
	default:
		return reply
	}

	return out
}

// ShapeMapList converts a list of flat key-value replies (ACL LOG, XINFO GROUPS) to objects.
func ShapeMapList(reply any) any {
	items, ok := reply.([]any)
	if !ok {
		return reply
	}

	out := make([]any, len(items))
	for i, item := range items {
		out[i] = ShapeMap(item)
	}

	return out
}

// encodeAll makes a shaped reply JSON-encodable without limits.
func encodeAll(v any) any {
	return codec.NewEncoder(codec.EncodingAuto, math.MaxInt).Encode(v)
}

func encodeBase64(v any) any {
	return codec.NewEncoder(codec.EncodingBase64, 1).Encode(v)
}
