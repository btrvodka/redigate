package webui

import (
	"cmp"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/btrvodka/redigate/internal/auth"
	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/service"
)

const keysPageSize = 100

// --- overview ---

type nodeRow struct {
	Addr      string
	Role      string
	OK        bool
	LatencyMS float64
	Error     string
	Version   string
	Memory    string
	Clients   string
	Keys      string
}

type overviewData struct {
	Topology       *service.TopologyInfo
	Nodes          []nodeRow
	TotalKeys      int64
	SentinelMaster string
}

func (ui *UI) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	topology, err := ui.svc.Topology(ctx)
	if err != nil {
		ui.render(w, r, statusOf(err), "overview", "page", withError(ui.page(r, "Overview", "overview", nil), err))

		return
	}

	ping, err := ui.svc.Ping(ctx)
	if err != nil {
		ui.render(w, r, statusOf(err), "overview", "page", withError(ui.page(r, "Overview", "overview", nil), err))

		return
	}

	info := make(map[string]map[string]map[string]any)

	if res, err := ui.svc.Info(ctx, service.NodeSelector{}, []string{"server", "memory", "clients", "keyspace"}); err == nil {
		for _, node := range res.Nodes {
			sections, _ := node.Value.(map[string]map[string]any)
			info[node.Addr] = sections
		}
	}

	data := overviewData{Topology: topology}

	for _, node := range ping.Nodes {
		row := nodeRow{Addr: node.Addr, Role: node.Role, OK: node.OK, LatencyMS: node.LatencyMS, Error: node.Error}

		if sections, ok := info[node.Addr]; ok {
			row.Version = str(sections["server"]["redis_version"])
			row.Memory = str(sections["memory"]["used_memory_human"])
			row.Clients = str(sections["clients"]["connected_clients"])
			row.Keys = keyspaceKeys(sections["keyspace"])
		}

		data.Nodes = append(data.Nodes, row)
	}

	if size, err := ui.svc.DBSize(ctx, service.NodeSelector{}); err == nil {
		data.TotalKeys = size.Total
	}

	ui.render(w, r, http.StatusOK, "overview", "page", ui.page(r, "Overview", "overview", data))
}

// keyspaceKeys sums keys over databases: db0:keys=1,expires=0,avg_ttl=0.
func keyspaceKeys(keyspace map[string]any) string {
	var total int64

	for _, db := range keyspace {
		if fields, ok := db.(map[string]string); ok {
			n, _ := strconv.ParseInt(fields["keys"], 10, 64)
			total += n
		}
	}

	return strconv.FormatInt(total, 10)
}

func str(v any) string {
	if v == nil {
		return ""
	}

	return fmt.Sprint(v)
}

func withError(data pageData, err error) pageData {
	data.Error = err.Error()

	return data
}

// --- keys ---

type keysFilter struct {
	Match string
	Type  string
	DB    string
}

// Query is the filter as URL query parameters.
func (f keysFilter) Query() string {
	values := make([]string, 0, 3) //nolint:mnd // three filters
	values = append(values, "match="+urlEscape(f.Match), "type="+urlEscape(f.Type))

	if f.DB != "" {
		values = append(values, "db="+urlEscape(f.DB))
	}

	return strings.Join(values, "&")
}

func filterOf(r *http.Request) keysFilter {
	q := r.URL.Query()

	return keysFilter{Match: q.Get("match"), Type: q.Get("type"), DB: q.Get("db")}
}

func dbOf(r *http.Request) (*int, error) {
	raw := r.URL.Query().Get("db")
	if raw == "" {
		raw = r.PostFormValue("db")
	}

	if raw == "" {
		return nil, nil //nolint:nilnil // no db means the default one
	}

	db, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid db %q", service.ErrInvalidRequest, raw)
	}

	return &db, nil
}

type keysPage struct {
	Filter keysFilter
	Types  []string
}

func (ui *UI) keysPage(w http.ResponseWriter, r *http.Request) {
	data := keysPage{
		Filter: filterOf(r),
		Types:  []string{"string", "hash", "list", "set", "zset", "stream", "ReJSON-RL", "TSDB-TYPE", "vectorset"},
	}

	ui.render(w, r, http.StatusOK, "keys", "page", ui.page(r, "Keys", "keys", data))
}

type keyRow struct {
	ID      string
	Param   string
	Display string
	Binary  bool
	Type    string
	TTL     string
}

type keyRowsData struct {
	First    bool
	Rows     []keyRow
	Next     string
	Filter   keysFilter
	ReadOnly bool
}

