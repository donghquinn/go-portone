package portone

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// Payment status values ("status" of the Payment union).
// Note the API reports a pending payment as "PAY_PENDING".
const (
	PaymentStatusReady                = "READY"
	PaymentStatusPayPending           = "PAY_PENDING"
	PaymentStatusVirtualAccountIssued = "VIRTUAL_ACCOUNT_ISSUED"
	PaymentStatusPaid                 = "PAID"
	PaymentStatusFailed               = "FAILED"
	PaymentStatusPartialCancelled     = "PARTIAL_CANCELLED"
	PaymentStatusCancelled            = "CANCELLED"
)

// CurrencyKRW is the Korean won. The REST API uses ISO codes ("KRW"); the browser SDK uses "CURRENCY_KRW".
const CurrencyKRW = "KRW"

// Payment is one payment attempt group (merchant paymentId). The API returns a union keyed by
// Status; fields that only some variants have are pointers / omitempty here.
type Payment struct {
	Status          string         `json:"status"`
	ID              string         `json:"id"`
	TransactionID   string         `json:"transactionId"`
	MerchantID      string         `json:"merchantId"`
	StoreID         string         `json:"storeId"`
	Channel         *Channel       `json:"channel,omitempty"`
	OrderName       string         `json:"orderName"`
	Amount          PaymentAmount  `json:"amount"`
	Currency        string         `json:"currency"`
	Customer        Customer       `json:"customer"`
	Escrow          *Escrow        `json:"escrow,omitempty"`
	Products        []Product      `json:"products,omitempty"`
	CustomData      string         `json:"customData,omitempty"`
	RequestedAt     time.Time      `json:"requestedAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
	StatusChangedAt time.Time      `json:"statusChangedAt"`
	PaidAt          *time.Time     `json:"paidAt,omitempty"`
	CancelledAt     *time.Time     `json:"cancelledAt,omitempty"`
	FailedAt        *time.Time     `json:"failedAt,omitempty"`
	Failure         *Failure       `json:"failure,omitempty"`
	PgTxID          string         `json:"pgTxId,omitempty"`
	ReceiptURL      string         `json:"receiptUrl,omitempty"`
	Cancellations   []Cancellation `json:"cancellations,omitempty"`
	// Method is the payment method union, kept raw; MethodType() reads its "type".
	Method json.RawMessage `json:"method,omitempty"`
}

// MethodType returns the payment method type (e.g. "PaymentMethodCard", "PaymentMethodEasyPay"), or "".
func (p *Payment) MethodType() string {
	var m struct {
		Type string `json:"type"`
	}
	if len(p.Method) == 0 || json.Unmarshal(p.Method, &m) != nil {
		return ""
	}
	return m.Type
}

// IsPaid reports whether the payment is fully paid (not cancelled).
func (p *Payment) IsPaid() bool { return p.Status == PaymentStatusPaid }

type PaymentAmount struct {
	Total            int64 `json:"total"`
	TaxFree          int64 `json:"taxFree"`
	Vat              int64 `json:"vat,omitempty"`
	Supply           int64 `json:"supply,omitempty"`
	Discount         int64 `json:"discount"`
	Paid             int64 `json:"paid"`
	Cancelled        int64 `json:"cancelled"`
	CancelledTaxFree int64 `json:"cancelledTaxFree"`
}

type Channel struct {
	Type         string `json:"type"` // LIVE | TEST
	ID           string `json:"id,omitempty"`
	Key          string `json:"key,omitempty"`
	Name         string `json:"name,omitempty"`
	PgProvider   string `json:"pgProvider"`
	PgMerchantID string `json:"pgMerchantId"`
}

type Customer struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Email       string `json:"email,omitempty"`
	PhoneNumber string `json:"phoneNumber,omitempty"`
	Zipcode     string `json:"zipcode,omitempty"`
}

type Failure struct {
	Reason    string `json:"reason,omitempty"`
	PgCode    string `json:"pgCode,omitempty"`
	PgMessage string `json:"pgMessage,omitempty"`
}

// Product is a line item (also used when registering escrow logistics).
type Product struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Tag      string `json:"tag,omitempty"`
	Code     string `json:"code,omitempty"`
	Amount   int64  `json:"amount"`
	Quantity int32  `json:"quantity"`
	Link     string `json:"link,omitempty"`
}

// Cancellation status values.
const (
	CancellationStatusRequested = "REQUESTED"
	CancellationStatusSucceeded = "SUCCEEDED"
	CancellationStatusFailed    = "FAILED"
)

type Cancellation struct {
	Status           string     `json:"status"`
	ID               string     `json:"id"`
	PgCancellationID string     `json:"pgCancellationId,omitempty"`
	TotalAmount      int64      `json:"totalAmount"`
	TaxFreeAmount    int64      `json:"taxFreeAmount"`
	VatAmount        int64      `json:"vatAmount"`
	Reason           string     `json:"reason"`
	RequestedAt      time.Time  `json:"requestedAt"`
	CancelledAt      *time.Time `json:"cancelledAt,omitempty"`
	ReceiptURL       string     `json:"receiptUrl,omitempty"`
}

// GetPayment looks a payment up by the merchant-assigned payment ID.
// Use it to verify what the browser reported (status, amount, currency) before trusting it.
func (c *Client) GetPayment(ctx context.Context, paymentID string) (*Payment, error) {
	var q url.Values
	if c.storeID != "" {
		q = url.Values{"storeId": {c.storeID}}
	}
	var p Payment
	if err := c.do(ctx, http.MethodGet, paymentPath(paymentID, ""), q, nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// PreRegisterRequest is the amount the payment must match. PortOne rejects a payment
// whose amount differs, so a tampered browser request cannot pay less.
type PreRegisterRequest struct {
	StoreID       string `json:"storeId,omitempty"`
	TotalAmount   int64  `json:"totalAmount"`
	TaxFreeAmount *int64 `json:"taxFreeAmount,omitempty"`
	Currency      string `json:"currency,omitempty"`
}

// PreRegisterPayment registers the expected amount before the browser requests the payment.
func (c *Client) PreRegisterPayment(ctx context.Context, paymentID string, req PreRegisterRequest) error {
	if req.StoreID == "" {
		req.StoreID = c.storeID
	}
	return c.do(ctx, http.MethodPost, paymentPath(paymentID, "/pre-register"), nil, req, nil)
}

// Cancel requester values.
const (
	CancelRequesterCustomer = "CUSTOMER"
	CancelRequesterAdmin    = "ADMIN"
)

// CancelRequest cancels (refunds) all or part of a payment. Amount nil means the whole remaining amount.
// CurrentCancellableAmount, when set, makes the API refuse the cancel if someone else cancelled in between.
type CancelRequest struct {
	StoreID                  string `json:"storeId,omitempty"`
	Amount                   *int64 `json:"amount,omitempty"`
	TaxFreeAmount            *int64 `json:"taxFreeAmount,omitempty"`
	VatAmount                *int64 `json:"vatAmount,omitempty"`
	Reason                   string `json:"reason"`
	Requester                string `json:"requester,omitempty"`
	CurrentCancellableAmount *int64 `json:"currentCancellableAmount,omitempty"`
	RefundEmail              string `json:"refundEmail,omitempty"`
	SkipWebhook              *bool  `json:"skipWebhook,omitempty"`
}

// CancelPayment cancels a payment and returns the cancellation (which may still be REQUESTED for some PGs).
func (c *Client) CancelPayment(ctx context.Context, paymentID string, req CancelRequest) (*Cancellation, error) {
	if req.StoreID == "" {
		req.StoreID = c.storeID
	}
	var out struct {
		Cancellation Cancellation `json:"cancellation"`
	}
	if err := c.do(ctx, http.MethodPost, paymentPath(paymentID, "/cancel"), nil, req, &out); err != nil {
		return nil, err
	}
	return &out.Cancellation, nil
}
