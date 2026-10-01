package service

import (
	"testing"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/redisx"
)

func TestReadOnlyAllowed(t *testing.T) {
	t.Parallel()

	spec := func(flags, acl []string) *redisx.CommandSpec {
		return &redisx.CommandSpec{Flags: flags, ACLCategories: acl}
	}

	// Flags and ACL categories as reported by redis 7.
	tests := []struct {
		args codec.Args
		spec *redisx.CommandSpec
		want bool
	}{
		{codec.Args{"GET", "k"}, spec([]string{"readonly", "fast"}, []string{"@read", "@string", "@fast"}), true},
		{codec.Args{"KEYS", "*"}, spec([]string{"readonly"}, []string{"@keyspace", "@read", "@slow", "@dangerous"}), true},
		{codec.Args{"INFO"}, spec([]string{"loading", "stale"}, []string{"@slow", "@dangerous"}), true},
		{codec.Args{"CONFIG", "GET", "*"}, spec([]string{"admin", "noscript", "loading", "stale"}, []string{"@admin", "@slow", "@dangerous"}), true},
		{codec.Args{"client", "list"}, spec([]string{"admin", "noscript", "loading", "stale"}, []string{"@admin", "@slow", "@dangerous", "@connection"}), true},
		{codec.Args{"EVAL_RO", "return 1", "0"}, spec([]string{"readonly", "noscript", "movablekeys"}, []string{"@slow", "@scripting"}), true},
		{codec.Args{"SET", "k", "v"}, spec([]string{"write", "denyoom"}, []string{"@write", "@string", "@slow"}), false},
		{codec.Args{"CONFIG", "SET", "x", "y"}, spec([]string{"admin", "noscript", "loading", "stale"}, []string{"@admin", "@slow", "@dangerous"}), false},
		{codec.Args{"CLIENT", "KILL", "ID", "1"}, spec([]string{"admin", "noscript", "loading", "stale"}, []string{"@admin", "@slow", "@dangerous", "@connection"}), false},
		{codec.Args{"FLUSHALL"}, spec([]string{"write"}, []string{"@keyspace", "@write", "@slow", "@dangerous"}), false},
		{codec.Args{"EVAL", "return 1", "0"}, spec([]string{"noscript", "may_replicate", "movablekeys"}, []string{"@slow", "@scripting"}), false},
		{codec.Args{"FCALL", "f", "0"}, spec([]string{"noscript", "may_replicate", "movablekeys"}, []string{"@slow", "@scripting"}), false},
		{codec.Args{"PUBLISH", "ch", "m"}, spec([]string{"pubsub", "loading", "stale", "fast"}, []string{"@pubsub", "@fast"}), false},
		{codec.Args{"SCRIPT", "FLUSH"}, spec([]string{"noscript"}, []string{"@slow", "@scripting"}), false},
		{codec.Args{"SHUTDOWN"}, spec([]string{"admin", "noscript", "loading", "stale"}, []string{"@admin", "@slow", "@dangerous"}), false},
		{codec.Args{"UNKNOWN"}, nil, false},
	}

	for _, tt := range tests {
		if got := readOnlyAllowed(tt.spec, tt.args); got != tt.want {
			t.Errorf("readOnlyAllowed(%v) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestSameSlot(t *testing.T) {
	t.Parallel()

	if err := sameSlot(nil); err != nil {
		t.Errorf("no keys: %v", err)
	}

	if err := sameSlot([]string{"{u}a", "{u}b", "u"}); err != nil {
		t.Errorf("same slot: %v", err)
	}

	if err := sameSlot([]string{"foo", "bar"}); err == nil {
		t.Error("different slots must fail")
	}
}
