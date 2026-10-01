package service

import (
	"fmt"
	"strconv"
	"strings"
)

// parseInfo parses INFO into sections. Values in the "k1=v1,k2=v2" form
// (keyspace, replicas, command stats) become objects.
func parseInfo(reply any) (any, error) {
	raw, ok := reply.(string)
	if !ok {
		return nil, fmt.Errorf("unexpected INFO reply %T", reply)
	}

	sections := make(map[string]map[string]any)
	section := "default"

	for line := range strings.Lines(raw) {
		line = strings.TrimSpace(line)

		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#"):
			section = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "#")))

			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		if sections[section] == nil {
			sections[section] = make(map[string]any)
		}

		if fields, ok := parseFields(value, ",", "="); ok {
			sections[section][key] = fields
		} else {
			sections[section][key] = value
		}
	}

	return sections, nil
}

// parseFields parses "k1=v1<sep>k2=v2". It fails if any part has no kv separator.
func parseFields(s, sep, kvSep string) (map[string]string, bool) {
	if !strings.Contains(s, kvSep) {
		return nil, false
	}

	fields := make(map[string]string)

	for part := range strings.SplitSeq(s, sep) {
		if part == "" {
			continue
		}

		k, v, ok := strings.Cut(part, kvSep)
		if !ok {
			return nil, false
		}

		fields[k] = v
	}

	return fields, true
}

// parseClientList parses CLIENT LIST: one "k=v k=v" line per client.
func parseClientList(reply any) (any, error) {
	raw, ok := reply.(string)
	if !ok {
		return nil, fmt.Errorf("unexpected CLIENT LIST reply %T", reply)
	}

	clients := make([]map[string]string, 0)

	for line := range strings.Lines(raw) {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}

		client := make(map[string]string)

		for field := range strings.FieldsSeq(line) {
			k, v, _ := strings.Cut(field, "=")
			client[k] = v
		}

		clients = append(clients, client)
	}

	return clients, nil
}

// parseStringMap parses a RESP3 map or a RESP2 flat key-value array (CONFIG GET, CLUSTER INFO-like replies).
func parseStringMap(reply any) (any, error) {
	result := make(map[string]string)

	switch v := reply.(type) {
	case []any:
		for i := 0; i+1 < len(v); i += 2 {
			result[fmt.Sprint(v[i])] = fmt.Sprint(v[i+1])
		}
	case map[any]any:
		for key, value := range v {
			result[fmt.Sprint(key)] = fmt.Sprint(value)
		}
	default:
		return nil, fmt.Errorf("unexpected reply %T", reply)
	}

	return result, nil
}

// parseColonLines parses "key:value" lines, e.g. CLUSTER INFO.
func parseColonLines(reply any) (any, error) {
	raw, ok := reply.(string)
	if !ok {
		return nil, fmt.Errorf("unexpected reply %T", reply)
	}

	result := make(map[string]string)

	for line := range strings.Lines(raw) {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
			result[k] = v
		}
	}

	return result, nil
}

type SlowlogEntry struct {
	ID         int64    `json:"id"`
	Timestamp  int64    `json:"timestamp"`
	DurationUS int64    `json:"duration_us"`
	Args       []string `json:"args"`
	ClientAddr string   `json:"client_addr,omitempty"`
	ClientName string   `json:"client_name,omitempty"`
}

// parseSlowlog parses SLOWLOG GET: [id, timestamp, duration, args, client addr, client name].
func parseSlowlog(reply any) (any, error) {
	items, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected SLOWLOG reply %T", reply)
	}

	entries := make([]SlowlogEntry, 0, len(items))

	for _, item := range items {
		fields, ok := item.([]any)
		if !ok || len(fields) < 4 { //nolint:mnd // mandatory fields
			return nil, fmt.Errorf("unexpected SLOWLOG entry %v", item)
		}

		entry := SlowlogEntry{
			ID:         toInt64(fields[0]),
			Timestamp:  toInt64(fields[1]),
			DurationUS: toInt64(fields[2]),
		}

		args, _ := fields[3].([]any)
		for _, arg := range args {
			entry.Args = append(entry.Args, fmt.Sprint(arg))
		}

		if len(fields) > 5 { //nolint:mnd // client fields since redis 4
			entry.ClientAddr = fmt.Sprint(fields[4])
			entry.ClientName = fmt.Sprint(fields[5])
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

type ClusterNode struct {
	ID          string   `json:"id"`
	Addr        string   `json:"addr"`
	BusPort     int      `json:"bus_port,omitempty"`
	Hostname    string   `json:"hostname,omitempty"`
	Flags       []string `json:"flags"`
	Role        string   `json:"role"`
	MasterID    string   `json:"master_id,omitempty"`
	PingSent    int64    `json:"ping_sent"`
	PongRecv    int64    `json:"pong_recv"`
	ConfigEpoch int64    `json:"config_epoch"`
	LinkState   string   `json:"link_state"`
	Slots       []string `json:"slots"`
}

// parseClusterNodes parses CLUSTER NODES:
// <id> <ip:port@cport[,hostname]> <flags> <master> <ping-sent> <pong-recv> <config-epoch> <link-state> <slot>...
func parseClusterNodes(reply any) (any, error) {
	raw, ok := reply.(string)
	if !ok {
		return nil, fmt.Errorf("unexpected CLUSTER NODES reply %T", reply)
	}

	nodes := make([]ClusterNode, 0)

	for line := range strings.Lines(raw) {
		fields := strings.Fields(line)
		if len(fields) < 8 { //nolint:mnd // mandatory fields
			continue
		}

		addr, hostname, _ := strings.Cut(fields[1], ",")
		addr, busPort, _ := strings.Cut(addr, "@")

		node := ClusterNode{
			ID:          fields[0],
			Addr:        addr,
			Hostname:    hostname,
			Flags:       strings.Split(fields[2], ","),
			Role:        "master",
			PingSent:    parseInt64(fields[4]),
			PongRecv:    parseInt64(fields[5]),
			ConfigEpoch: parseInt64(fields[6]),
			LinkState:   fields[7],
			Slots:       append([]string{}, fields[8:]...),
		}

		node.BusPort, _ = strconv.Atoi(busPort)

		if fields[3] != "-" {
			node.MasterID = fields[3]
		}

		for _, flag := range node.Flags {
			if flag == "slave" || flag == "replica" {
				node.Role = "replica"
			}
		}

		nodes = append(nodes, node)
	}

	return nodes, nil
}

func toInt64(v any) int64 {
	switch v := v.(type) {
	case int64:
		return v
	case string:
		return parseInt64(v)
	default:
		return 0
	}
}

func parseInt64(s string) int64 {
	i, _ := strconv.ParseInt(s, 10, 64)

	return i
}
