package portone

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeAPI records the last request and answers with a canned status and body.
type fakeAPI struct {
	t      *testing.T
	status int
	reply  string

	method, path, auth, userAgent, contentType string
	query                                      url.Values
	body                                       map[string]any
}

func newFakeAPI(t *testing.T, status int, reply string, opts ...Option) (*fakeAPI, *Client) {
	t.Helper()
	f := &fakeAPI{t: t, status: status, reply: reply}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	c, err := NewClient("secret-123", append([]Option{WithBaseURL(srv.URL + "/")}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.method, f.path, f.query = r.Method, r.URL.EscapedPath(), r.URL.Query()
	f.auth, f.userAgent, f.contentType = r.Header.Get("Authorization"), r.Header.Get("User-Agent"), r.Header.Get("Content-Type")
	f.body = nil
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		if err := json.Unmarshal(raw, &f.body); err != nil {
			f.t.Errorf("request body is not JSON: %s", raw)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.status)
	io.WriteString(w, f.reply)
}

const paidPayment = `{
  "status": "PAID", "id": "order/1", "transactionId": "tx-1", "merchantId": "m-1", "storeId": "store-abc",
  "method": {"type": "PaymentMethodCard", "approvalNumber": "12345678"},
  "channel": {"type": "TEST", "id": "ch-1", "key": "channel-key", "name": "nice", "pgProvider": "NICEPAY_V2", "pgMerchantId": "nictest"},
  "version": "V2", "orderName": "크레스티드 게코", "currency": "KRW",
  "amount": {"total": 150000, "taxFree": 0, "vat": 13636, "supply": 136364, "discount": 0, "paid": 150000, "cancelled": 0, "cancelledTaxFree": 0},
  "customer": {"id": "user-1", "name": "앨리스"},
  "origin": {"url": "https://shop.example", "userAgent": "test"},
  "requestedAt": "2026-10-27T01:00:00Z", "updatedAt": "2026-10-27T01:00:05Z", "statusChangedAt": "2026-10-27T01:00:05Z",
  "paidAt": "2026-10-27T01:00:05Z", "receiptUrl": "https://receipt.example/1",
  "disputes": [],
  "cancellations": [{"status": "SUCCEEDED", "id": "c-1", "totalAmount": 1000, "taxFreeAmount": 0, "vatAmount": 91, "reason": "test", "requestedAt": "2026-10-27T02:00:00Z"}],
  "someNewField": {"ignored": true}
}`

func TestNewClient(t *testing.T) {
	tests := []struct {
		name       string
		giveSecret string
		giveOpts   []Option
		wantErr    bool
	}{
		{name: "defaults", giveSecret: "s"},
		{name: "empty secret", giveSecret: "", wantErr: true},
		{name: "relative base url", giveSecret: "s", giveOpts: []Option{WithBaseURL("api.portone.io")}, wantErr: true},
		{name: "nil http client", giveSecret: "s", giveOpts: []Option{WithHTTPClient(nil)}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := NewClient(tt.giveSecret, tt.giveOpts...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && (c.Payment == nil || c.Payment.BillingKey == nil || c.Platform.Partner == nil) {
				t.Errorf("NewClient() left sub-clients nil: %+v", c)
			}
		})
	}
}

func TestClient_GetPayment(t *testing.T) {
	t.Parallel()
	f, c := newFakeAPI(t, http.StatusOK, paidPayment, WithStoreID("store-abc"))

	got, err := c.Payment.GetPayment(context.Background(), "order/1")
	if err != nil {
		t.Fatal(err)
	}

	if f.method != http.MethodGet || f.path != "/payments/order%2F1" || f.query.Get("storeId") != "store-abc" {
		t.Errorf("request = %s %s?%s, want GET /payments/order%%2F1?storeId=store-abc", f.method, f.path, f.query.Encode())
	}
	if f.auth != "PortOne secret-123" || f.userAgent != "go-portone/"+Version {
		t.Errorf("headers: Authorization = %q, User-Agent = %q", f.auth, f.userAgent)
	}

	paid, ok := got.(*PaidPayment)
	if !ok {
		t.Fatalf("GetPayment() = %T, want *PaidPayment", got)
	}
	if paid.ID != "order/1" || paid.Amount.Total != 150000 || paid.Currency != CurrencyKrw || paid.PaidAt.IsZero() {
		t.Errorf("payment fields: %+v", paid)
	}
	if card, ok := paid.Method.(*PaymentMethodCard); !ok || card.ApprovalNumber == nil || *card.ApprovalNumber != "12345678" {
		t.Errorf("Method = %#v, want *PaymentMethodCard with approval number", paid.Method)
	}
	if len(paid.Cancellations) != 1 {
		t.Fatalf("Cancellations = %d, want 1", len(paid.Cancellations))
	}
	if cancel, ok := paid.Cancellations[0].(*SucceededPaymentCancellation); !ok || cancel.TotalAmount != 1000 {
		t.Errorf("Cancellations[0] = %#v, want *SucceededPaymentCancellation of 1000", paid.Cancellations[0])
	}
}

func TestClient_CancelPayment(t *testing.T) {
	t.Parallel()
	const reply = `{"cancellation": {"status": "REQUESTED", "id": "c-1", "totalAmount": 500, "taxFreeAmount": 0, "vatAmount": 45, "reason": "변심", "requestedAt": "2026-10-27T02:00:00Z"}}`
	tests := []struct {
		name         string
		giveStoreID  *string
		wantStoreID  any
		wantClientID string
	}{
		{name: "store id from client", wantStoreID: "store-abc"},
		{name: "store id from request", giveStoreID: Ptr("store-other"), wantStoreID: "store-other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, c := newFakeAPI(t, http.StatusOK, reply, WithStoreID("store-abc"))

			got, err := c.Payment.CancelPayment(context.Background(), "order-1", CancelPaymentBody{
				StoreID:                  tt.giveStoreID,
				Amount:                   Ptr(int64(500)),
				Reason:                   "변심",
				Requester:                Ptr(CancelRequesterAdmin),
				CurrentCancellableAmount: Ptr(int64(150000)),
			})
			if err != nil {
				t.Fatal(err)
			}

			if f.method != http.MethodPost || f.path != "/payments/order-1/cancel" || f.contentType != "application/json" {
				t.Errorf("request = %s %s (%s)", f.method, f.path, f.contentType)
			}
			want := map[string]any{"storeId": tt.wantStoreID, "amount": 500.0, "reason": "변심", "requester": "ADMIN", "currentCancellableAmount": 150000.0}
			if !jsonEqual(f.body, want) {
				t.Errorf("body = %v, want %v", f.body, want)
			}
			if r, ok := got.Cancellation.(*RequestedPaymentCancellation); !ok || r.TotalAmount != 500 {
				t.Errorf("Cancellation = %#v, want *RequestedPaymentCancellation of 500", got.Cancellation)
			}
		})
	}
}

func TestClient_bodyInQuery(t *testing.T) {
	t.Parallel()
	f, c := newFakeAPI(t, http.StatusOK, `{"items": [], "page": {"number": 0, "size": 10, "totalCount": 0}}`)

	_, err := c.Payment.GetPayments(context.Background(), GetPaymentsBody{Page: &PageInput{Number: Ptr(int32(0)), Size: Ptr(int32(10))}})
	if err != nil {
		t.Fatal(err)
	}

	if f.method != http.MethodGet || f.body != nil {
		t.Errorf("request = %s with body %v, want GET without body", f.method, f.body)
	}
	if got, want := f.query.Get("requestBody"), `{"page":{"number":0,"size":10}}`; got != want {
		t.Errorf("requestBody query = %s, want %s", got, want)
	}
}

func TestClient_queryParams(t *testing.T) {
	t.Parallel()
	f, c := newFakeAPI(t, http.StatusOK, `{"deletedAt": "2026-10-27T02:00:00Z"}`)

	_, err := c.Payment.BillingKey.DeleteBillingKey(context.Background(), "bk-1", &DeleteBillingKeyParams{
		Reason:      Ptr("탈퇴"),
		Requester:   Ptr(BillingKeyDeleteRequesterAdmin),
		SkipWebhook: Ptr(true),
	})
	if err != nil {
		t.Fatal(err)
	}

	want := url.Values{"reason": {"탈퇴"}, "requester": {"ADMIN"}, "skipWebhook": {"true"}}
	if f.method != http.MethodDelete || f.path != "/billing-keys/bk-1" || f.query.Encode() != want.Encode() {
		t.Errorf("request = %s %s?%s, want DELETE /billing-keys/bk-1?%s", f.method, f.path, f.query.Encode(), want.Encode())
	}

	if _, err := c.Payment.BillingKey.DeleteBillingKey(context.Background(), "bk-1", nil); err != nil || len(f.query) != 0 {
		t.Errorf("nil params: err = %v, query = %v", err, f.query)
	}
}

func TestClient_enumPathParam(t *testing.T) {
	t.Parallel()
	f, c := newFakeAPI(t, http.StatusOK, `{"holderName": "홍길동", "accountVerificationId": "v-1"}`)

	_, err := c.Platform.Account.GetPlatformAccountHolder(context.Background(), BankKookmin, "123-456", nil)
	if err != nil {
		t.Fatal(err)
	}

	if f.path != "/platform/bank-accounts/KOOKMIN/123-456/holder" {
		t.Errorf("path = %s", f.path)
	}
}

func TestClient_csvResponse(t *testing.T) {
	t.Parallel()
	const sheet = "id,amount\nt-1,1000\n"
	_, c := newFakeAPI(t, http.StatusOK, sheet)

	got, err := c.Platform.Transfer.DownloadPlatformTransferSheet(context.Background(), DownloadPlatformTransferSheetBody{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != sheet {
		t.Errorf("DownloadPlatformTransferSheet() = %q, want %q", got, sheet)
	}
}

func TestClient_decodeError(t *testing.T) {
	t.Parallel()
	_, c := newFakeAPI(t, http.StatusOK, `{"status": "PAID", "amount": "not a number"}`)

	_, err := c.Payment.GetPayment(context.Background(), "order-1")

	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Errorf("GetPayment() error = %v, want a decode error", err)
	}
}

func jsonEqual(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}
