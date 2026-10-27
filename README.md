# portone

A small Go client for the [PortOne V2 REST API](https://developers.portone.io/api/rest-v2): the server-side half of a payment the browser SDK started.

- **Payments**: look a payment up to verify it, pre-register the expected amount, cancel (refund)
- **Escrow**: register / modify shipping, confirm the purchase
- **Webhooks**: [Standard Webhooks](https://github.com/standard-webhooks/standard-webhooks) signature verification, with secret rotation
- **Zero dependencies**: standard library only

## Installation

```bash
go get github.com/donghquinn/pg/portone
```

## Usage

```go
client := portone.New(os.Getenv("PORTONE_API_SECRET"),
	portone.WithStoreID(os.Getenv("PORTONE_STORE_ID")))
```

### Verify a payment before trusting the browser

The browser SDK reports success, but anyone can call your "payment done" endpoint. Look the payment up and compare it with your order.

```go
// Before opening the payment window: PortOne rejects a payment for any other amount.
err := client.PreRegisterPayment(ctx, order.PaymentID, portone.PreRegisterRequest{
	TotalAmount: order.Amount, Currency: portone.CurrencyKRW,
})

// After the browser returns (or on a webhook):
p, err := client.GetPayment(ctx, order.PaymentID)
switch {
case portone.IsNotFound(err):
	// never paid
case err != nil:
	// retry later
case p.IsPaid() && p.Amount.Total == order.Amount && p.Currency == portone.CurrencyKRW:
	// mark the order paid
}
```

### Cancel (refund)

```go
amount := order.Amount // what you believe is still cancellable
c, err := client.CancelPayment(ctx, paymentID, portone.CancelRequest{
	Reason:                   "판매자 거절",
	Requester:                portone.CancelRequesterAdmin,
	CurrentCancellableAmount: &amount, // refuses if someone else cancelled in between
})
```

Leave `Amount` nil to cancel everything that remains.

### Escrow

```go
// Seller shipped: BEFORE_REGISTERED → REGISTERED
_, err := client.RegisterEscrowLogistics(ctx, paymentID, portone.EscrowLogisticsRequest{
	Receiver:  &portone.EscrowReceiver{Name: "구매자", PhoneNumber: "01012345678", Zipcode: "06236"},
	Logistics: portone.Logistics{Company: portone.LogisticsCJ, InvoiceNumber: "123456789012", SentAt: time.Now()},
})

// Buyer pressed "구매 확정" in your app: the PG settles to the seller.
completedAt, err := client.ConfirmEscrow(ctx, paymentID, true)
```

`p.Escrow.Status` on a looked-up payment tells you where it is (`BEFORE_REGISTERED`, `REGISTERED`, `DELIVERED`, `CONFIRMED`, …). The PG may also confirm automatically after its deadline; `IsAutomaticallyConfirmed` is then true.

### Webhooks

Verify against the **raw** body; never re-encode JSON before verifying.

```go
verifier, err := portone.NewWebhookVerifier(os.Getenv("PORTONE_WEBHOOK_SECRET")) // pass two secrets while rotating

http.HandleFunc("/portone/webhook", func(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	hook, err := verifier.Verify(body, r.Header)
	if err != nil {
		http.Error(w, "invalid webhook", http.StatusBadRequest)
		return
	}
	if hook.IsTransaction() {
		// Data has only IDs: look the payment up with GetPayment before acting.
		syncPayment(r.Context(), hook.Data.PaymentID)
	}
	w.WriteHeader(http.StatusOK)
})
```

PortOne retries a failed delivery up to 5 times, and webhooks can arrive before or after the browser redirect, so make your handler idempotent. `SignWebhook` produces the same signature PortOne sends, for testing your handler.

### Errors

Non-2xx responses are `*portone.APIError` with the API's `Type` (`PAYMENT_NOT_FOUND`, `PG_PROVIDER`, …) and, for PG errors, `PgCode`/`PgMessage`:

```go
var apiErr *portone.APIError
if errors.As(err, &apiErr) && apiErr.Type == portone.ErrTypePgProvider {
	log.Printf("PG refused: %s %s", apiErr.PgCode, apiErr.PgMessage)
}
```

Network errors and context cancellation are returned as-is.

### Options

| Option | Default |
| --- | --- |
| `WithStoreID(id)` | none (required when one API secret serves several stores) |
| `WithBaseURL(url)` | `https://api.portone.io` |
| `WithHTTPClient(c)` | `http.Client` with a 30s timeout |
| `WithUserAgent(ua)` | `donghquinn-pg-portone` |

## Testing

```bash
go test -race ./...
```

Request and response shapes follow PortOne's published OpenAPI schema (`https://developers.portone.io/schema/v2.openapi.json`); the webhook signer is checked against the Standard Webhooks test vector.

## License

MIT
