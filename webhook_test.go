package portone

import (
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// Test vector from the Standard Webhooks spec (which PortOne follows).
const (
	vectorSecret    = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	vectorID        = "msg_p5jXN8AQM9LWM0D4loKWxJek"
	vectorTimestamp = 1614265330
	vectorBody      = `{"test": 2432232314}`
	vectorSignature = "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="
)

func headers(id string, ts int64, sig string) http.Header {
	h := http.Header{}
	h.Set("webhook-id", id)
	h.Set("webhook-timestamp", strconv.FormatInt(ts, 10))
	h.Set("webhook-signature", sig)
	return h
}

func verifierAt(t *testing.T, now int64, secrets ...string) *WebhookVerifier {
	t.Helper()
	v, err := NewWebhookVerifier(secrets...)
	if err != nil {
		t.Fatal(err)
	}
	v.now = func() time.Time { return time.Unix(now, 0) }
	return v
}

func TestSignMatchesSpecVector(t *testing.T) {
	got, err := SignWebhook(vectorSecret, vectorID, time.Unix(vectorTimestamp, 0), []byte(vectorBody))
	if err != nil || got != vectorSignature {
		t.Fatalf("signature = %q (%v), want %q", got, err, vectorSignature)
	}
}

func TestVerify(t *testing.T) {
	v := verifierAt(t, vectorTimestamp+10, vectorSecret)

	if _, err := v.Verify([]byte(vectorBody), headers(vectorID, vectorTimestamp, vectorSignature)); err != nil {
		t.Errorf("valid webhook: %v", err)
	}
	// Several signatures (key rotation on the sender side): any v1 match passes, other versions are skipped.
	multi := "v2,AAAA v1,bm90LXRoZS1yaWdodC1vbmU= " + vectorSignature
	if _, err := v.Verify([]byte(vectorBody), headers(vectorID, vectorTimestamp, multi)); err != nil {
		t.Errorf("multi-signature header: %v", err)
	}

	for name, tc := range map[string]struct {
		body string
		h    http.Header
		want error
	}{
		"tampered body":   {`{"test": 2432232315}`, headers(vectorID, vectorTimestamp, vectorSignature), ErrWebhookSignature},
		"other id":        {vectorBody, headers("msg_other", vectorTimestamp, vectorSignature), ErrWebhookSignature},
		"garbage sig":     {vectorBody, headers(vectorID, vectorTimestamp, "v1,%%%"), ErrWebhookSignature},
		"no version":      {vectorBody, headers(vectorID, vectorTimestamp, "g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="), ErrWebhookSignature},
		"missing headers": {vectorBody, http.Header{}, ErrWebhookMissingHeaders},
		"bad timestamp": {vectorBody, func() http.Header {
			h := headers(vectorID, 1, vectorSignature)
			h.Set("webhook-timestamp", "abc")
			return h
		}(), ErrWebhookTimestamp},
	} {
		if _, err := v.Verify([]byte(tc.body), tc.h); !errors.Is(err, tc.want) {
			t.Errorf("%s: want %v, got %v", name, tc.want, err)
		}
	}

	dup := headers(vectorID, vectorTimestamp, vectorSignature)
	dup.Add("webhook-signature", vectorSignature)
	if _, err := v.Verify([]byte(vectorBody), dup); !errors.Is(err, ErrWebhookMissingHeaders) {
		t.Errorf("duplicate header must be rejected: %v", err)
	}

	if _, err := verifierAt(t, vectorTimestamp+6*60, vectorSecret).Verify([]byte(vectorBody), headers(vectorID, vectorTimestamp, vectorSignature)); !errors.Is(err, ErrWebhookTooOld) {
		t.Errorf("old: %v", err)
	}
	if _, err := verifierAt(t, vectorTimestamp-6*60, vectorSecret).Verify([]byte(vectorBody), headers(vectorID, vectorTimestamp, vectorSignature)); !errors.Is(err, ErrWebhookTooNew) {
		t.Errorf("future: %v", err)
	}
}

func TestVerifyRotationAndPayload(t *testing.T) {
	const newSecret = "whsec_c2Vjb25kLXNlY3JldC1rZXktZm9yLXRlc3Rz"
	now := time.Now()
	body := []byte(`{"type":"Transaction.Paid","timestamp":"2026-10-27T01:00:05Z","data":{"storeId":"store-abc","paymentId":"order-1","transactionId":"tx-1","futureField":1}}`)
	sig, err := SignWebhook(newSecret, "msg_1", now, body)
	if err != nil {
		t.Fatal(err)
	}
	h := headers("msg_1", now.Unix(), sig)

	if _, err := verifierAt(t, now.Unix(), vectorSecret).Verify(body, h); !errors.Is(err, ErrWebhookSignature) {
		t.Errorf("old secret only must fail: %v", err)
	}
	w, err := verifierAt(t, now.Unix(), vectorSecret, newSecret).Verify(body, h)
	if err != nil {
		t.Fatalf("either secret: %v", err)
	}
	if w.Type != WebhookTransactionPaid || !w.IsTransaction() || w.Data.PaymentID != "order-1" || w.Data.StoreID != "store-abc" || w.Timestamp.IsZero() {
		t.Errorf("payload: %+v", w)
	}

	// The prefix is optional.
	if _, err := verifierAt(t, now.Unix(), newSecret[len("whsec_"):]).Verify(body, h); err != nil {
		t.Errorf("secret without prefix: %v", err)
	}

	if _, err := NewWebhookVerifier("whsec_!!!"); !errors.Is(err, ErrWebhookBadSecret) {
		t.Errorf("bad secret: %v", err)
	}
	if _, err := NewWebhookVerifier(""); !errors.Is(err, ErrWebhookBadSecret) {
		t.Errorf("no secret: %v", err)
	}
}
