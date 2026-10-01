package auth

import (
	"errors"
	"testing"

	"github.com/btrvodka/redigate/internal/config"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	tokens := config.Auth{Token: "full", ReadOnlyToken: "ro"}

	tests := []struct {
		name  string
		cfg   config.Auth
		token string
		want  Access
		err   error
	}{
		{"disabled", config.Auth{}, "", Full, nil},
		{"disabled read-only", config.Auth{ReadOnly: true}, "", ReadOnly, nil},
		{"full", tokens, "full", Full, nil},
		{"read-only token", tokens, "ro", ReadOnly, nil},
		{"read-only mode", config.Auth{Token: "full", ReadOnly: true}, "full", ReadOnly, nil},
		{"missing", tokens, "", None, ErrTokenRequired},
		{"invalid", tokens, "nope", None, ErrInvalidToken},
		{"empty read-only token is not a wildcard", config.Auth{Token: "full"}, "", None, ErrTokenRequired},
	}

	for _, tt := range tests {
		got, err := Resolve(tt.cfg, tt.token)
		if got != tt.want || !errors.Is(err, tt.err) {
			t.Errorf("%s: Resolve() = %v, %v, want %v, %v", tt.name, got, err, tt.want, tt.err)
		}
	}
}
