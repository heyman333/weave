package errors

import (
	"fmt"

	"github.com/pkg/errors"
)

var (
	// InternalErr represents a general case issue that cannot be
	// categorized as any of the below cases.
	InternalErr = NewRootError(0, "internal")

	// UnauthorizedErr is used whenever a request without sufficient
	// authorization is handled.
	UnauthorizedErr = NewRootError(1, "unauthorized")

	// NotFoundErr is used when a requested operation cannot be completed
	// due to missing data.
	NotFoundErr = NewRootError(2, "not found")

	// InvalidMsgErr is returned whenever an event is invalid and cannot be
	// handled.
	InvalidMsgErr = NewRootError(3, "invalid message")

	// InvalidModelErr is returned whenever a message is invalid and cannot
	// be used (ie. persisted).
	InvalidModelErr = NewRootError(4, "invalid model")

	// InvalidValueErr is returned whenever a validation is failing due to
	// unaccepted value. This is a generalization of InvalidMsgErr and
	// InvalidModelErr errors.
	InvalidValueErr = NewRootError(5, "invalid value")
)

// NewRootError returns an error instance that should be used as the base for
// creating error instances during runtime.
//
// Popular root errors are declared in this package, but extensions may want to
// declare custom codes. This function ensures that no error code is used
// twice. Attempt to reuse an error code results in panic.
//
// Use this function only during a program startup phase.
func NewRootError(code uint32, description string) RootError {
	if e, ok := usedCodes[code]; ok {
		panic(fmt.Sprintf("error with code %d is already registered: %q", code, e.desc))
	}
	err := RootError{
		code: code,
		desc: description,
	}
	usedCodes[err.code] = err
	return err
}

// usedCodes is keeping track of used codes to ensure uniqueness.
var usedCodes = map[uint32]RootError{}

// RootError represents a root error.
//
// Weave framework is using root error to categorize issues. Each instance
// created during the runtime should wrap one of the declared root errors. This
// allows error tests and returning all errors to the client in a safe manner.
//
// All popular root errors are declared in this package. If an extension has to
// declare a custom root error, always use NewRootError function to ensure
// error code uniqueness.
type RootError struct {
	code uint32
	desc string
}

func (e RootError) Error() string    { return e.desc }
func (e RootError) ABCICode() uint32 { return e.code }
func (e RootError) ABCILog() string  { return e.desc }

// New returns a new error. Returned instance is having the root cause set to
// this error. Below two lines are equal
//   e.New("my description")
//   Wrap(e, "my description")
func (e RootError) New(description string) error {
	return Wrap(e, description)
}

// This Wrap implementation provides a transition layer between two Wrap
// function implementations with incompatible notations.
//
// Once migration is complete, it will be removed and replaced by wrapng
// function.
func Wrap(err error, description ...string) TMError {
	switch len(description) {
	case 0:
		// fmt.Fprintf
		// debug.PrintStack()
		return deprecatedLegacyWrap(err)
	case 1:
		return wrapng(err, description[0])
	default:
		panic("invalid Wrap notation used")
	}
}

// Wrap extends given error with an additional information.
//
// If the wrapped error does not provide ABCICode method (ie. stdlib errors),
// it will be labeled as internal error.
func wrapng(err error, description string) TMError {
	return &wrappedError{
		Parent: err,
		Msg:    description,
	}
}

type wrappedError struct {
	// This error layer description.
	Msg string
	// The underlying error that triggered this one.
	Parent error
}

func (e *wrappedError) StackTrace() errors.StackTrace {
	// TODO: this is either to be implemented or expectation of it being
	// present removed completely. As this is an early stage of
	// refactoring, this is left unimplemented for now.
	return nil
}

func (e *wrappedError) Error() string {
	if e.Parent == nil {
		return e.Msg
	}
	return fmt.Sprintf("%s: %s", e.Msg, e.Parent.Error())
}

func (e *wrappedError) ABCICode() uint32 {
	if e.Parent == nil {
		return InternalErr.code
	}
	type coder interface {
		ABCICode() uint32
	}
	if p, ok := e.Parent.(coder); ok {
		return p.ABCICode()
	}
	return InternalErr.code
}

func (e *wrappedError) ABCILog() string {
	// Internal error must not be revealed as a public API message.
	// Instead, return generic description.
	if e.ABCICode() == InternalErr.code {
		return "internal error"
	}
	return e.Error()
}

func (e *wrappedError) Cause() error {
	type causer interface {
		Cause() error
	}
	// If there is no parent, this is the root error and the cause.
	if e.Parent == nil {
		return e
	}
	if c, ok := e.Parent.(causer); ok {
		if cause := c.Cause(); cause != nil {
			return cause
		}
	}
	return e.Parent
}

// Is returns true if both errors represent the same class of issue. For
// example, both errors' root cause is NotFoundErr.
//
// If two errors are not the same instance, Is always returns false if at least
// one of the errors is internal. This is because all external errors (created
// outside of weave package) are internal to the implementation and we cannot
// reason about their equality.
func Is(a, b error) bool {
	if a == b {
		return true
	}

	type coder interface {
		ABCICode() uint32
	}

	// Two errors are equal only if none of them is internal and they have
	// the same ABCICode.
	ac, ok := a.(coder)
	if !ok || ac.ABCICode() == InternalErr.code {
		return false
	}
	bc, ok := b.(coder)
	if !ok || bc.ABCICode() == InternalErr.code {
		return false
	}
	return ac.ABCICode() == bc.ABCICode()
}
