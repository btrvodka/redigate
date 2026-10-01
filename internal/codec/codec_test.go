package codec

import (
	"encoding/json"
	"math"
	"math/big"
	"reflect"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestSplitArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		line string
		want Args
	}{
		{"", nil},
		{"   ", nil},
		{"GET key", Args{"GET", "key"}},
		{"  SET   key  value  ", Args{"SET", "key", "value"}},
		{`SET "a key" "a \"value\""`, Args{"SET", "a key", `a "value"`}},
		{`SET k "line\nbreak\ttab"`, Args{"SET", "k", "line\nbreak\ttab"}},
		{`SET k "\x00\xff"`, Args{"SET", "k", "\x00\xff"}},
		{`SET k 'single \'quoted\' \n'`, Args{"SET", "k", `single 'quoted' \n`}},
		{`SET k ""`, Args{"SET", "k", ""}},
		{"SET\tk\r\nv", Args{"SET", "k", "v"}},
	}

	for _, tt := range tests {
		got, err := SplitArgs(tt.line)
		if err != nil {
			t.Errorf("SplitArgs(%q) error = %v", tt.line, err)

			continue
		}

		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitArgs(%q) = %#v, want %#v", tt.line, got, tt.want)
		}
	}

	for _, line := range []string{`GET "unclosed`, `GET 'unclosed`, `GET "a"b`} {
		if _, err := SplitArgs(line); err == nil {
			t.Errorf("SplitArgs(%q) expected error", line)
		}
	}
}

func TestArgsUnmarshalJSON(t *testing.T) {
	t.Parallel()

	var args Args
	if err := json.Unmarshal([]byte(`["SET", "k", 42, 1.5, {"base64": "AP8="}]`), &args); err != nil {
		t.Fatal(err)
	}

	want := Args{"SET", "k", "42", "1.5", []byte{0x00, 0xff}}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("got %#v, want %#v", args, want)
	}

	if args.Name() != "set" || args.String(4) != "\x00\xff" || args.String(10) != "" {
		t.Errorf("unexpected accessors: %q %q", args.Name(), args.String(4))
	}

	for _, raw := range []string{`"SET"`, `[null]`, `[true]`, `[["nested"]]`, `[{"foo": "bar"}]`, `[{"base64": "!!"}]`} {
		if err := json.Unmarshal([]byte(raw), &args); err == nil {
			t.Errorf("Unmarshal(%s) expected error", raw)
		}
	}
}

func TestEncoder(t *testing.T) {
	t.Parallel()

	binary := "\xff\x00"

	tests := []struct {
		name     string
		encoding Encoding
		in       any
		want     any
	}{
		{"nil", EncodingAuto, nil, nil},
		{"utf8 string", EncodingAuto, "привет", "привет"},
		{"binary string", EncodingAuto, binary, Binary{Base64: "/wA="}},
		{"binary as utf8", EncodingUTF8, binary, "�\x00"},
		{"base64", EncodingBase64, "hi", "aGk="},
		{"int", EncodingAuto, int64(7), int64(7)},
		{"bool", EncodingAuto, true, true},
		{"float", EncodingAuto, 1.5, 1.5},
		{"inf", EncodingAuto, math.Inf(1), "inf"},
		{"big", EncodingAuto, big.NewInt(42), json.Number("42")},
		{"nested error", EncodingAuto, []any{"OK", redis.ErrClosed}, []any{"OK", ReplyError{Error: redis.ErrClosed.Error()}}},
		{"array", EncodingAuto, []any{"a", int64(1), nil}, []any{"a", int64(1), nil}},
		{"map", EncodingAuto, map[any]any{"f": "v"}, map[string]any{"f": "v"}},
		{"map with int key", EncodingAuto, map[any]any{int64(1): "v"}, []Pair{{Key: int64(1), Value: "v"}}},
		{"map with binary key", EncodingAuto, map[any]any{binary: "v"}, []Pair{{Key: Binary{Base64: "/wA="}, Value: "v"}}},
		{"map in base64", EncodingBase64, map[any]any{"f": "v"}, map[string]any{"Zg==": "dg=="}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			enc := NewEncoder(tt.encoding, 100)
			if got := enc.Encode(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Encode(%#v) = %#v, want %#v", tt.in, got, tt.want)
			}

			if enc.Truncated() {
				t.Error("unexpected truncation")
			}
		})
	}
}

func TestEncoderTruncates(t *testing.T) {
	t.Parallel()

	enc := NewEncoder(EncodingAuto, 3)

	got := enc.Encode([]any{"a", []any{"b", "c"}, "d"})
	// The outer array takes 3 items, the nested one gets nothing.
	want := []any{"a", []any{}, "d"}

	if !reflect.DeepEqual(got, want) || !enc.Truncated() {
		t.Errorf("Encode() = %#v, truncated = %v", got, enc.Truncated())
	}
}

func TestParseEncoding(t *testing.T) {
	t.Parallel()

	if enc, err := ParseEncoding(""); err != nil || enc != EncodingAuto {
		t.Errorf("ParseEncoding(\"\") = %q, %v", enc, err)
	}

	if _, err := ParseEncoding("hex"); err == nil {
		t.Error("ParseEncoding(hex) expected error")
	}
}
