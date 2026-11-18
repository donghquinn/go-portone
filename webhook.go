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

// WebhookType is the "type" of a webhook (webhook version 2024-04-25).
// New types may be added; handle unknown ones gracefully.
type WebhookType string

// Webhook types.
const (
	WebhookTypeTransactionReady                WebhookType = "Transaction.Ready"                // 결제창이 열렸을 때
	WebhookTypeTransactionPaid                 WebhookType = "Transaction.Paid"                 // 결제(예약 결제 포함)가 승인되었을 때 (모든 결제 수단)
	WebhookTypeTransactionVirtualAccountIssued WebhookType = "Transaction.VirtualAccountIssued" // 가상계좌가 발급되었을 때
	WebhookTypeTransactionPartialCancelled     WebhookType = "Transaction.PartialCancelled"     // 결제가 부분 취소되었을 때
	WebhookTypeTransactionCancelled            WebhookType = "Transaction.Cancelled"            // 결제가 완전 취소되었을 때
	WebhookTypeTransactionFailed               WebhookType = "Transaction.Failed"               // 결제(예약 결제 포함)가 실패했을 때
	WebhookTypeTransactionPayPending           WebhookType = "Transaction.PayPending"           // 결제 승인 대기 상태가 되었을 때 (해외 결제시 발생 가능)
	WebhookTypeTransactionCancelPending        WebhookType = "Transaction.CancelPending"        // (결제 취소가 비동기로 수행되는 경우) 결제 취소를 요청했을 때
	WebhookTypeTransactionConfirm              WebhookType = "Transaction.Confirm"              // 컨펌 프로세스에서 승인 요청을 받았을 때
	WebhookTypeTransactionDisputeCreated       WebhookType = "Transaction.DisputeCreated"       // 분쟁이 발생되었을 때
	WebhookTypeTransactionDisputeResolved      WebhookType = "Transaction.DisputeResolved"      // 분쟁이 해소되었을 때
	WebhookTypeBillingKeyReady                 WebhookType = "BillingKey.Ready"                 // 빌링키 발급창이 열렸을 때
	WebhookTypeBillingKeyIssued                WebhookType = "BillingKey.Issued"                // 빌링키가 발급되었을 때
	WebhookTypeBillingKeyFailed                WebhookType = "BillingKey.Failed"                // 빌링키 발급이 실패했을 때
	WebhookTypeBillingKeyDeleted               WebhookType = "BillingKey.Deleted"               // 빌링키가 삭제되었을 때
	WebhookTypeBillingKeyUpdated               WebhookType = "BillingKey.Updated"               // 빌링키가 업데이트되었을 때
)

// Webhook is a verified webhook payload: [*WebhookTransaction] for "Transaction.*" types,
// [*WebhookBillingKey] for "BillingKey.*" types, or [*UnrecognizedVariant] for any other type.
//
// Payloads carry only IDs, never amounts: look the payment up (e.g. with PaymentClient.GetPayment) before acting on it.
type Webhook interface {
	isWebhook()
}

func (*UnrecognizedVariant) isWebhook() {}

// WebhookTransaction is a payment event (결제 관련).
type WebhookTransaction struct {
	Type WebhookType `json:"type"`
	// 해당 웹훅을 트리거한 이벤트의 발생 시각(RFC 3339 형식)입니다. 고객사 서버가 웹훅을 수신하는 데 실패하여 재시도가 일어나도 이 값은 동일하게 유지됩니다.
	Timestamp time.Time              `json:"timestamp"`
	Data      WebhookTransactionData `json:"data"`
}

func (*WebhookTransaction) isWebhook() {}

// WebhookTransactionData identifies the payment a [WebhookTransaction] is about.
type WebhookTransactionData struct {
	// 고객사에서 채번한 결제 건의 고유 주문 번호입니다.
	PaymentID string `json:"paymentId"`
	// 웹훅을 트리거한 상점의 아이디입니다.
	StoreID string `json:"storeId"`
	// 포트원에서 채번한 고유 거래 번호입니다. 한 결제 건에 여러 시도가 있을 경우 transactionId 가 달라질 수 있습니다.
	TransactionID string `json:"transactionId"`
	// 포트원에서 채번한 결제건의 취소 고유 번호입니다. Set for Transaction.PartialCancelled, Transaction.Cancelled and Transaction.CancelPending.
	CancellationID *string `json:"cancellationId,omitempty"`
	// 결제건의 결제 요청 금액입니다. Set for Transaction.Confirm.
	TotalAmount *int64 `json:"totalAmount,omitempty"`
}

