package webui

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/btrvodka/redigate/internal/codec"
)

//nolint:gochecknoglobals // template functions
var funcs = template.FuncMap{
	"ms": func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) },
}

func urlEscape(s string) string {
	return url.QueryEscape(s)
}

// displayKey returns a printable key and whether it is binary (shown as base64).
func displayKey(raw []byte) (string, bool) {
	if utf8.Valid(raw) {
		return string(raw), false
	}

	return base64.StdEncoding.EncodeToString(raw), true
}

// rowID is a stable HTML id of a key row.
func rowID(raw []byte) string {
	sum := sha256.Sum256(raw)

	return "key-" + hex.EncodeToString(sum[:8])
}

func formatTTL(ms int64) string {
	switch {
	case ms == -1:
		return "no TTL"
	case ms < 0:
		return "-"
	default:
		return (time.Duration(ms) * time.Millisecond).Round(time.Second).String()
	}
}

// text renders an encoded value: binary strings are marked with a base64 prefix.
func text(v any) string {
	switch v := v.(type) {
	case nil:
		return "(nil)"
	case string:
		return v
	case codec.Binary:
		return "base64:" + v.Base64
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

func prettyJSON(v any) string {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}

	return string(out)
}
