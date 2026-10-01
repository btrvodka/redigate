package service

import (
	"context"
	"fmt"
)

// ACL users are stored by every node separately: changes go to all data nodes by default.

func shapeMapReply(reply any) (any, error) {
	return encodeAll(ShapeMap(reply)), nil
}

func shapeMapListReply(reply any) (any, error) {
	return encodeAll(ShapeMapList(reply)), nil
}

func (s *Service) ACLUsers(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "acl", "users")
}

// ACLList returns users with their rules in the ACL file format.
func (s *Service) ACLList(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "acl", "list")
}

func (s *Service) ACLGetUser(ctx context.Context, sel NodeSelector, user string) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, shapeMapReply, "acl", "getuser", user)
}

// ACLSetUser applies rules ("on", ">password", "~keys:*", "+@read", ...) to the user,
// optionally resetting it first.
func (s *Service) ACLSetUser(ctx context.Context, sel NodeSelector, user string, rules []string, reset bool) (*FanOutResult, error) {
	args := []any{"acl", "setuser", user}
	if reset {
		args = append(args, "reset")
	}

	return s.perNodeCommand(ctx, sel, TargetAll, nil, commandArgs(args, rules)...)
}

func (s *Service) ACLDelUser(ctx context.Context, sel NodeSelector, users []string) (*FanOutResult, error) {
	if len(users) == 0 {
		return nil, fmt.Errorf("%w: user is required", ErrInvalidRequest)
	}

	return s.perNodeCommand(ctx, sel, TargetAll, nil, commandArgs([]any{"acl", "deluser"}, users)...)
}

func (s *Service) ACLLog(ctx context.Context, sel NodeSelector, count int) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, shapeMapListReply, "acl", "log", count)
}

func (s *Service) ACLLogReset(ctx context.Context, sel NodeSelector) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "acl", "log", "reset")
}

// ACLPersist runs ACL SAVE or ACL LOAD (requires an ACL file on the server).
func (s *Service) ACLPersist(ctx context.Context, sel NodeSelector, cmd string) (*FanOutResult, error) {
	return s.perNodeCommand(ctx, sel, TargetAll, nil, "acl", cmd)
}

// ACLDryRun checks whether the user may run the command without running it (redis 7+).
func (s *Service) ACLDryRun(ctx context.Context, node, user string, command []string) (*CommandResult, error) {
	if user == "" || len(command) == 0 {
		return nil, fmt.Errorf("%w: user and command are required", ErrInvalidRequest)
	}

	return s.onNode(ctx, node, s.anyMaster, nil, commandArgs([]any{"acl", "dryrun", user}, command)...)
}

func (s *Service) ACLWhoAmI(ctx context.Context, node string) (*CommandResult, error) {
	return s.onNode(ctx, node, s.anyMaster, nil, "acl", "whoami")
}

func (s *Service) ACLCat(ctx context.Context, category string) (*CommandResult, error) {
	args := []any{"acl", "cat"}
	if category != "" {
		args = append(args, category)
	}

	return s.onNode(ctx, "", s.anyMaster, nil, args...)
}

func (s *Service) ACLGenPass(ctx context.Context, bits int) (*CommandResult, error) {
	args := []any{"acl", "genpass"}
	if bits > 0 {
		args = append(args, bits)
	}

	return s.onNode(ctx, "", s.anyMaster, nil, args...)
}
