# go-portone

**English** | [한국어](./README.ko.md)

[![Go Reference](https://pkg.go.dev/badge/github.com/donghquinn/go-portone.svg)](https://pkg.go.dev/github.com/donghquinn/go-portone) [![Test](https://img.shields.io/github/actions/workflow/status/donghquinn/go-portone/test.yml?branch=main)](https://github.com/donghquinn/go-portone/actions) [![Go Version](https://img.shields.io/github/go-mod/go-version/donghquinn/go-portone)](https://go.dev/) [![License](https://img.shields.io/github/license/donghquinn/go-portone)](./LICENSE)

A Go server SDK for the [PortOne V2 REST API](https://developers.portone.io/api/rest-v2), generated from the same OpenAPI document as PortOne's official [server SDKs](https://github.com/portone-io/server-sdk) (JavaScript, Python, JVM). Unofficial; not maintained by PortOne.

- All 142 REST operations the official SDKs expose, grouped the same way (`client.Payment.BillingKey...`)
- Typed requests, responses and errors, with Korean doc comments from the spec
- Webhook verification ([Standard Webhooks](https://github.com/standard-webhooks/standard-webhooks)) with secret rotation
- Standard library only, Go 1.22+

## Getting started

```bash
go get github.com/donghquinn/go-portone
```

```go
client, err := portone.NewClient(os.Getenv("PORTONE_API_SECRET"))
if err != nil {
	log.Fatal(err)
}

payment, err := client.Payment.GetPayment(ctx, paymentID)
if err != nil {
	return err
}
if paid, ok := payment.(*portone.PaidPayment); ok && paid.Amount.Total == order.Amount {
	// mark the order paid
}
```

## Usage

### Clients

`Client` groups the APIs like the official SDKs: `Auth`, `B2b` (`Counterparty`, `TaxInvoice`), `IdentityVerification`, `Payment` (`AdditionalFeature`, `BillingKey`, `CashReceipt`, `PaymentSchedule`, `Promotion`), `PgSpecific`, `Platform` (`Account`, `AccountTransfer`, `BulkAccountTransfer`, `BulkPayout`, `Company`, `Partner`, `PartnerSettlement`, `Payout`, `Policy`, `Transfer`) and `Reconciliation`. Method names are the API's operation IDs: `getPayment` → `GetPayment`.

A method takes path parameters, then the request body, then the query parameters (a `*XxxParams`, which may be `nil`):

```go
res, err := client.Payment.CancelPayment(ctx, paymentID, portone.CancelPaymentBody{
	Reason:                   "고객 요청",
	Amount:                   portone.Ptr(int64(5000)),    // nil cancels everything left
	CurrentCancellableAmount: portone.Ptr(int64(150000)), // refuse if someone cancelled in between
})

_, err = client.Payment.BillingKey.DeleteBillingKey(ctx, billingKey, &portone.DeleteBillingKeyParams{
	Reason: portone.Ptr("회원 탈퇴"),
})
```

Optional fields are pointers; `portone.Ptr` makes one from a value.

### Store ID

With `portone.WithStoreID(id)`, every request that takes a store ID sends it. A request body that sets `StoreID` itself wins, so a platform can address several stores with one client.

### One-of types

The API's one-of types (payment statuses, payment methods, cancellations, …) are interfaces. Use a type switch:

```go
switch p := payment.(type) {
case *portone.PaidPayment:
	// p.PaidAt, p.Method, p.ReceiptURL, …
case *portone.VirtualAccountIssuedPayment:
	// waiting for the deposit
case *portone.CancelledPayment, *portone.PartialCancelledPayment:
	// refunded
case *portone.UnrecognizedVariant:
	// a status added after this SDK was generated: p.Discriminator, p.Raw
}
```

`UnrecognizedVariant` keeps an unknown variant as received, so an API change does not break decoding. Values re-encode with `json.Marshal`, discriminator included.

### Errors

A non-2xx response is an `*APIError` (status code, `Type`, `Message`, raw `Body`). When the type is one the operation documents, it wraps a typed error, reachable with `errors.As`:

```go
var notFound *portone.PaymentNotFoundError
var pgErr *portone.PgProviderError
switch {
case errors.As(err, &notFound):
	// unknown payment ID
case errors.As(err, &pgErr):
	log.Printf("PG refused: %s %s", pgErr.PgCode, pgErr.PgMessage)
case err != nil:
	// network error, context cancelled, or another *APIError
}
```

Each method's doc comment lists the error types it can return.

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
	if tx, ok := hook.(*portone.WebhookTransaction); ok {
		// Webhooks carry only IDs: look the payment up before acting.
		syncPayment(r.Context(), tx.Data.PaymentID)
	}
	w.WriteHeader(http.StatusOK)
})
```

PortOne retries failed deliveries, and a webhook can arrive before or after the browser redirect, so make the handler idempotent. `SignWebhook` produces the signature PortOne sends, for testing handlers.

### Options

| Option | Default |
| --- | --- |
| `WithStoreID(id)` | none |
| `WithBaseURL(url)` | `https://api.portone.io` |
| `WithHTTPClient(c)` | `http.Client` with a 30s timeout |
| `WithUserAgent(ua)` | `go-portone/<module version>`, e.g. `go-portone/v1.0.2` |

## Differences from the official SDKs

- Request bodies are the spec's named body types (`CancelPaymentBody`) instead of flattened keyword arguments.
- Errors are decoded through each operation's own error one-of, following the spec exactly.
- Webhooks decode into `WebhookTransaction` / `WebhookBillingKey` with a `Type` field rather than one type per event, and any `Transaction.*` or `BillingKey.*` type added later still decodes.
- `getBankInfos` is left out, as in the official SDKs.

## Development

The `*.gen.go` files come from [`internal/codegen`](./internal/codegen), which reads `internal/codegen/openapi.json`, a copy of [upstream's spec](https://github.com/portone-io/server-sdk/blob/main/codegen/openapi.json). To pick up API changes:

```bash
curl -fsSL https://raw.githubusercontent.com/portone-io/server-sdk/main/codegen/openapi.json -o internal/codegen/openapi.json
go generate ./...
go vet ./... && go test -race ./...
```

`SpecVersion` holds the spec version the package was generated from. Never edit `*.gen.go` by hand; CI fails when they differ from the generator's output.

To release, add the version to `CHANGELOG.md`, merge to `main`, then push a tag (`git tag v1.2.0 && git push origin v1.2.0`). The release workflow runs the tests, creates the GitHub Release and publishes the version to the Go module proxy. The User-Agent picks the version up from the build, so there is no version constant to bump. `govulncheck` runs on every push and weekly.

## License

[MIT](./LICENSE). The OpenAPI document is PortOne's, distributed under MIT/Apache-2.0 with their server SDKs; see [NOTICE](./NOTICE).
