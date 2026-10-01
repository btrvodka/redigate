// Package auth resolves the access level granted by a token. It is shared by
// the HTTP API (bearer tokens) and the web UI (a session cookie).
package auth

import (
	"crypto/subtle"
	"errors"

	"github.com/btrvodka/redigate/internal/config"
)

// Access is the level of access granted to a request.
type Access int

const (
	None Access = iota
	ReadOnly
	Full
)

func (a Access) String() string {
	switch a {
	case ReadOnly:
		return "read-only"
	case Full:
		return "full"
	default:
		return "none"
	}
}

var (
	ErrTokenRequired = errors.New("token is required")
	ErrInvalidToken  = errors.New("invalid token")
)

// Resolve returns the access granted by the token. Without configured tokens
// authentication is disabled and every request gets full access, unless READ_ONLY is set.
func Resolve(cfg config.Auth, token string) (Access, error) {
	access := Full

	if cfg.Enabled() {
		switch {
		case token == "":
			return None, ErrTokenRequired
		case equal(token, cfg.Token):
			access = Full
		case equal(token, cfg.ReadOnlyToken):
			access = ReadOnly
		default:
			return None, ErrInvalidToken
		}
	}

	if cfg.ReadOnly {
		access = min(access, ReadOnly)
	}

	return access, nil
}

func equal(got, want string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
