package domain

import (
	"errors"
	"fmt"
)

// Error codes are part of the CLI/GUI contract; keep them stable.
const (
	CodeNotFound          = "not_found"
	CodeAlreadyExists     = "already_exists"
	CodeBuiltinReadonly   = "builtin_readonly"
	CodeBlockedByProfile  = "blocked_by_profile"
	CodeLocked            = "locked"
	CodeInvalidArgument   = "invalid_argument"
	CodeDaemonUnavailable = "daemon_unavailable"
	CodeBackend           = "backend_error"
	CodeInternal          = "internal"
)

// Error is a typed error carried unchanged from the engine to the CLI JSON.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithDetails attaches structured data (e.g. which profiles hold an app).
func (e *Error) WithDetails(kv map[string]any) *Error {
	e.Details = kv
	return e
}

// AsError converts any error into a *Error, defaulting to CodeInternal.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var de *Error
	if errors.As(err, &de) {
		return de
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}
