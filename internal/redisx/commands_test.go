package redisx

import (
	"reflect"
	"testing"
)

func TestSlot(t *testing.T) {
	t.Parallel()

	// Values from the redis cluster specification and CLUSTER KEYSLOT.
	tests := map[string]int{
		"foo":                  12182,
		"bar":                  5061,
		"123456789":            12739,
		"{user1000}.following": Slot("user1000"),
		"foo{}{bar}":           Slot("foo{}{bar}"),
		"foo{{bar}}zap":        Slot("{bar"),
	}

	for key, want := range tests {
		if got := Slot(key); got != want {
			t.Errorf("Slot(%q) = %d, want %d", key, got, want)
		}
	}

	if Slot("{user1000}.following") != Slot("{user1000}.followers") {
		t.Error("hash tags must map keys to the same slot")
	}
}

func TestParseCommandSpec(t *testing.T) {
	t.Parallel()

	// A redis 7 entry with a subcommand.
	entry := []any{
		"config", int64(-2),
		[]any{},
		int64(0), int64(0), int64(0),
		[]any{"@slow"},
		[]any{},
		[]any{},
		[]any{
			[]any{"config|set", int64(-4), []any{"admin", "noscript"}, int64(0), int64(0), int64(0), []any{"@admin", "@dangerous"}},
		},
	}

	spec, err := parseCommandSpec(entry)
	if err != nil {
		t.Fatal(err)
	}

	sub := spec.Subcommands["set"]
	if spec.Name != "config" || sub == nil || sub.FullName != "config|set" || !sub.HasFlag("admin") || !sub.HasACLCategory("@admin") {
		t.Fatalf("unexpected spec %+v, subcommand %+v", spec, sub)
	}

	// A redis 5 entry: only 6 fields.
	mset, err := parseCommandSpec([]any{"mset", int64(-3), []any{"write", "denyoom"}, int64(1), int64(-1), int64(2)})
	if err != nil {
		t.Fatal(err)
	}

	if got := mset.KeyIndexes(5); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Errorf("MSET key indexes = %v, want [1 3]", got)
	}

	if _, err := parseCommandSpec([]any{"broken"}); err == nil {
		t.Error("expected error for a short entry")
	}
}
