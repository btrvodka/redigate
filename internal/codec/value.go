// Package codec converts redis replies to JSON-friendly values and request
// arguments to redis command arguments.
package codec

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

// Encoding defines how binary-unsafe redis strings are represented in JSON.
type Encoding string

const (
	// EncodingAuto returns valid UTF-8 strings as JSON strings and other
	// strings as {"base64": "..."} objects.
	EncodingAuto Encoding = "auto"
	// EncodingUTF8 always returns JSON strings, invalid bytes are replaced with U+FFFD.
	EncodingUTF8 Encoding = "utf8"
	// EncodingBase64 returns every string base64-encoded.
	EncodingBase64 Encoding = "base64"
)

func ParseEncoding(s string) (Encoding, error) {
	switch Encoding(strings.ToLower(s)) {
	case "", EncodingAuto:
		return EncodingAuto, nil
	case EncodingUTF8:
		return EncodingUTF8, nil
	case EncodingBase64:
		return EncodingBase64, nil
	default:
		return "", fmt.Errorf("unknown encoding %q, expected auto, utf8 or base64", s)
	}
}

// Binary is a string that is not valid UTF-8, encoded in the auto mode.
type Binary struct {
	Base64 string `json:"base64"`
}

// ReplyError is an error reply nested in an array, e.g. in EXEC results.
type ReplyError struct {
	Error string `json:"error"`
}

// Pair is a key-value entry of a RESP3 map that cannot be represented as a JSON object.
type Pair struct {
	Key   any `json:"key"`
	Value any `json:"value"`
}

// Encoder converts redis replies into values encodable by encoding/json.
// It limits the total number of collection elements and reports truncation.
type Encoder struct {
	encoding  Encoding
	budget    int
	truncated bool
}

func NewEncoder(encoding Encoding, maxItems int) *Encoder {
	return &Encoder{encoding: encoding, budget: maxItems}
}

// Truncated reports whether some collections were cut to fit the item limit.
func (e *Encoder) Truncated() bool {
	return e.truncated
}

// Encode converts a reply returned by go-redis (Cmd.Result) to a JSON-friendly value.
func (e *Encoder) Encode(v any) any {
	switch v := v.(type) {
	case nil:
		return nil
	case string:
		return e.String(v)
	case []byte:
		return e.String(string(v))
	case int64, bool:
		return v
	case float64:
		return encodeFloat(v)
	case *big.Int:
		return json.Number(v.String())
	case []any:
		return e.encodeSlice(v)
	case map[any]any:
		return e.encodeMap(v)
	case map[string]any:
		// Already decoded JSON (e.g. JSON.GET): keys are kept as they are.
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[key] = e.Encode(value)
		}

		return out
	case redis.Error:
		return ReplyError{Error: v.Error()}
	case error:
		return ReplyError{Error: v.Error()}
	default:
		return fmt.Sprint(v)
	}
}

// String encodes a single redis string according to the encoding.
func (e *Encoder) String(s string) any {
	switch e.encoding {
	case EncodingBase64:
		return base64.StdEncoding.EncodeToString([]byte(s))
	case EncodingUTF8:
		return strings.ToValidUTF8(s, string(utf8.RuneError))
	default:
		if utf8.ValidString(s) {
			return s
		}

		return Binary{Base64: base64.StdEncoding.EncodeToString([]byte(s))}
	}
}

func encodeFloat(f float64) any {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	default:
		return f
	}
}

// take reserves n items from the budget and returns how many may be encoded.
func (e *Encoder) take(n int) int {
	if n > e.budget {
		n = e.budget
		e.truncated = true
	}

	e.budget -= n

	return n
}

func (e *Encoder) encodeSlice(v []any) []any {
	n := e.take(len(v))
	out := make([]any, 0, n)

	for _, item := range v[:n] {
		out = append(out, e.Encode(item))
	}

	return out
}

// encodeMap returns a JSON object when every key is a string representable as
// an object key, and a list of key-value pairs otherwise.
func (e *Encoder) encodeMap(v map[any]any) any {
	n := e.take(len(v))

	if e.objectKeys(v) {
		out := make(map[string]any, n)

		for key, value := range v {
			if len(out) == n {
				break
			}

			out[e.objectKey(key.(string))] = e.Encode(value) //nolint:forcetypeassert // checked by objectKeys
		}

		return out
	}

	out := make([]Pair, 0, n)

	for key, value := range v {
		if len(out) == n {
			break
		}

		out = append(out, Pair{Key: e.Encode(key), Value: e.Encode(value)})
	}

	return out
}

func (e *Encoder) objectKeys(v map[any]any) bool {
	for key := range v {
		s, ok := key.(string)
		if !ok || (e.encoding == EncodingAuto && !utf8.ValidString(s)) {
			return false
		}
	}

	return true
}

func (e *Encoder) objectKey(s string) string {
	if key, ok := e.String(s).(string); ok {
		return key
	}

	return s
}