func (ui *UI) keyRows(w http.ResponseWriter, r *http.Request) {
	db, err := dbOf(r)
	if err != nil {
		ui.fail(w, r, err)

		return
	}

	filter := filterOf(r)

	res, err := ui.svc.ScanKeys(r.Context(), service.ScanOptions{
		Match:    filter.Match,
		Type:     filter.Type,
		Count:    keysPageSize,
		Cursor:   r.URL.Query().Get("cursor"),
		DB:       db,
		Encoding: codec.EncodingBase64,
		WithMeta: true,
	})
	if err != nil {
		ui.fail(w, r, err)

		return
	}

	data := keyRowsData{Filter: filter, First: r.URL.Query().Get("cursor") == "", ReadOnly: accessOf(r) < auth.Full}

	for _, item := range res.Keys {
		data.Rows = append(data.Rows, rowOf(item))
	}

	if res.Cursor != "0" {
		data.Next = res.Cursor
	}

	ui.render(w, r, http.StatusOK, "keys", "rows", data)
}

// rowOf converts a scan entry with a base64-encoded key into a table row.
func rowOf(item any) keyRow {
	brief, _ := item.(service.KeyBrief)
	encoded, _ := brief.Key.(string)
	raw, _ := base64.StdEncoding.DecodeString(encoded)
	display, binary := displayKey(raw)

	return keyRow{
		ID:      rowID(raw),
		Param:   base64.RawURLEncoding.EncodeToString(raw),
		Display: display,
		Binary:  binary,
		Type:    brief.Type,
		TTL:     formatTTL(brief.TTLMS),
	}
}

// keyOf decodes the key parameter: keys travel base64url-encoded, so binary keys work.
func keyOf(r *http.Request) (string, error) {
	raw := r.URL.Query().Get("k")
	if raw == "" {
		raw = r.PostFormValue("k")
	}

	key, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || raw == "" {
		return "", fmt.Errorf("%w: invalid key", service.ErrInvalidRequest)
	}

	return string(key), nil
}

type keyViewData struct {
	Key      keyRow
	Value    *service.KeyValue
	Kind     string
	Pairs    []pair
	Items    []string
	Scored   []pair
	Entries  []streamEntry
	Pretty   string
	ReadOnly bool
	DB       string
	Message  string
}

type pair struct {
	Name  string
	Value string
}

type streamEntry struct {
	ID     string
	Fields []pair
}

func (ui *UI) keyView(w http.ResponseWriter, r *http.Request) {
	key, err := keyOf(r)
	if err != nil {
		ui.fail(w, r, err)

		return
	}

	ui.renderKey(w, r, key, "")
}

func (ui *UI) renderKey(w http.ResponseWriter, r *http.Request, key, message string) {
	db, err := dbOf(r)
	if err != nil {
		ui.fail(w, r, err)

		return
	}

	value, err := ui.svc.KeyValue(r.Context(), key, db, codec.EncodingAuto)
	if errors.Is(err, redis.Nil) {
		ui.render(w, r, http.StatusOK, "keys", "gone", "The key does not exist.")

		return
	}

	if err != nil {
		ui.fail(w, r, err)

		return
	}

	display, binary := displayKey([]byte(key))
	data := keyViewData{
		Key:      keyRow{ID: rowID([]byte(key)), Param: base64.RawURLEncoding.EncodeToString([]byte(key)), Display: display, Binary: binary, Type: value.Type, TTL: formatTTL(value.TTLMS)},
		Value:    value,
		ReadOnly: accessOf(r) < auth.Full,
		DB:       r.URL.Query().Get("db"),
		Message:  message,
	}

	fillValue(&data, value)
	ui.render(w, r, http.StatusOK, "keys", "key", data)
}

// fillValue prepares the value for the template according to the type.
func fillValue(data *keyViewData, value *service.KeyValue) {
	switch value.Type {
	case "string":
		data.Kind = "string"
		data.Items = []string{text(value.Value)}
	case "list", "set":
		data.Kind = "items"

		items, _ := value.Value.([]any)
		for _, item := range items {
			data.Items = append(data.Items, text(item))
		}
	case "hash":
		data.Kind = "pairs"
		data.Pairs = pairsOf(value.Value)
	case "zset":
		data.Kind = "scored"

		items, _ := value.Value.([]any)
		for _, item := range items {
			m, _ := item.(map[string]any)
			data.Scored = append(data.Scored, pair{Name: text(m["member"]), Value: text(m["score"])})
		}
	case "stream":
		data.Kind = "stream"

		items, _ := value.Value.([]any)
		for _, item := range items {
			m, _ := item.(map[string]any)
			data.Entries = append(data.Entries, streamEntry{ID: text(m["id"]), Fields: pairsOf(m["fields"])})
		}
	default:
		data.Kind = "json"
		data.Pretty = prettyJSON(value.Value)
	}
}

// pairsOf converts an encoded map (an object or a list of pairs for binary names) to sorted pairs.
func pairsOf(v any) []pair {
	var out []pair

	switch m := v.(type) {
	case map[string]any:
		for name, value := range m {
			out = append(out, pair{Name: name, Value: text(value)})
		}
	case []any:
		for _, item := range m {
			if p, ok := item.(codec.Pair); ok {
				out = append(out, pair{Name: text(p.Key), Value: text(p.Value)})
			}
		}
	case []codec.Pair:
		for _, p := range m {
			out = append(out, pair{Name: text(p.Key), Value: text(p.Value)})
		}
	}

	slices.SortFunc(out, func(a, b pair) int { return cmp.Compare(a.Name, b.Name) })

	return out
}

