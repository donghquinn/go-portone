package portone

import (
	"errors"
	"fmt"
)

// Error types returned by the API (the "type" field). Not exhaustive: the API may add more.
const (
	ErrTypeInvalidRequest            = "INVALID_REQUEST"
	ErrTypeUnauthorized              = "UNAUTHORIZED"
	ErrTypeForbidden                 = "FORBIDDEN"
	ErrTypePaymentNotFound           = "PAYMENT_NOT_FOUND"
	ErrTypePaymentNotPaid            = "PAYMENT_NOT_PAID"
	ErrTypePaymentAlreadyCancelled   = "PAYMENT_ALREADY_CANCELLED"
	ErrTypeAlreadyPaid               = "ALREADY_PAID"
	ErrTypePgProvider                = "PG_PROVIDER"
	ErrTypeCancelAmountExceeds       = "CANCEL_AMOUNT_EXCEEDS_CANCELLABLE_AMOUNT"
	ErrTypeCancellableAmountMismatch = "CANCELLABLE_AMOUNT_CONSISTENCY_BROKEN"
)

// APIError is a non-2xx response from the API.
type APIError struct {
	StatusCode int    `json:"-"`
	Type       string `json:"type"`
	Message    string `json:"message,omitempty"`
	// PgCode and PgMessage are set for PG_PROVIDER errors (the PG rejected the request).
	PgCode    string `json:"pgCode,omitempty"`
	PgMessage string `json:"pgMessage,omitempty"`
}

func (e *APIError) Error() string {
	if e.PgCode != "" {
		return fmt.Sprintf("portone: %d %s: %s (pg %s: %s)", e.StatusCode, e.Type, e.Message, e.PgCode, e.PgMessage)
	}
	return fmt.Sprintf("portone: %d %s: %s", e.StatusCode, e.Type, e.Message)
}

// IsErrorType reports whether err is (or wraps) an *APIError of the given type.
func IsErrorType(err error, errType string) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Type == errType
}

// IsNotFound reports whether err means the payment does not exist.
func IsNotFound(err error) bool { return IsErrorType(err, ErrTypePaymentNotFound) }
