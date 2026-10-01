package service

import (
	"reflect"
	"testing"
)

func TestParseInfo(t *testing.T) {
	t.Parallel()

	raw := "# Server\r\nredis_version:7.4.1\r\nexecutable:/usr/bin/redis-server\r\n\r\n" +
		"# Replication\r\nrole:master\r\nslave0:ip=10.0.0.2,port=6379,state=online,offset=42,lag=0\r\n\r\n" +
		"# Keyspace\r\ndb0:keys=3,expires=1,avg_ttl=0\r\n"

	got, err := parseInfo(raw)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]map[string]any{
		"server":      {"redis_version": "7.4.1", "executable": "/usr/bin/redis-server"},
		"replication": {"role": "master", "slave0": map[string]string{"ip": "10.0.0.2", "port": "6379", "state": "online", "offset": "42", "lag": "0"}},
		"keyspace":    {"db0": map[string]string{"keys": "3", "expires": "1", "avg_ttl": "0"}},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseInfo() = %#v", got)
	}
}

func TestParseClusterNodes(t *testing.T) {
	t.Parallel()

	raw := "07c37dfeb235213a872192d90877d0cd55635b91 127.0.0.1:30004@31004,host-4 slave e7d1eecce10fd6bb5eb35b9f99a514335d9ba9ca 0 1426238317239 4 connected\n" +
		"e7d1eecce10fd6bb5eb35b9f99a514335d9ba9ca 127.0.0.1:30001@31001 myself,master - 0 0 1 connected 0-5460 6000 [5461->-292f8b365bb7edb5e285caf0b7e6ddc7265d2f4f]\n"

	got, err := parseClusterNodes(raw)
	if err != nil {
		t.Fatal(err)
	}

	want := []ClusterNode{
		{
			ID: "07c37dfeb235213a872192d90877d0cd55635b91", Addr: "127.0.0.1:30004", BusPort: 31004, Hostname: "host-4",
			Flags: []string{"slave"}, Role: "replica", MasterID: "e7d1eecce10fd6bb5eb35b9f99a514335d9ba9ca",
			PongRecv: 1426238317239, ConfigEpoch: 4, LinkState: "connected", Slots: []string{},
		},
		{
			ID: "e7d1eecce10fd6bb5eb35b9f99a514335d9ba9ca", Addr: "127.0.0.1:30001", BusPort: 31001,
			Flags: []string{"myself", "master"}, Role: "master", ConfigEpoch: 1, LinkState: "connected",
			Slots: []string{"0-5460", "6000", "[5461->-292f8b365bb7edb5e285caf0b7e6ddc7265d2f4f]"},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseClusterNodes() =\n%#v\nwant\n%#v", got, want)
	}

	slots := want[1].Slots
	for slot, in := range map[int]bool{0: true, 5460: true, 6000: true, 5461: false, 5999: false, 16383: false} {
		if slotInRanges(slot, slots) != in {
			t.Errorf("slotInRanges(%d) = %v", slot, !in)
		}
	}
}

func TestParseClientListAndSlowlog(t *testing.T) {
	t.Parallel()

	clients, err := parseClientList("id=3 addr=127.0.0.1:5000 name= cmd=client|list\nid=4 addr=127.0.0.1:5001 name=app cmd=get\n")
	if err != nil {
		t.Fatal(err)
	}

	want := []map[string]string{
		{"id": "3", "addr": "127.0.0.1:5000", "name": "", "cmd": "client|list"},
		{"id": "4", "addr": "127.0.0.1:5001", "name": "app", "cmd": "get"},
	}
	if !reflect.DeepEqual(clients, want) {
		t.Errorf("parseClientList() = %#v", clients)
	}

	entries, err := parseSlowlog([]any{
		[]any{int64(1), int64(1700000000), int64(15000), []any{"KEYS", "*"}, "127.0.0.1:5000", "app"},
	})
	if err != nil {
		t.Fatal(err)
	}

	wantEntries := []SlowlogEntry{{ID: 1, Timestamp: 1700000000, DurationUS: 15000, Args: []string{"KEYS", "*"}, ClientAddr: "127.0.0.1:5000", ClientName: "app"}}
	if !reflect.DeepEqual(entries, wantEntries) {
		t.Errorf("parseSlowlog() = %#v", entries)
	}
}
