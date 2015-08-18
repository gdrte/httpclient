package errors

import (
	"fmt"
)

type Code string

const (
	// Public available error types.
	// These errors are provided because they are specifically required by business logic in the callers.
	UnspecifiedError    = Code("Unspecified")
	NotFoundError       = Code("NotFound")
	DuplicateValueError = Code("DuplicateValue")
	TimeoutError        = Code("Timeout")
	UnauthorisedError   = Code("Unauthorised")
)

// Error instances store an optional error cause.
type Error interface {
	error
	Cause() error
}
type httpError struct {
	errcode Code
	error
	cause error
}

var _ Error = (*httpError)(nil)

// New creates a new Error instance with the specified cause.
func makeErrorf(code Code, cause error, format string, args ...interface{}) Error {
	return &httpError{
		errcode: code,
		error:   fmt.Errorf(format, args...),
		cause:   cause,
	}
}

// Cause returns the error cause.
func (err *httpError) Cause() error {
	return err.cause
}

// Code returns the error code.
func (err *httpError) code() Code {
	if err.errcode != UnspecifiedError {
		return err.errcode
	}
	if e, ok := err.cause.(*httpError); ok {
		return e.code()
	}
	return UnspecifiedError
}

// CausedBy returns true if this error or its cause are of the specified error code.
func (err *httpError) causedBy(code Code) bool {
	if err.code() == code {
		return true
	}
	if cause, ok := err.cause.(*httpError); ok {
		return cause.code() == code
	}
	return false
}

// Error fulfills the error interface, taking account of any caused by error.
func (err *httpError) Error() string {
	if err.cause != nil {
		return fmt.Sprintf("%v\ncaused by: %v", err.error, err.cause)
	}
	return err.error.Error()
}

// New creates a new Unspecified Error instance with the specified cause.
func Newf(cause error, format string, args ...interface{}) Error {
	return makeErrorf(UnspecifiedError, cause, format, args...)
}

// New creates a new NotFound Error instance with the specified cause.
func NewNotFoundf(cause error, context interface{}, format string, args ...interface{}) Error {
	if format == "" {
		format = fmt.Sprintf("Not found: %s", context)
	}
	return makeErrorf(NotFoundError, cause, format, args...)
}

// New creates a new DuplicateValue Error instance with the specified cause.
func NewDuplicateValuef(cause error, context interface{}, format string, args ...interface{}) Error {
	if format == "" {
		format = fmt.Sprintf("Duplicate: %s", context)
	}
	return makeErrorf(DuplicateValueError, cause, format, args...)
}

// New creates a new Timeout Error instance with the specified cause.
func NewTimeoutf(cause error, context interface{}, format string, args ...interface{}) Error {
	if format == "" {
		format = fmt.Sprintf("Timeout: %s", context)
	}
	return makeErrorf(TimeoutError, cause, format, args...)
}

// New creates a new Unauthorised Error instance with the specified cause.
func NewUnauthorisedf(cause error, context interface{}, format string, args ...interface{}) Error {
	if format == "" {
		format = fmt.Sprintf("Unauthorised: %s", context)
	}
	return makeErrorf(UnauthorisedError, cause, format, args...)
}