func (ui *UI) deleteKey(w http.ResponseWriter, r *http.Request) {
	key, err := keyOf(r)
	if err != nil {
		ui.fail(w, r, err)

		return
	}

	if err := ui.exec(r, "del", key); err != nil {
		ui.fail(w, r, err)

		return
	}

	ui.render(w, r, http.StatusOK, "keys", "deleted", rowID([]byte(key)))
}

func (ui *UI) setTTL(w http.ResponseWriter, r *http.Request) {
	key, err := keyOf(r)
	if err != nil {
		ui.fail(w, r, err)

		return
	}

	raw := strings.TrimSpace(r.PostFormValue("ttl"))

	if raw == "" || raw == "0" {
		err = ui.exec(r, "persist", key)
	} else {
		var ttl time.Duration
		if ttl, err = parseTTL(raw); err == nil {
			err = ui.exec(r, "pexpire", key, ttl.Milliseconds())
		}
	}

	if err != nil {
		ui.fail(w, r, err)

		return
	}

	ui.renderKey(w, r, key, "TTL updated.")
}

// parseTTL accepts seconds ("60") or a duration ("1h30m").
func parseTTL(raw string) (time.Duration, error) {
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second, nil
	}

	ttl, err := time.ParseDuration(raw)
	if err != nil || ttl <= 0 {
		return 0, fmt.Errorf("%w: TTL must be seconds or a duration like 1h30m", service.ErrInvalidRequest)
	}

	return ttl, nil
}

// exec runs a command through the service with the access of the request.
func (ui *UI) exec(r *http.Request, args ...any) error {
	db, err := dbOf(r)
	if err != nil {
		return err
	}

	_, err = ui.svc.Exec(r.Context(), codec.Args(args), service.ExecOptions{
		Target:   service.TargetAuto,
		DB:       db,
		Encoding: codec.EncodingAuto,
		ReadOnly: accessOf(r) < auth.Full,
	})

	return err //nolint:wrapcheck // rendered as is
}

// --- console ---

type consoleData struct {
	Nodes []service.NodeInfo
}

func (ui *UI) consolePage(w http.ResponseWriter, r *http.Request) {
	var data consoleData

	if topology, err := ui.svc.Topology(r.Context()); err == nil {
		data.Nodes = topology.Nodes
	}

	ui.render(w, r, http.StatusOK, "console", "page", ui.page(r, "Console", "console", data))
}

type consoleResult struct {
	Command  string
	Where    string
	Duration string
	Output   string
	Error    string
}

func (ui *UI) runCommand(w http.ResponseWriter, r *http.Request) {
	line := strings.TrimSpace(r.PostFormValue("command"))
	result := consoleResult{Command: line, Where: where(r)}

	start := time.Now()
	output, err := ui.runLine(r, line)
	result.Duration = time.Since(start).Round(time.Microsecond).String()

	if err != nil {
		result.Error = err.Error()
	} else {
		result.Output = output
	}

	ui.render(w, r, http.StatusOK, "console", "result", result)
}

func (ui *UI) runLine(r *http.Request, line string) (string, error) {
	args, err := codec.SplitArgs(line)
	if err != nil {
		return "", fmt.Errorf("%w: %v", service.ErrInvalidRequest, err) //nolint:errorlint // message only
	}

	target, err := service.ParseTarget(r.PostFormValue("target"))
	if err != nil {
		return "", err //nolint:wrapcheck // rendered as is
	}

	db, err := dbOf(r)
	if err != nil {
		return "", err
	}

	opts := service.ExecOptions{
		Target:   target,
		Node:     r.PostFormValue("node"),
		DB:       db,
		Encoding: codec.EncodingAuto,
		ReadOnly: accessOf(r) < auth.Full,
	}

	if opts.Node != "" {
		opts.Target = service.TargetNode
	}

	result, err := ui.svc.Exec(r.Context(), args, opts)
	if err != nil {
		return "", err //nolint:wrapcheck // rendered as is
	}

	if single, ok := result.(*service.CommandResult); ok {
		// Strings are shown as they are, like redis-cli does: INFO stays readable.
		switch value := single.Value.(type) {
		case string, codec.Binary, nil:
			return text(value), nil
		default:
			return prettyJSON(value), nil
		}
	}

	return prettyJSON(result), nil
}

func where(r *http.Request) string {
	parts := make([]string, 0, 3) //nolint:mnd // target, node and db

	if node := r.PostFormValue("node"); node != "" {
		parts = append(parts, node)
	} else if target := r.PostFormValue("target"); target != "" && target != "auto" {
		parts = append(parts, target)
	}

	if db := r.PostFormValue("db"); db != "" {
		parts = append(parts, "db "+db)
	}

	return strings.Join(parts, ", ")
}
