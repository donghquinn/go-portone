# go-portone

[English](./README.md) | **한국어**

[![Go Reference](https://pkg.go.dev/badge/github.com/donghquinn/go-portone.svg)](https://pkg.go.dev/github.com/donghquinn/go-portone) [![Test](https://img.shields.io/github/actions/workflow/status/donghquinn/go-portone/test.yml?branch=main)](https://github.com/donghquinn/go-portone/actions) [![Go Version](https://img.shields.io/github/go-mod/go-version/donghquinn/go-portone)](https://go.dev/) [![License](https://img.shields.io/github/license/donghquinn/go-portone)](./LICENSE)

[포트원 V2 REST API](https://developers.portone.io/api/rest-v2)용 Go 서버 SDK입니다. 포트원 공식 [서버 SDK](https://github.com/portone-io/server-sdk)(JavaScript, Python, JVM)와 같은 OpenAPI 문서로부터 생성됩니다. 비공식 SDK이며 포트원이 관리하지 않습니다.

- 공식 SDK가 제공하는 REST API 142개 전체를 같은 구조로 제공합니다 (`client.Payment.BillingKey...`)
- 요청, 응답, 에러가 모두 타입으로 제공되며, 스펙의 한국어 설명이 문서 주석으로 들어 있습니다
- 웹훅 검증([Standard Webhooks](https://github.com/standard-webhooks/standard-webhooks))과 시크릿 교체를 지원합니다
- 표준 라이브러리만 사용하며 Go 1.22 이상에서 동작합니다

## 시작하기

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
	// 주문을 결제 완료로 처리
}
```

## 사용법

### 클라이언트

`Client`는 공식 SDK와 같은 방식으로 API를 묶습니다: `Auth`, `B2b` (`Counterparty`, `TaxInvoice`), `IdentityVerification`, `Payment` (`AdditionalFeature`, `BillingKey`, `CashReceipt`, `PaymentSchedule`, `Promotion`), `PgSpecific`, `Platform` (`Account`, `AccountTransfer`, `BulkAccountTransfer`, `BulkPayout`, `Company`, `Partner`, `PartnerSettlement`, `Payout`, `Policy`, `Transfer`), `Reconciliation`. 메서드 이름은 API의 operation ID입니다: `getPayment` → `GetPayment`.

메서드는 경로 파라미터, 요청 본문, 쿼리 파라미터 순서로 인자를 받습니다. 쿼리 파라미터는 `*XxxParams`이며 `nil`을 넘길 수 있습니다.

```go
res, err := client.Payment.CancelPayment(ctx, paymentID, portone.CancelPaymentBody{
	Reason:                   "고객 요청",
	Amount:                   portone.Ptr(int64(5000)),    // nil이면 남은 금액 전체를 취소
	CurrentCancellableAmount: portone.Ptr(int64(150000)), // 그 사이 다른 취소가 있었다면 거절
})

_, err = client.Payment.BillingKey.DeleteBillingKey(ctx, billingKey, &portone.DeleteBillingKeyParams{
	Reason: portone.Ptr("회원 탈퇴"),
})
```

선택 필드는 포인터입니다. `portone.Ptr`로 값에서 포인터를 만들 수 있습니다.

### 상점 ID

`portone.WithStoreID(id)`를 지정하면 상점 ID를 받는 모든 요청에 이 값을 보냅니다. 요청 본문에 `StoreID`를 직접 지정하면 그 값이 우선하므로, 플랫폼은 클라이언트 하나로 여러 상점을 다룰 수 있습니다.

### One-of 타입

결제 상태, 결제 수단, 취소 내역처럼 API의 one-of 타입은 인터페이스입니다. 타입 스위치로 구분합니다.

```go
switch p := payment.(type) {
case *portone.PaidPayment:
	// p.PaidAt, p.Method, p.ReceiptURL, …
case *portone.VirtualAccountIssuedPayment:
	// 입금 대기
case *portone.CancelledPayment, *portone.PartialCancelledPayment:
	// 환불됨
case *portone.UnrecognizedVariant:
	// SDK 생성 이후 추가된 상태: p.Discriminator, p.Raw
}
```

`UnrecognizedVariant`는 알 수 없는 variant를 받은 그대로 보관하므로 API가 바뀌어도 디코딩이 실패하지 않습니다. 값은 `json.Marshal`로 다시 인코딩되며 discriminator도 포함됩니다.

### 에러

2xx가 아닌 응답은 `*APIError`(상태 코드, `Type`, `Message`, 원본 `Body`)로 반환됩니다. 해당 API가 문서화한 에러 타입이면 타입이 있는 에러를 감싸고 있어 `errors.As`로 꺼낼 수 있습니다.

```go
var notFound *portone.PaymentNotFoundError
var pgErr *portone.PgProviderError
switch {
case errors.As(err, &notFound):
	// 존재하지 않는 결제 ID
case errors.As(err, &pgErr):
	log.Printf("PG사 거절: %s %s", pgErr.PgCode, pgErr.PgMessage)
case err != nil:
	// 네트워크 에러, 컨텍스트 취소, 또는 다른 *APIError
}
```

각 메서드의 문서 주석에 반환될 수 있는 에러 타입이 나열되어 있습니다.

### 웹훅

**원본** 본문으로 검증해야 합니다. 검증 전에 JSON을 다시 인코딩하면 안 됩니다.

```go
verifier, err := portone.NewWebhookVerifier(os.Getenv("PORTONE_WEBHOOK_SECRET")) // 교체 중에는 시크릿 두 개를 전달

http.HandleFunc("/portone/webhook", func(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	hook, err := verifier.Verify(body, r.Header)
	if err != nil {
		http.Error(w, "invalid webhook", http.StatusBadRequest)
		return
	}
	if tx, ok := hook.(*portone.WebhookTransaction); ok {
		// 웹훅에는 ID만 들어 있으므로 결제 건을 조회한 뒤 처리합니다.
		syncPayment(r.Context(), tx.Data.PaymentID)
	}
	w.WriteHeader(http.StatusOK)
})
```

포트원은 전달에 실패한 웹훅을 재시도하고, 웹훅은 브라우저 리다이렉트보다 먼저 또는 나중에 도착할 수 있으므로 핸들러는 멱등해야 합니다. `SignWebhook`은 포트원이 보내는 것과 같은 서명을 만들어 핸들러 테스트에 쓸 수 있습니다.

### 옵션

| 옵션 | 기본값 |
| --- | --- |
| `WithStoreID(id)` | 없음 |
| `WithBaseURL(url)` | `https://api.portone.io` |
| `WithHTTPClient(c)` | 타임아웃 30초인 `http.Client` |
| `WithUserAgent(ua)` | `go-portone/<version>` |

## 공식 SDK와의 차이

- 요청 본문은 키워드 인자로 펼치지 않고 스펙에 정의된 본문 타입(`CancelPaymentBody`)을 그대로 받습니다.
- 에러는 각 API의 에러 one-of를 통해 디코딩하여 스펙을 그대로 따릅니다.
- 웹훅은 이벤트마다 별도 타입을 두지 않고 `Type` 필드가 있는 `WebhookTransaction` / `WebhookBillingKey`로 디코딩되며, 이후 추가되는 `Transaction.*`, `BillingKey.*` 타입도 디코딩됩니다.
- `getBankInfos`는 공식 SDK와 마찬가지로 제외됩니다.

## 개발

`*.gen.go` 파일은 [`internal/codegen`](./internal/codegen)이 생성합니다. 생성기는 [업스트림 스펙](https://github.com/portone-io/server-sdk/blob/main/codegen/openapi.json)의 사본인 `internal/codegen/openapi.json`을 읽습니다. API 변경 사항을 반영하려면:

```bash
curl -fsSL https://raw.githubusercontent.com/portone-io/server-sdk/main/codegen/openapi.json -o internal/codegen/openapi.json
go generate ./...
go vet ./... && go test -race ./...
```

`SpecVersion`에 생성에 사용된 스펙 버전이 들어 있습니다. `*.gen.go`는 직접 수정하지 마세요. 생성기 출력과 다르면 CI가 실패합니다.

릴리스하려면 `client.go`의 `Version`을 바꾸고 `CHANGELOG.md`에 버전을 추가한 뒤 `main`에 병합하고, 같은 버전의 태그를 푸시합니다 (`git tag v1.2.0 && git push origin v1.2.0`). 릴리스 워크플로는 태그가 `Version`과 다르면 중단하고, 테스트를 실행한 뒤 GitHub Release를 만들고 Go 모듈 프록시에 버전을 게시합니다. `govulncheck`는 모든 푸시와 매주 실행됩니다.

## 라이선스

[MIT](./LICENSE). OpenAPI 문서는 포트원의 것으로, 포트원 서버 SDK와 함께 MIT/Apache-2.0으로 배포됩니다. [NOTICE](./NOTICE)를 참고하세요.
