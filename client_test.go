package portone

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fake records the last request and answers with a canned status/body.
type fake struct {
	method, path, query, auth, contentType string
	body                                   map[string]any
	status                                 int
	reply                                  string
}

func (f *fake) server(t *testing.T) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.method, f.path, f.query = r.Method, r.URL.EscapedPath(), r.URL.RawQuery
		f.auth, f.contentType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		f.body = nil
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			if err := json.Unmarshal(raw, &f.body); err != nil {
				t.Errorf("request body is not JSON: %s", raw)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		io.WriteString(w, f.reply)
	}))
	t.Cleanup(srv.Close)
	return srv, New("secret-123", WithStoreID("store-abc"), WithBaseURL(srv.URL+"/"))
}

const paidPayment = `{
  "status": "PAID", "id": "order/1", "transactionId": "tx-1", "merchantId": "m-1", "storeId": "store-abc",
  "channel": {"type": "TEST", "id": "ch-1", "key": "channel-key", "name": "nice", "pgProvider": "NICEPAY_V2", "pgMerchantId": "nictest"},
  "orderName": "크레스티드 게코", "currency": "KRW",
  "amount": {"total": 150000, "taxFree": 0, "vat": 13636, "supply": 136364, "discount": 0, "paid": 150000, "cancelled": 0, "cancelledTaxFree": 0},
  "customer": {"id": "user-1", "name": "앨리스"},
  "escrow": {"status": "BEFORE_REGISTERED"},
  "method": {"type": "PaymentMethodCard", "card": {"publisher": "KB"}},
  "requestedAt": "2026-10-27T01:00:00Z", "updatedAt": "2026-10-27T01:00:05Z", "statusChangedAt": "2026-10-27T01:00:05Z",
  "paidAt": "2026-10-27T01:00:05Z", "receiptUrl": "https://receipt.example/1",
  "version": "V2", "origin": {}, "disputes": [], "someNewField": {"ignored": true}
}`

func TestGetPayment(t *testing.T) {
	f := &fake{status: 200, reply: paidPayment}
	_, c := f.server(t)

	p, err := c.GetPayment(context.Background(), "order/1")
	if err != nil {
		t.Fatal(err)
	}
	if f.method != http.MethodGet || f.path != "/payments/order%2F1" || f.query != "storeId=store-abc" {
		t.Errorf("request: %s %s ?%s", f.method, f.path, f.query)
	}
	if f.auth != "PortOne secret-123" {
		t.Errorf("auth header: %q", f.auth)
	}
	if !p.IsPaid() || p.Amount.Total != 150000 || p.Currency != CurrencyKRW || p.Customer.ID != "user-1" {
		t.Errorf("payment: %+v", p)
	}
	if p.Escrow == nil || p.Escrow.Status != EscrowStatusBeforeRegistered || p.PaidAt == nil || p.Channel.Type != "TEST" {
		t.Errorf("escrow/paidAt/channel: %+v %v %+v", p.Escrow, p.PaidAt, p.Channel)
	}
	if p.MethodType() != "PaymentMethodCard" {
		t.Errorf("method type: %q", p.MethodType())
	}
}

func TestErrors(t *testing.T) {
	f := &fake{status: 404, reply: `{"type": "PAYMENT_NOT_FOUND", "message": "결제 건이 존재하지 않습니다."}`}
	_, c := f.server(t)

	_, err := c.GetPayment(context.Background(), "nope")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 || !IsNotFound(err) {
		t.Fatalf("want PAYMENT_NOT_FOUND APIError, got %v", err)
	}

	f.status, f.reply = 502, `{"type": "PG_PROVIDER", "message": "PG사에서 오류를 전달했습니다.", "pgCode": "F113", "pgMessage": "한도 초과"}`
	_, err = c.CancelPayment(context.Background(), "p", CancelRequest{Reason: "x"})
	if !IsErrorType(err, ErrTypePgProvider) || !strings.Contains(err.Error(), "F113") {
		t.Errorf("pg error: %v", err)
	}

	// A proxy's HTML error page must not be lost.
	f.status, f.reply = 503, `<html>bad gateway</html>`
	_, err = c.GetPayment(context.Background(), "p")
	if !errors.As(err, &apiErr) || apiErr.Type != "UNKNOWN" || !strings.Contains(apiErr.Message, "bad gateway") {
		t.Errorf("non-JSON error: %v", err)
	}

	// Network failures surface as plain errors (not APIError).
	c2 := New("s", WithBaseURL("http://127.0.0.1:1"), WithHTTPClient(&http.Client{Timeout: time.Second}))
	if _, err := c2.GetPayment(context.Background(), "p"); err == nil || errors.As(err, &apiErr) {
		t.Errorf("network error: %v", err)
	}
}

