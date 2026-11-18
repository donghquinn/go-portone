package portone

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// UnrecognizedVariant is a one-of value whose discriminator this SDK version does not know, typically a
// variant PortOne added after this SDK was generated. It satisfies every one-of interface.
type UnrecognizedVariant struct {
	// Discriminator is the value of the one-of's discriminator property, e.g. a new payment status.
	Discriminator string
	// Raw is the JSON object as received.
	Raw json.RawMessage
}

// MarshalJSON writes the value back as received.
func (u *UnrecognizedVariant) MarshalJSON() ([]byte, error) {
	if len(u.Raw) == 0 {
		return []byte("null"), nil
	}
	return u.Raw, nil
}

// Error lets an UnrecognizedVariant stand in for an error one-of.
func (u *UnrecognizedVariant) Error() string {
	return "portone: unrecognized variant " + u.Discriminator
}

func newUnrecognizedVariant(discriminator string, raw []byte) *UnrecognizedVariant {
	return &UnrecognizedVariant{Discriminator: discriminator, Raw: bytes.Clone(raw)}
}

// readDiscriminator reads one string property of a JSON object.
func readDiscriminator(b []byte, prop string) (value string, isNull bool, err error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return "", true, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return "", false, err
	}
	if raw, ok := fields[prop]; ok {
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", false, fmt.Errorf("discriminator %q: %w", prop, err)
		}
	}
	return value, false, nil
}

// decodeVariant decodes b as *T and returns it as the one-of interface U.
func decodeVariant[T any, U any](b []byte) (U, error) {
	var zero U
	v := new(T)
	if err := json.Unmarshal(b, v); err != nil {
		return zero, err
	}
	u, ok := any(v).(U)
	if !ok {
		return zero, fmt.Errorf("%T does not implement %T", v, &zero)
	}
	return u, nil
}

func unmarshalSlice[T any](raws []json.RawMessage, decode func([]byte) (T, error)) ([]T, error) {
	if raws == nil {
		return nil, nil
	}
	out := make([]T, len(raws))
	for i, raw := range raws {
		v, err := decode(raw)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}
