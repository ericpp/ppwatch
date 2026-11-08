package hive

import "fmt"

// PodpingError is the base error for PodPing operations.
type PodpingError struct{ Msg string }

func (e *PodpingError) Error() string { return e.Msg }

// PodpingConnectionError is an error connecting to Hive nodes.
type PodpingConnectionError struct{ PodpingError }

// PodpingAuthenticationError is an error with Hive account authentication.
type PodpingAuthenticationError struct{ PodpingError }

// PodpingValidationError is an error validating input data (URLs, etc.).
type PodpingValidationError struct{ PodpingError }

// PodpingNetworkError is a network-related error during operations.
type PodpingNetworkError struct{ PodpingError }

// NewError creates a base podping error.
func NewError(format string, args ...any) error {
	return &PodpingError{Msg: fmt.Sprintf(format, args...)}
}

// NewConnectionError creates a connection error.
func NewConnectionError(format string, args ...any) error {
	return &PodpingConnectionError{PodpingError{Msg: fmt.Sprintf(format, args...)}}
}

// NewAuthenticationError creates an authentication error.
func NewAuthenticationError(format string, args ...any) error {
	return &PodpingAuthenticationError{PodpingError{Msg: fmt.Sprintf(format, args...)}}
}

// NewValidationError creates a validation error.
func NewValidationError(format string, args ...any) error {
	return &PodpingValidationError{PodpingError{Msg: fmt.Sprintf(format, args...)}}
}

// NewNetworkError creates a network error.
func NewNetworkError(format string, args ...any) error {
	return &PodpingNetworkError{PodpingError{Msg: fmt.Sprintf(format, args...)}}
}
