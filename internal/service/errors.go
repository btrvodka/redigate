package service

import "errors"

var (
	ErrInvalidRequest     = errors.New("invalid request")
	ErrForbidden          = errors.New("forbidden")
	ErrCrossSlot          = errors.New("keys don't hash to the same slot")
	ErrUnsupportedCommand = errors.New("unsupported command")
)
