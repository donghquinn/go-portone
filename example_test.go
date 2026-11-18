package portone_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/donghquinn/go-portone"
)

func ExampleNewClient() {
	// A fake API server stands in for https://api.portone.io.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":"PAID","id":"order-1","amount":{"total":15000,"paid":15000}}`)
	}))
	defer srv.Close()

	client, err := portone.NewClient("api-secret", portone.WithStoreID("store-id"), portone.WithBaseURL(srv.URL))
	if err != nil {
		log.Fatal(err)
	}

	payment, err := client.Payment.GetPayment(context.Background(), "order-1")
	if err != nil {
		log.Fatal(err)
	}
	switch p := payment.(type) {
	case *portone.PaidPayment:
		fmt.Println("paid", p.Amount.Total)
	case *portone.UnrecognizedVariant:
		fmt.Println("status unknown to this SDK:", p.Discriminator)
	default:
		fmt.Printf("not paid: %T\n", p)
	}
	// Output: paid 15000
}

func ExampleAPIError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"type":"PAYMENT_NOT_FOUND","message":"결제 건이 존재하지 않습니다."}`)
	}))
	defer srv.Close()
	client, err := portone.NewClient("api-secret", portone.WithBaseURL(srv.URL))
	if err != nil {
		log.Fatal(err)
	}

	_, err = client.Payment.GetPayment(context.Background(), "missing")

	var notFound *portone.PaymentNotFoundError
	var apiErr *portone.APIError
	if errors.As(err, &notFound) && errors.As(err, &apiErr) {
		fmt.Println(apiErr.StatusCode, *notFound.Message)
	}
	// Output: 404 결제 건이 존재하지 않습니다.
}

func ExampleWebhookVerifier_Verify() {
	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	body := []byte(`{"type":"Transaction.Paid","timestamp":"2026-10-27T01:00:05Z","data":{"storeId":"store-id","paymentId":"order-1","transactionId":"tx-1"}}`)
	now := time.Now()
	signature, err := portone.SignWebhook(secret, "msg_1", now, body)
	if err != nil {
		log.Fatal(err)
	}
	header := http.Header{}
	header.Set("webhook-id", "msg_1")
	header.Set("webhook-timestamp", fmt.Sprint(now.Unix()))
	header.Set("webhook-signature", signature)

	verifier, err := portone.NewWebhookVerifier(secret)
	if err != nil {
		log.Fatal(err)
	}
	hook, err := verifier.Verify(body, header)
	if err != nil {
		log.Fatal(err)
	}
	if tx, ok := hook.(*portone.WebhookTransaction); ok {
		fmt.Println(tx.Type, tx.Data.PaymentID)
	}
	// Output: Transaction.Paid order-1
}
