package redisx

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// CommandSpec is the metadata of a command or a subcommand as reported by COMMAND.
type CommandSpec struct {
	// Name is the command or subcommand name, FullName is "container|subcommand" for subcommands.
	Name          string
	FullName      string
	Arity         int
	Flags         []string
	FirstKey      int
	LastKey       int
	Step          int
	ACLCategories []string
	Subcommands   map[string]*CommandSpec
}

func (s *CommandSpec) HasFlag(flag string) bool {
	return slices.Contains(s.Flags, flag)
}

func (s *CommandSpec) HasACLCategory(category string) bool {
	return slices.Contains(s.ACLCategories, category)
}

// MovableKeys reports whether key positions can not be derived from the spec.
func (s *CommandSpec) MovableKeys() bool {
	return s.HasFlag("movablekeys")
}

// KeyIndexes returns indexes of key arguments in args (args[0] is the command name).
func (s *CommandSpec) KeyIndexes(argc int) []int {
	if s.FirstKey <= 0 || s.Step <= 0 {
		return nil
	}

	last := s.LastKey
	if last < 0 {
		last += argc
	}

	var indexes []int
	for i := s.FirstKey; i <= last && i < argc; i += s.Step {
		indexes = append(indexes, i)
	}

	return indexes
}

// CommandTable holds metadata of commands supported by the connected server,
// including commands provided by modules. It is safe for concurrent use.
type CommandTable struct {
	commands atomic.Pointer[map[string]*CommandSpec]
}

func (t *CommandTable) Load(ctx context.Context, client redis.UniversalClient) error {
	reply, err := client.Do(ctx, "command").Slice()
	if err != nil {
		return fmt.Errorf("COMMAND: %w", err)
	}

	commands := make(map[string]*CommandSpec, len(reply))

	for _, entry := range reply {
		spec, err := parseCommandSpec(entry)
		if err != nil {
			return fmt.Errorf("parse COMMAND reply: %w", err)
		}

		commands[spec.Name] = spec
	}

	t.commands.Store(&commands)

	return nil
}

// Get returns a top-level command by name.
func (t *CommandTable) Get(name string) (*CommandSpec, bool) {
	commands := t.commands.Load()
	if commands == nil {
		return nil, false
	}

	spec, ok := (*commands)[strings.ToLower(name)]

	return spec, ok
}

// Lookup returns the most specific spec for a command line: the subcommand spec
// if the command has subcommands and args[1] names one of them.
func (t *CommandTable) Lookup(name, subcommand string) (*CommandSpec, bool) {
	spec, ok := t.Get(name)
	if !ok {
		return nil, false
	}

	if sub, ok := spec.Subcommands[strings.ToLower(subcommand)]; ok && subcommand != "" {
		return sub, true
	}

	return spec, true
}

// All returns all top-level commands.
func (t *CommandTable) All() []*CommandSpec {
	commands := t.commands.Load()
	if commands == nil {
		return nil
	}

	return slices.Collect(maps.Values(*commands))
}

func (t *CommandTable) Has(name string) bool {
	_, ok := t.Get(name)

	return ok
}

func (t *CommandTable) Len() int {
	commands := t.commands.Load()
	if commands == nil {
		return 0
	}

	return len(*commands)
}

// parseCommandSpec parses one entry of the COMMAND reply:
// [name, arity, flags, first key, last key, step, acl categories, tips, key specs, subcommands].
// Fields after the step are optional, older servers do not send them.
func parseCommandSpec(entry any) (*CommandSpec, error) {
	fields, ok := entry.([]any)
	if !ok || len(fields) < 6 { //nolint:mnd // mandatory fields
		return nil, fmt.Errorf("unexpected entry %v", entry)
	}

	name, ok := fields[0].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected command name %v", fields[0])
	}

	fullName := strings.ToLower(name)

	// Subcommands are reported as "container|subcommand".
	if _, sub, found := strings.Cut(fullName, "|"); found {
		name = sub
	}

	spec := &CommandSpec{
		Name:     strings.ToLower(name),
		FullName: fullName,
		Arity:    toInt(fields[1]),
		Flags:    toStrings(fields[2]),
		FirstKey: toInt(fields[3]),
		LastKey:  toInt(fields[4]),
		Step:     toInt(fields[5]),
	}

	if len(fields) > 6 { //nolint:mnd // acl categories
		spec.ACLCategories = toStrings(fields[6])
	}

	if len(fields) > 9 { //nolint:mnd // subcommands
		subs, _ := fields[9].([]any)
		if len(subs) > 0 {
			spec.Subcommands = make(map[string]*CommandSpec, len(subs))
		}

		for _, entry := range subs {
			sub, err := parseCommandSpec(entry)
			if err != nil {
				return nil, errors.Join(fmt.Errorf("subcommand of %s", spec.Name), err)
			}

			spec.Subcommands[sub.Name] = sub
		}
	}

	return spec, nil
}

func toInt(v any) int {
	i, _ := v.(int64)

	return int(i)
}

func toStrings(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))

	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, strings.ToLower(s))
		}
	}

	return out
}
