package domain

import "fmt"

// Kind задаёт смысл ошибки; транспорт выбирает HTTP/gRPC представление.
type Kind string

const (
	Invalid      Kind = "validation_error"
	Forbidden    Kind = "forbidden"
	Limited      Kind = "rate_limited"
	Missing      Kind = "not_found"
	Duplicate    Kind = "conflict"
	Unavailable  Kind = "external_service_error"
	Unauthorized Kind = "unauthorized"
)

type Error struct {
	Kind    Kind
	Message string
	Plain   bool
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func NewError(kind Kind, message string) error {
	return &Error{Kind: kind, Message: message}
}
