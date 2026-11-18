package portone

import (
	"encoding/json"
	"fmt"
	"strings"
)

// APIError is a non-2xx response from the API.
//
// When the response's "type" is one this SDK knows, Err holds the typed error (e.g. [*PaymentNotFoundError],
// [*PgProviderError]), so errors.As reaches it through the APIError:
//
//	var notFound *portone.PaymentNotFoundError
//	if errors.As(err, &notFound) { ... }
type APIError struct {
	StatusCode int
	// Type is the API's error type, e.g. "PAYMENT_NOT_FOUND". Empty if the body was not a PortOne error.
	Type    string
	Message string
	// Body is the raw response body.
	Body []byte
	// Err is the typed error decoded from Body, or nil for an error type this SDK version does not know.
	Err error
}

func (e *APIError) Error() string {
	if e.Type == "" {
		return fmt.Sprintf("portone: http %d: %s", e.StatusCode, truncate(strings.TrimSpace(string(e.Body)), 200))
	}
	return fmt.Sprintf("portone: http %d: %s: %s", e.StatusCode, e.Type, e.Message)
}

// Unwrap returns the typed error, if any.
func (e *APIError) Unwrap() error { return e.Err }

func newAPIError(status int, body []byte, decodeErr func([]byte) error) *APIError {
	e := &APIError{StatusCode: status, Body: body}
	var head struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &head) != nil {
		return e
	}
	e.Type, e.Message = head.Type, head.Message
	e.Err = decodeErr(body)
	return e
}

// errorDecoder adapts an operation's error one-of decoder. Unknown or undecodable errors yield nil.
func errorDecoder[E error](decode func([]byte) (E, error)) func([]byte) error {
	return func(b []byte) error {
		e, err := decode(b)
		if err != nil {
			return nil
		}
		if _, ok := any(e).(*UnrecognizedVariant); ok {
			return nil
		}
		return e
	}
}

// errorString formats a typed API error.
func errorString(typ string, message *string) string {
	if message == nil || *message == "" {
		return "portone: " + typ
	}
	return "portone: " + typ + ": " + *message
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
