package portone

import (
	"context"
	"net/http"
	"time"
)

// Escrow status values ("status" of Payment.Escrow).
const (
	EscrowStatusBeforeRegistered = "BEFORE_REGISTERED" // paid, shipping not registered yet
	EscrowStatusRegistered       = "REGISTERED"        // shipping registered
	EscrowStatusDelivered        = "DELIVERED"
	EscrowStatusConfirmed        = "CONFIRMED" // purchase confirmed (by buyer, store, or automatically)
	EscrowStatusRejected         = "REJECTED"  // buyer rejected the purchase
	EscrowStatusRejectConfirmed  = "REJECT_CONFIRMED"
	EscrowStatusCancelled        = "CANCELLED"
)

// Logistics companies accepted by the escrow API (a subset; see PaymentLogisticsCompany in the spec).
const (
	LogisticsCJ             = "CJ"
	LogisticsHanjin         = "HANJIN"
	LogisticsLotte          = "LOTTE"
	LogisticsLogen          = "LOGEN"
	LogisticsPost           = "POST"
	LogisticsPostRegistered = "POST_REGISTERED"
	LogisticsKyungdong      = "KYUNGDONG"
	LogisticsDaesin         = "DAESIN"
	LogisticsIlyang         = "ILYANG"
	LogisticsChunil         = "CHUNIL"
	LogisticsHapdong        = "HAPDONG"
	LogisticsGS             = "GS"
	LogisticsETC            = "ETC"
)

// Escrow is the escrow state of a payment. Company/InvoiceNumber are set once shipping is registered.
type Escrow struct {
	Status                   string     `json:"status"`
	Company                  string     `json:"company,omitempty"`
	InvoiceNumber            string     `json:"invoiceNumber,omitempty"`
	SentAt                   *time.Time `json:"sentAt,omitempty"`
	AppliedAt                *time.Time `json:"appliedAt,omitempty"`
	IsAutomaticallyConfirmed bool       `json:"isAutomaticallyConfirmed,omitempty"`
}

// Address is a separated address (line 1 = road/street, line 2 = detail).
type Address struct {
	AddressLine1 string `json:"addressLine1"`
	AddressLine2 string `json:"addressLine2"`
	City         string `json:"city,omitempty"`
	Province     string `json:"province,omitempty"`
	Country      string `json:"country,omitempty"`
}

type EscrowSender struct {
	Name         string   `json:"name,omitempty"`
	PhoneNumber  string   `json:"phoneNumber,omitempty"`
	Zipcode      string   `json:"zipcode,omitempty"`
	Relationship string   `json:"relationship,omitempty"`
	Address      *Address `json:"address,omitempty"`
}

type EscrowReceiver struct {
	Name        string   `json:"name,omitempty"`
	PhoneNumber string   `json:"phoneNumber,omitempty"`
	Zipcode     string   `json:"zipcode,omitempty"`
	Address     *Address `json:"address,omitempty"`
}

type Logistics struct {
	Company       string     `json:"company"`
	InvoiceNumber string     `json:"invoiceNumber"`
	SentAt        time.Time  `json:"sentAt"`
	ReceivedAt    *time.Time `json:"receivedAt,omitempty"`
	Address       *Address   `json:"address,omitempty"`
}

// EscrowLogisticsRequest registers (or modifies) shipping for an escrow payment.
type EscrowLogisticsRequest struct {
	StoreID   string          `json:"storeId,omitempty"`
	Sender    *EscrowSender   `json:"sender,omitempty"`
	Receiver  *EscrowReceiver `json:"receiver,omitempty"`
	Logistics Logistics       `json:"logistics"`
	SendEmail *bool           `json:"sendEmail,omitempty"`
	Products  []Product       `json:"products,omitempty"`
}

// EscrowLogisticsResult is returned after registering/modifying shipping.
// AppliedAt is set on register, ModifiedAt on modify.
type EscrowLogisticsResult struct {
	InvoiceNumber string     `json:"invoiceNumber"`
	SentAt        time.Time  `json:"sentAt"`
	AppliedAt     *time.Time `json:"appliedAt,omitempty"`
	ModifiedAt    *time.Time `json:"modifiedAt,omitempty"`
}

// RegisterEscrowLogistics tells the PG the goods were shipped (escrow: BEFORE_REGISTERED → REGISTERED).
func (c *Client) RegisterEscrowLogistics(ctx context.Context, paymentID string, req EscrowLogisticsRequest) (*EscrowLogisticsResult, error) {
	return c.escrowLogistics(ctx, http.MethodPost, paymentID, req)
}

// ModifyEscrowLogistics corrects registered shipping (e.g. a mistyped invoice number).
func (c *Client) ModifyEscrowLogistics(ctx context.Context, paymentID string, req EscrowLogisticsRequest) (*EscrowLogisticsResult, error) {
	return c.escrowLogistics(ctx, http.MethodPatch, paymentID, req)
}

func (c *Client) escrowLogistics(ctx context.Context, method, paymentID string, req EscrowLogisticsRequest) (*EscrowLogisticsResult, error) {
	if req.StoreID == "" {
		req.StoreID = c.storeID
	}
	var out EscrowLogisticsResult
	if err := c.do(ctx, method, paymentPath(paymentID, "/escrow/logistics"), nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ConfirmEscrow confirms the purchase so the PG settles to the seller.
// fromStore=true means the store confirms on the buyer's behalf (e.g. the buyer pressed "구매 확정" in your app).
func (c *Client) ConfirmEscrow(ctx context.Context, paymentID string, fromStore bool) (time.Time, error) {
	body := struct {
		StoreID   string `json:"storeId,omitempty"`
		FromStore bool   `json:"fromStore"`
	}{c.storeID, fromStore}
	var out struct {
		CompletedAt time.Time `json:"completedAt"`
	}
	if err := c.do(ctx, http.MethodPost, paymentPath(paymentID, "/escrow/complete"), nil, body, &out); err != nil {
		return time.Time{}, err
	}
	return out.CompletedAt, nil
}