func TestPreRegisterAndCancel(t *testing.T) {
	f := &fake{status: 200, reply: `{}`}
	_, c := f.server(t)

	if err := c.PreRegisterPayment(context.Background(), "p-1", PreRegisterRequest{TotalAmount: 150000, Currency: CurrencyKRW}); err != nil {
		t.Fatal(err)
	}
	if f.method != http.MethodPost || f.path != "/payments/p-1/pre-register" || f.contentType != "application/json" ||
		f.body["storeId"] != "store-abc" || f.body["totalAmount"] != float64(150000) || f.body["currency"] != "KRW" {
		t.Errorf("pre-register: %s %s %v", f.method, f.path, f.body)
	}
	if _, ok := f.body["taxFreeAmount"]; ok {
		t.Error("unset optional fields must be omitted")
	}

	f.reply = `{"cancellation": {"status": "SUCCEEDED", "id": "c-1", "totalAmount": 150000, "taxFreeAmount": 0, "vatAmount": 13636,
		"reason": "판매자 거절", "requestedAt": "2026-10-27T02:00:00Z", "cancelledAt": "2026-10-27T02:00:01Z"}}`
	cancellable := int64(150000)
	got, err := c.CancelPayment(context.Background(), "p-1", CancelRequest{Reason: "판매자 거절", Requester: CancelRequesterAdmin, CurrentCancellableAmount: &cancellable})
	if err != nil {
		t.Fatal(err)
	}
	if f.path != "/payments/p-1/cancel" || f.body["reason"] != "판매자 거절" || f.body["requester"] != "ADMIN" ||
		f.body["currentCancellableAmount"] != float64(150000) || f.body["storeId"] != "store-abc" {
		t.Errorf("cancel request: %v", f.body)
	}
	if _, ok := f.body["amount"]; ok {
		t.Error("full cancel must omit amount")
	}
	if got.Status != CancellationStatusSucceeded || got.TotalAmount != 150000 || got.CancelledAt == nil {
		t.Errorf("cancellation: %+v", got)
	}
}

func TestEscrow(t *testing.T) {
	f := &fake{status: 200, reply: `{"invoiceNumber": "123456789012", "sentAt": "2026-10-27T03:00:00Z", "appliedAt": "2026-10-27T03:00:02Z"}`}
	_, c := f.server(t)

	sent := time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC)
	req := EscrowLogisticsRequest{
		Sender:    &EscrowSender{Name: "판매자", PhoneNumber: "01011112222"},
		Receiver:  &EscrowReceiver{Name: "구매자", PhoneNumber: "01033334444", Zipcode: "06236", Address: &Address{AddressLine1: "서울 강남구 테헤란로 1", AddressLine2: "101호"}},
		Logistics: Logistics{Company: LogisticsCJ, InvoiceNumber: "123456789012", SentAt: sent},
	}
	res, err := c.RegisterEscrowLogistics(context.Background(), "p-1", req)
	if err != nil {
		t.Fatal(err)
	}
	logistics, _ := f.body["logistics"].(map[string]any)
	receiver, _ := f.body["receiver"].(map[string]any)
	if f.method != http.MethodPost || f.path != "/payments/p-1/escrow/logistics" || logistics["company"] != "CJ" ||
		logistics["sentAt"] != "2026-10-27T03:00:00Z" || receiver["zipcode"] != "06236" || f.body["storeId"] != "store-abc" {
		t.Errorf("register logistics: %s %s %v", f.method, f.path, f.body)
	}
	if res.InvoiceNumber != "123456789012" || res.AppliedAt == nil {
		t.Errorf("register result: %+v", res)
	}

	f.reply = `{"invoiceNumber": "999", "sentAt": "2026-10-27T03:00:00Z", "modifiedAt": "2026-10-27T04:00:00Z"}`
	req.Logistics.InvoiceNumber = "999"
	if res, err := c.ModifyEscrowLogistics(context.Background(), "p-1", req); err != nil || f.method != http.MethodPatch || res.ModifiedAt == nil {
		t.Errorf("modify: %s %+v %v", f.method, res, err)
	}

	f.reply = `{"completedAt": "2026-10-28T00:00:00Z"}`
	done, err := c.ConfirmEscrow(context.Background(), "p-1", true)
	if err != nil || f.path != "/payments/p-1/escrow/complete" || f.body["fromStore"] != true || f.body["storeId"] != "store-abc" || done.IsZero() {
		t.Errorf("confirm: %s %v %v %v", f.path, f.body, done, err)
	}
}

func TestContextCancel(t *testing.T) {
	f := &fake{status: 200, reply: paidPayment}
	_, c := f.server(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetPayment(ctx, "p"); !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
}
