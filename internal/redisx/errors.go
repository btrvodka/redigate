package redisx

import (
	"errors"
	"strings"

	"github.com/redis/go-redis/v9"
)

var (
	ErrClusterDB     = errors.New("redis cluster supports only db 0")
	ErrInvalidDB     = errors.New("invalid db")
	ErrNodeNotFound  = errors.New("node not found")
	ErrNotSupported  = errors.New("operation is not supported by the current topology")
	ErrNoSentinel    = errors.New("no sentinel is reachable")
	ErrMasterUnknown = errors.New("sentinel master name is ambiguous")
)

// ReplyError returns the error reply sent by the server, if err is one.
func ReplyError(err error) (redis.Error, bool) {
	var replyErr redis.Error
	if errors.As(err, &replyErr) && !errors.Is(err, redis.Nil) {
		return replyErr, true
	}

	return nil, false
}

// ReplyErrorPrefix returns the upper-cased first word of an error reply
// ("WRONGTYPE", "MOVED", "NOPERM", ...), or "" if err is not an error reply.
func ReplyErrorPrefix(err error) string {
	replyErr, ok := ReplyError(err)
	if !ok {
		return ""
	}

	prefix, _, _ := strings.Cut(replyErr.Error(), " ")

	return strings.ToUpper(prefix)
}