// WebhookBillingKey is a billing key event (빌링키 발급 관련).
type WebhookBillingKey struct {
	Type WebhookType `json:"type"`
	// 해당 웹훅을 트리거한 이벤트의 발생 시각(RFC 3339 형식)입니다.
	Timestamp time.Time             `json:"timestamp"`
	Data      WebhookBillingKeyData `json:"data"`
}

func (*WebhookBillingKey) isWebhook() {}

// WebhookBillingKeyData identifies the billing key a [WebhookBillingKey] is about.
type WebhookBillingKeyData struct {
	// 포트원에서 채번한 빌링키입니다.
	BillingKey string `json:"billingKey"`
	// 웹훅을 트리거한 상점의 아이디입니다.
	StoreID string `json:"storeId"`
}

// WebhookTolerance is how far the webhook-timestamp may be from now (the same as PortOne's SDKs).
const WebhookTolerance = 5 * time.Minute

// Webhook verification errors.
var (
	ErrWebhookMissingHeaders = errors.New("portone: webhook headers missing")
	ErrWebhookBadSecret      = errors.New("portone: webhook secret is not valid base64")
	ErrWebhookTimestamp      = errors.New("portone: webhook timestamp is invalid")
	ErrWebhookTooOld         = errors.New("portone: webhook timestamp is too old")
	ErrWebhookTooNew         = errors.New("portone: webhook timestamp is in the future")
	ErrWebhookSignature      = errors.New("portone: webhook signature does not match")
	ErrWebhookPayload        = errors.New("portone: webhook payload is not valid json")
)

// WebhookVerifier checks the Standard Webhooks signature PortOne sends
// (headers webhook-id, webhook-timestamp, webhook-signature; HMAC-SHA256 over "id.timestamp.body").
// It accepts several secrets so a secret can be rotated without downtime. It is safe for concurrent use.
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
		key, err := decodeSecret(s)
		if err != nil {
			return nil, err
		}
		v.keys = append(v.keys, key)
	}
	if len(v.keys) == 0 {
		return nil, ErrWebhookBadSecret
	}
	return v, nil
}

// VerifyWebhook verifies a webhook with a single secret. See [WebhookVerifier.Verify].
func VerifyWebhook(secret string, body []byte, header http.Header) (Webhook, error) {
	v, err := NewWebhookVerifier(secret)
	if err != nil {
		return nil, err
	}
	return v.Verify(body, header)
}

// Verify checks the signature and timestamp of a webhook request and decodes the payload.
// body must be the raw request body, byte for byte.
func (v *WebhookVerifier) Verify(body []byte, header http.Header) (Webhook, error) {
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
	tolerance := int64(WebhookTolerance / time.Second)
	switch {
	case sec < now-tolerance:
		return nil, ErrWebhookTooOld
	case sec > now+tolerance:
		return nil, ErrWebhookTooNew
	}

	if !v.matches(id, ts, sigs, body) {
		return nil, ErrWebhookSignature
	}
	hook, err := decodeWebhook(body)
	if err != nil {
		return nil, ErrWebhookPayload
	}
	return hook, nil
}

func (v *WebhookVerifier) matches(id, ts, sigs string, body []byte) bool {
	for _, key := range v.keys {
		expected := sign(key, id, ts, body)
		for _, versioned := range strings.Split(sigs, " ") {
			version, sig, ok := strings.Cut(versioned, ",")
			if !ok || version != "v1" {
				continue
			}
			got, err := base64.StdEncoding.DecodeString(sig)
			if err == nil && hmac.Equal(got, expected) {
				return true
			}
		}
	}
	return false
}

func decodeWebhook(body []byte) (Webhook, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return nil, err
	}
	switch {
	case strings.HasPrefix(head.Type, "Transaction."):
		var w WebhookTransaction
		if err := json.Unmarshal(body, &w); err != nil {
			return nil, err
		}
		return &w, nil
	case strings.HasPrefix(head.Type, "BillingKey."):
		var w WebhookBillingKey
		if err := json.Unmarshal(body, &w); err != nil {
			return nil, err
		}
		return &w, nil
	}
	return newUnrecognizedVariant(head.Type, body), nil
}

// SignWebhook builds a webhook-signature header value ("v1,<base64>") for a payload, as PortOne does.
// Use it to test your webhook handler.
func SignWebhook(secret, id string, timestamp time.Time, body []byte) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	return "v1," + base64.StdEncoding.EncodeToString(sign(key, id, strconv.FormatInt(timestamp.Unix(), 10), body)), nil
}

func decodeSecret(secret string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil || len(key) == 0 {
		return nil, ErrWebhookBadSecret
	}
	return key, nil
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

// single returns the header value only if it appears exactly once (duplicates are rejected, as in PortOne's SDKs).
func single(h http.Header, name string) string {
	values := h.Values(name)
	if len(values) != 1 {
		return ""
	}
	return values[0]
}
