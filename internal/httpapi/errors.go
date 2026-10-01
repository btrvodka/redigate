package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/redis/go-redis/v9"

	"github.com/btrvodka/redigate/internal/redisx"
	"github.com/btrvodka/redigate/internal/service"
)

const (
	CodeBadRequest       = "BAD_REQUEST"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeForbidden        = "FORBIDDEN"
	CodeNotFound         = "NOT_FOUND"
	CodeRouteNotFound    = "ROUTE_NOT_FOUND"
	CodePayloadTooLarge  = "PAYLOAD_TOO_LARGE"
	CodeNotSupported     = "NOT_SUPPORTED"
	CodeInternal         = "INTERNAL_ERROR"
	CodeTimeout          = "TIMEOUT"
	CodeCanceled         = "CANCELED"
	CodeRedisError       = "REDIS_ERROR"
	CodeWrongType        = "WRONG_TYPE"
	CodeCrossSlot        = "CROSS_SLOT"
	CodeRedisNoPerm      = "REDIS_NO_PERMISSION"
	CodeRedisAuth        = "REDIS_AUTH_FAILED"
	CodeRedisReadOnly    = "REDIS_READONLY"
	CodeRedisBusy        = "REDIS_BUSY"
	CodeRedisUnavailable = "REDIS_UNAVAILABLE"
	CodeRedisOOM         = "REDIS_OOM"
	CodeRedisRedirect    = "REDIS_REDIRECT"
	CodeUnsupportedCmd   = "UNSUPPORTED_COMMAND"
)

// APIError is an error with an HTTP status and a stable machine-readable code.
type APIError struct {
	Status  int
	Code    string
	Message string
	Err     error
}

func (e *APIError) Error() string {
	return e.Message
}

func (e *APIError) Unwrap() error {
	return e.Err
}

func newAPIError(status int, code, format string, args ...any) *APIError {
	return &APIError{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

func badRequest(format string, args ...any) *APIError {
	return newAPIError(http.StatusBadRequest, CodeBadRequest, format, args...)
}

//nolint:cyclop // flat mapping table
func toAPIError(err error) *APIError {
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		return apiErr
	}

	wrap := func(status int, code string) *APIError {
		return &APIError{Status: status, Code: code, Message: err.Error(), Err: err}
	}

	switch {
	case errors.Is(err, service.ErrInvalidRequest):
		return wrap(http.StatusBadRequest, CodeBadRequest)
	case errors.Is(err, service.ErrForbidden):
		return wrap(http.StatusForbidden, CodeForbidden)
	case errors.Is(err, service.ErrCrossSlot):
		return wrap(http.StatusBadRequest, CodeCrossSlot)
	case errors.Is(err, service.ErrUnsupportedCommand):
		return wrap(http.StatusBadRequest, CodeUnsupportedCmd)
	case errors.Is(err, redis.Nil):
		return wrap(http.StatusNotFound, CodeNotFound)
	case errors.Is(err, redisx.ErrNodeNotFound):
		return wrap(http.StatusNotFound, CodeNotFound)
	case errors.Is(err, redisx.ErrClusterDB), errors.Is(err, redisx.ErrNotSupported):
		return wrap(http.StatusNotImplemented, CodeNotSupported)
	case errors.Is(err, redisx.ErrInvalidDB):
		return wrap(http.StatusBadRequest, CodeBadRequest)
	case errors.Is(err, context.DeadlineExceeded):
		return wrap(http.StatusGatewayTimeout, CodeTimeout)
	case errors.Is(err, context.Canceled):
		// nginx-style "client closed request".
		return wrap(499, CodeCanceled) //nolint:mnd // non-standard status
	case errors.Is(err, redis.ErrClosed), redisx.IsNetworkError(err):
		return wrap(http.StatusBadGateway, CodeRedisUnavailable)
	}

	if _, ok := redisx.ReplyError(err); !ok {
		return wrap(http.StatusInternalServerError, CodeInternal)
	}

	switch redisx.ReplyErrorPrefix(err) {
	case "WRONGTYPE":
		return wrap(http.StatusBadRequest, CodeWrongType)
	case "CROSSSLOT":
		return wrap(http.StatusBadRequest, CodeCrossSlot)
	case "NOPERM":
		return wrap(http.StatusForbidden, CodeRedisNoPerm)
	case "NOAUTH", "WRONGPASS":
		return wrap(http.StatusBadGateway, CodeRedisAuth)
	case "READONLY":
		return wrap(http.StatusConflict, CodeRedisReadOnly)
	case "BUSY", "BUSYKEY", "NOTBUSY", "UNKILLABLE":
		return wrap(http.StatusConflict, CodeRedisBusy)
	case "OOM":
		return wrap(http.StatusInsufficientStorage, CodeRedisOOM)
	case "MOVED", "ASK":
		return wrap(http.StatusMisdirectedRequest, CodeRedisRedirect)
	case "LOADING", "MASTERDOWN", "CLUSTERDOWN", "TRYAGAIN", "NOREPLICAS":
		return wrap(http.StatusServiceUnavailable, CodeRedisUnavailable)
	default:
		return wrap(http.StatusBadRequest, CodeRedisError)
	}
}
