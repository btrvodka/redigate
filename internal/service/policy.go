package service

import (
	"fmt"
	"strings"

	"github.com/btrvodka/redigate/internal/codec"
	"github.com/btrvodka/redigate/internal/redisx"
)

type commandSet map[string]struct{}

func newCommandSet(names ...string) commandSet {
	set := make(commandSet, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}

	return set
}

// match reports whether the command or its "command|subcommand" form is in the set.
func (s commandSet) match(args codec.Args) bool {
	if _, ok := s[args.Name()]; ok {
		return true
	}

	if len(args) < 2 { //nolint:mnd // command and subcommand
		return false
	}

	_, ok := s[args.Name()+"|"+strings.ToLower(args.String(1))]

	return ok
}

//nolint:gochecknoglobals // static command lists
var (
	// streamingCommands turn the connection into a stream and are served by dedicated endpoints.
	streamingCommands = newCommandSet(
		"subscribe", "psubscribe", "ssubscribe",
		"unsubscribe", "punsubscribe", "sunsubscribe",
		"monitor", "sync", "psync",
	)

	// sessionCommands change the state of the connection, so they run on a dedicated one.
	sessionCommands = newCommandSet(
		"select", "auth", "hello", "reset", "quit",
		"multi", "exec", "discard", "watch", "unwatch",
		"readonly", "readwrite",
		"client|reply", "client|tracking", "client|caching",
		"client|setname", "client|setinfo", "client|no-evict", "client|no-touch",
	)

	// readOnlyDenied are commands that modify server state without the write flag.
	readOnlyDenied = newCommandSet(
		"publish", "spublish",
		// Scripts may write; servers before 7.0 don't flag them with may_replicate.
		"eval", "evalsha", "fcall",
		"script|load", "script|flush", "script|kill",
		"function|kill",
	)

	// readOnlyAdmin are commands with the admin flag that only read server state.
	readOnlyAdmin = newCommandSet(
		"config|get", "config|help",
		"client|list", "client|info", "client|getname", "client|id", "client|help",
		"slowlog|get", "slowlog|len", "slowlog|help",
		"latency|latest", "latency|history", "latency|doctor", "latency|graph", "latency|histogram", "latency|help",
		"memory|stats", "memory|usage", "memory|doctor", "memory|malloc-stats", "memory|help",
		"acl|whoami", "acl|list", "acl|users", "acl|getuser", "acl|cat", "acl|log", "acl|help",
		"cluster|info", "cluster|nodes", "cluster|slots", "cluster|shards", "cluster|myid", "cluster|myshardid",
		"cluster|keyslot", "cluster|countkeysinslot", "cluster|getkeysinslot", "cluster|links",
		"cluster|replicas", "cluster|slaves", "cluster|count-failure-reports", "cluster|help",
		"module|list", "module|help",
		"lastsave", "role", "info",
		"sentinel|masters", "sentinel|master", "sentinel|replicas", "sentinel|slaves", "sentinel|sentinels",
		"sentinel|get-master-addr-by-name", "sentinel|ckquorum", "sentinel|info-cache", "sentinel|myid",
	)
)

// readOnlyAllowed reports whether a read-only client may run the command.
// Unknown commands are denied.
func readOnlyAllowed(spec *redisx.CommandSpec, args codec.Args) bool {
	if spec == nil || readOnlyDenied.match(args) {
		return false
	}

	if spec.HasFlag("write") || spec.HasFlag("may_replicate") || spec.HasACLCategory("@write") {
		return false
	}

	if spec.HasFlag("admin") && !readOnlyAdmin.match(args) {
		return false
	}

	return true
}

// checkCommand validates a command line and returns its spec, nil for unknown commands.
func (s *Service) checkCommand(args codec.Args, readOnly bool) (*redisx.CommandSpec, error) {
	if len(args) == 0 || args.Name() == "" {
		return nil, fmt.Errorf("%w: command is empty", ErrInvalidRequest)
	}

	if streamingCommands.match(args) {
		return nil, fmt.Errorf("%w: %s holds the connection open, use the streaming endpoints",
			ErrUnsupportedCommand, strings.ToUpper(args.Name()))
	}

	spec, _ := s.redis.Commands().Lookup(args.Name(), args.String(1))

	if readOnly && !readOnlyAllowed(spec, args) {
		return nil, fmt.Errorf("%w: %s is not allowed with read-only access", ErrForbidden, commandTitle(spec, args))
	}

	return spec, nil
}

func commandTitle(spec *redisx.CommandSpec, args codec.Args) string {
	if spec != nil {
		return strings.ToUpper(strings.ReplaceAll(spec.FullName, "|", " "))
	}

	return strings.ToUpper(args.Name())
}
