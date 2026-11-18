package portone

import (
	"encoding/json"
	"testing"
)

func TestUnion_unrecognizedVariant(t *testing.T) {
	t.Parallel()
	const giveMethod = `{"type":"PaymentMethodTeleport","planet":"Mars"}`
	body := `{"status":"FAILED","id":"order-1","method":` + giveMethod + `}`

	got, err := unmarshalPayment([]byte(body))
	if err != nil {
		t.Fatal(err)
	}

	failed, ok := got.(*FailedPayment)
	if !ok {
		t.Fatalf("unmarshalPayment() = %T, want *FailedPayment", got)
	}
	u, ok := failed.Method.(*UnrecognizedVariant)
	if !ok || u.Discriminator != "PaymentMethodTeleport" || string(u.Raw) != giveMethod {
		t.Errorf("Method = %#v, want *UnrecognizedVariant", failed.Method)
	}

	out, err := json.Marshal(failed)
	if err != nil {
		t.Fatal(err)
	}
	var again map[string]json.RawMessage
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	if string(again["method"]) != giveMethod || string(again["status"]) != `"FAILED"` {
		t.Errorf("json.Marshal() = %s, want status and the method kept as received", out)
	}
}

func TestUnion_unknownTopLevel(t *testing.T) {
	t.Parallel()
	got, err := unmarshalPayment([]byte(`{"status":"REFUNDED_TO_MOON"}`))
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := got.(*UnrecognizedVariant); !ok || u.Discriminator != "REFUNDED_TO_MOON" {
		t.Errorf("unmarshalPayment() = %#v, want *UnrecognizedVariant", got)
	}
}

func TestUnion_null(t *testing.T) {
	t.Parallel()
	for _, give := range []string{"", "null", " null "} {
		got, err := unmarshalPayment([]byte(give))
		if err != nil || got != nil {
			t.Errorf("unmarshalPayment(%q) = %v, %v, want nil, nil", give, got, err)
		}
	}
}

func TestUnion_variantMarshalRoundTrip(t *testing.T) {
	t.Parallel()
	give := &PaidPayment{ID: "order-1", Method: &PaymentMethodCard{ApprovalNumber: Ptr("1")}}

	raw, err := json.Marshal(Payment(give))
	if err != nil {
		t.Fatal(err)
	}
	got, err := unmarshalPayment(raw)
	if err != nil {
		t.Fatal(err)
	}

	paid, ok := got.(*PaidPayment)
	if !ok || paid.ID != "order-1" {
		t.Fatalf("round trip = %#v from %s", got, raw)
	}
	if card, ok := paid.Method.(*PaymentMethodCard); !ok || *card.ApprovalNumber != "1" {
		t.Errorf("round trip Method = %#v from %s", paid.Method, raw)
	}
}
