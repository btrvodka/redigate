package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/btrvodka/redigate/internal/auth"
	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/service"
)

// call runs a command built by a typed endpoint through the same path as /command:
// read-only policy, db, node, target and encoding query parameters apply.
func (s *Server) call(r *http.Request, shape func(any) any, args ...any) (any, error) {
	return s.callWith(r, func(opts *service.ExecOptions) { opts.Shape = shape }, args...)
}

// callWith is call with a custom adjustment of options.
func (s *Server) callWith(r *http.Request, adjust func(*service.ExecOptions), args ...any) (any, error) {
	opts, err := execOptions(r, execParams{})
	if err != nil {
		return nil, err
	}

	adjust(&opts)

	return s.svc.Exec(r.Context(), codec.Args(args), opts)
}

// query reads typed query parameters and remembers the first error.
type query struct {
	r   *http.Request
	err error
}

func newQuery(r *http.Request) *query {
	return &query{r: r}
}

func (q *query) fail(format string, args ...any) {
	if q.err == nil {
		q.err = badRequest(format, args...)
	}
}

// binary decodes a binary-safe parameter: with key_encoding=base64 values are base64.
func (q *query) binary(name, value string) string {
	if q.r.URL.Query().Get("key_encoding") != "base64" {
		return value
	}

	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		q.fail("invalid base64 in %s", name)
	}

	return string(decoded)
}

// key returns a required binary-safe parameter.
func (q *query) key(name string) string {
	values := q.keys(name)
	if len(values) == 0 {
		return ""
	}

	if len(values) > 1 {
		q.fail("%s must be given once", name)
	}

	return values[0]
}

// keys returns a required repeated binary-safe parameter: ?key=a&key=b.
func (q *query) keys(name string) []string {
	raw, ok := q.r.URL.Query()[name]
	if !ok || len(raw) == 0 {
		q.fail("%s is required", name)

		return nil
	}

	values := make([]string, len(raw))
	for i, value := range raw {
		values[i] = q.binary(name, value)
	}

	return values
}

func (q *query) str(name, def string) string {
	if value := q.r.URL.Query().Get(name); value != "" {
		return value
	}

	return def
}

func (q *query) int(name string, def int64) int64 {
	raw := q.r.URL.Query().Get(name)
	if raw == "" {
		return def
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		q.fail("invalid %s %q", name, raw)
	}

	return value
}

func (q *query) bool(name string) bool {
	if !q.r.URL.Query().Has(name) {
		return false
	}

	raw := q.r.URL.Query().Get(name)
	if raw == "" {
		return true
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		q.fail("invalid %s %q", name, raw)
	}

	return value
}

func (q *query) db() *int {
	if !q.r.URL.Query().Has("db") {
		return nil
	}

	db := int(q.int("db", 0))

	return &db
}

func (q *query) encoding() codec.Encoding {
	enc, err := codec.ParseEncoding(q.r.URL.Query().Get("encoding"))
	if err != nil {
		q.fail("%v", err)
	}

	return enc
}

// bindJSON decodes a required JSON body.
func bindJSON(r *http.Request, v any) error {
	body, _, err := readBody(r)
	if err != nil {
		return err
	}

	if strings.TrimSpace(string(body)) == "" {
		return badRequest("JSON body is required")
	}

	return decodeJSON(body, v)
}

// required checks that binary-safe body fields are set, names are checked in order.
func required(names []string, values ...codec.Arg) error {
	for i, value := range values {
		if value == "" {
			return badRequest("%s is required", names[i])
		}
	}

	return nil
}

// readOnlyRequest reports whether the request has read-only access.
func readOnlyRequest(r *http.Request) bool {
	return accessFromContext(r.Context()) < auth.Full
}

// flag returns the argument when the condition is true.
func flag(cond bool, args ...any) []any {
	if cond {
		return args
	}

	return nil
}

// cmd joins command parts: values and slices of values.
func cmd(parts ...any) []any {
	var out []any

	for _, part := range parts {
		switch v := part.(type) {
		case []any:
			out = append(out, v...)
		case codec.Arg:
			out = append(out, string(v))
		case []codec.Arg:
			out = append(out, codec.Strings(v)...)
		case []string:
			out = append(out, codec.Strings(v)...)
		default:
			out = append(out, v)
		}
	}

	return out
}

// bodyRoute decodes a JSON body into T and runs the command built from it.
func bodyRoute[T any](s *Server, build func(r *http.Request, body *T) ([]any, func(any) any, error)) jsonHandler {
	return func(r *http.Request) (any, error) {
		var body T
		if err := bindJSON(r, &body); err != nil {
			return nil, err
		}

		args, shape, err := build(r, &body)
		if err != nil {
			return nil, err
		}

		return s.call(r, shape, args...)
	}
}

// queryRoute runs the command built from query parameters.
func queryRoute(s *Server, build func(q *query) ([]any, func(any) any)) jsonHandler {
	return func(r *http.Request) (any, error) {
		q := newQuery(r)

		args, shape := build(q)
		if q.err != nil {
			return nil, q.err
		}

		return s.call(r, shape, args...)
	}
}

// numberArg formats a JSON number for INCRBY-like commands (1 by default) and tells whether it is a float.
func numberArg(n json.Number) (string, bool) {
	s := n.String()
	if s == "" {
		s = "1"
	}

	return s, strings.ContainsAny(s, ".eE")
}

// blockSeconds converts block_ms to the seconds argument of blocking commands.
func blockSeconds(ms int64) string {
	return strconv.FormatFloat(float64(ms)/1000, 'f', -1, 64) //nolint:mnd // ms to s
}
