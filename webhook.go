package portone

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Webhook event types (webhook version 2024-04-25). New types may be added; ignore unknown ones.
const (
	WebhookTransactionReady                = "Transaction.Ready"
	WebhookTransactionPaid                 = "Transaction.Paid"
	WebhookTransactionVirtualAccountIssued = "Transaction.VirtualAccountIssued"
	WebhookTransactionPartialCancelled     = "Transaction.PartialCancelled"
	WebhookTransactionCancelled            = "Transaction.Cancelled"
	WebhookTransactionFailed               = "Transaction.Failed"
	WebhookTransactionPayPending           = "Transaction.PayPending"
	WebhookTransactionCancelPending        = "Transaction.CancelPending"
	WebhookTransactionDisputeCreated       = "Transaction.DisputeCreated"
	WebhookTransactionDisputeResolved      = "Transaction.DisputeResolved"
	WebhookBillingKeyReady                 = "BillingKey.Ready"
	WebhookBillingKeyIssued                = "BillingKey.Issued"
	WebhookBillingKeyFailed                = "BillingKey.Failed"
	WebhookBillingKeyDeleted               = "BillingKey.Deleted"
	WebhookBillingKeyUpdated               = "BillingKey.Updated"
)

// WebhookTolerance is how far the webhook-timestamp may be from now (same as PortOne's SDKs).
const WebhookTolerance = 5 * time.Minute

// Webhook is a verified webhook payload. Data never carries amounts: look the payment up with
// GetPayment before acting on it.
type Webhook struct {
	Type      string      `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Data      WebhookData `json:"data"`
}

type WebhookData struct {
	StoreID        string `json:"storeId"`
	PaymentID      string `json:"paymentId,omitempty"`
	TransactionID  string `json:"transactionId,omitempty"`
	CancellationID string `json:"cancellationId,omitempty"`
	BillingKey     string `json:"billingKey,omitempty"`
}

// IsTransaction reports whether the event is about a payment (Transaction.*).
func (w *Webhook) IsTransaction() bool { return strings.HasPrefix(w.Type, "Transaction.") }

var (
	ErrWebhookMissingHeaders = errors.New("portone: webhook headers missing")
	ErrWebhookBadSecret      = errors.New("portone: webhook secret is not valid base64")
	ErrWebhookTimestamp      = errors.New("portone: webhook timestamp is invalid")
	ErrWebhookTooOld         = errors.New("portone: webhook timestamp is too old")
	ErrWebhookTooNew         = errors.New("portone: webhook timestamp is in the future")
	ErrWebhookSignature      = errors.New("portone: webhook signature does not match")
	ErrWebhookPayload        = errors.New("portone: webhook payload is not valid JSON")
)

// WebhookVerifier checks the Standard Webhooks signature PortOne sends
// (headers webhook-id, webhook-timestamp, webhook-signature; HMAC-SHA256 over "id.timestamp.body").
// It accepts several secrets so a secret can be rotated without downtime.
type WebhookVerifier struct {
	keys [][]byte
	now  func() time.Time
}

// NewWebhookVerifier takes the webhook secret(s) from the PortOne console, with or without the "whsec_" prefix.
func NewWebhookVerifier(secrets ...string) (*WebhookVerifier, error) {
	v := &WebhookVerifier{now: time.Now}
	for _, s := range secrets {
		if s == "" {
			continue
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, "whsec_"))
		if err != nil || len(key) == 0 {
			return nil, ErrWebhookBadSecret
		}
		v.keys = append(v.keys, key)
	}
	if len(v.keys) == 0 {
		return nil, ErrWebhookBadSecret
	}
	return v, nil
}

// Verify checks the signature and timestamp of a webhook request and decodes the payload.
// body must be the raw request body, byte for byte.
func (v *WebhookVerifier) Verify(body []byte, header http.Header) (*Webhook, error) {
	id := single(header, "webhook-id")
	ts := single(header, "webhook-timestamp")
	sigs := single(header, "webhook-signature")
	if id == "" || ts == "" || sigs == "" {
		return nil, ErrWebhookMissingHeaders
	}

	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, ErrWebhookTimestamp
	}
	now := v.now().Unix()
	switch {
	case sec < now-int64(WebhookTolerance/time.Second):
		return nil, ErrWebhookTooOld
	case sec > now+int64(WebhookTolerance/time.Second):
		return nil, ErrWebhookTooNew
	}

	matched := false
	for _, key := range v.keys {
		expected := sign(key, id, ts, body)
		for _, versioned := range strings.Split(sigs, " ") {
			version, sig, ok := strings.Cut(versioned, ",")
			if !ok || version != "v1" {
				continue
			}
			got, err := base64.StdEncoding.DecodeString(sig)
			if err == nil && hmac.Equal(got, expected) {
				matched = true
			}
		}
	}
	if !matched {
		return nil, ErrWebhookSignature
	}

	var w Webhook
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, ErrWebhookPayload
	}
	return &w, nil
}

// SignWebhook builds a webhook-signature header value ("v1,<base64>") for a payload.
// It is what PortOne does; use it to test your webhook handler.
func SignWebhook(secret, id string, timestamp time.Time, body []byte) (string, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil || len(key) == 0 {
		return "", ErrWebhookBadSecret
	}
	return "v1," + base64.StdEncoding.EncodeToString(sign(key, id, strconv.FormatInt(timestamp.Unix(), 10), body)), nil
}

func sign(key []byte, id, ts string, body []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id))
	mac.Write([]byte{'.'})
	mac.Write([]byte(ts))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return mac.Sum(nil)
}

// single returns the header value only if it appears exactly once (duplicates are rejected, as in PortOne's SDK).
func single(h http.Header, name string) string {
	values := h.Values(name)
	if len(values) != 1 {
		return ""
	}
	return values[0]
}
