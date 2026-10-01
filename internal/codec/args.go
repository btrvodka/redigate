package codec

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Args are command arguments: every element is either a string or a []byte.
type Args []any

// UnmarshalJSON accepts an array of strings, numbers and {"base64": "..."} objects.
func (a *Args) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return errors.New("arguments must be an array")
	}

	args := make(Args, 0, len(raw))

	for i, item := range raw {
		arg, err := decodeArg(item)
		if err != nil {
			return fmt.Errorf("argument %d: %w", i, err)
		}

		args = append(args, arg)
	}

	*a = args

	return nil
}

func decodeArg(raw json.RawMessage) (any, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("empty value")
	}

	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("invalid string: %w", err)
		}

		return s, nil
	case '{':
		var bin struct {
			Base64 *string `json:"base64"`
		}

		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()

		if err := dec.Decode(&bin); err != nil || bin.Base64 == nil {
			return nil, errors.New(`object arguments must look like {"base64": "..."}`)
		}

		b, err := base64.StdEncoding.DecodeString(*bin.Base64)
		if err != nil {
			return nil, fmt.Errorf("invalid base64: %w", err)
		}

		return b, nil
	case 'n', 't', 'f', '[':
		return nil, errors.New("only strings, numbers and base64 objects are allowed")
	default:
		var n json.Number
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("invalid number: %w", err)
		}

		return n.String(), nil
	}
}

// Arg is a single binary-safe argument in a JSON body: a string, a number or {"base64": "..."}.
// The underlying string holds raw bytes.
type Arg string

func (a *Arg) UnmarshalJSON(data []byte) error {
	value, err := decodeArg(data)
	if err != nil {
		return err
	}

	switch v := value.(type) {
	case string:
		*a = Arg(v)
	case []byte:
		*a = Arg(v)
	}

	return nil
}

// Strings converts arguments to a slice usable as command arguments.
func Strings[T ~string](items []T) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = string(item)
	}

	return out
}

// String returns the i-th argument as a string or "" if it does not exist.
func (a Args) String(i int) string {
	if i < 0 || i >= len(a) {
		return ""
	}

	switch v := a[i].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

// Name returns the lower-cased command name.
func (a Args) Name() string {
	return strings.ToLower(a.String(0))
}

// SplitArgs splits a command line the way redis-cli does: arguments are separated
// by spaces, "double quotes" support \n \r \t \b \a \\ \" and \xHH escapes,
// 'single quotes' support only \'.
//
//nolint:cyclop,gocognit // a small state machine
func SplitArgs(line string) (Args, error) {
	var args Args

	i := 0
	for {
		for i < len(line) && isSpace(line[i]) {
			i++
		}

		if i == len(line) {
			return args, nil
		}

		var (
			current  []byte
			inDouble bool
			inSingle bool
			done     bool
		)

		for !done {
			if i == len(line) {
				if inDouble || inSingle {
					return nil, errors.New("unbalanced quotes")
				}

				break
			}

			c := line[i]

			switch {
			case inDouble:
				switch {
				case c == '\\' && i+3 < len(line) && line[i+1] == 'x' && isHex(line[i+2]) && isHex(line[i+3]):
					b, _ := strconv.ParseUint(line[i+2:i+4], 16, 8)
					current = append(current, byte(b))
					i += 3
				case c == '\\' && i+1 < len(line):
					i++
					current = append(current, unescape(line[i]))
				case c == '"':
					if i+1 < len(line) && !isSpace(line[i+1]) {
						return nil, errors.New("closing quote must be followed by a space")
					}

					done = true
				default:
					current = append(current, c)
				}
			case inSingle:
				switch {
				case c == '\\' && i+1 < len(line) && line[i+1] == '\'':
					i++

					current = append(current, '\'')
				case c == '\'':
					if i+1 < len(line) && !isSpace(line[i+1]) {
						return nil, errors.New("closing quote must be followed by a space")
					}

					done = true
				default:
					current = append(current, c)
				}
			default:
				switch c {
				case ' ', '\n', '\r', '\t', 0:
					done = true
				case '"':
					inDouble = true
				case '\'':
					inSingle = true
				default:
					current = append(current, c)
				}
			}

			if i < len(line) {
				i++
			}
		}

		args = append(args, string(current))
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == 0
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func unescape(c byte) byte {
	switch c {
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	case 'b':
		return '\b'
	case 'a':
		return '\a'
	default:
		return c
	}
}
