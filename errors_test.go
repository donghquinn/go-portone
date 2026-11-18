package portone

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestAPIError(t *testing.T) {
	tests := []struct {
		name        string
		giveStatus  int
		giveBody    string
		wantType    string
		wantMessage string
		wantTyped   bool
	}{
		{
			name:        "known error type",
			giveStatus:  http.StatusNotFound,
			giveBody:    `{"type": "PAYMENT_NOT_FOUND", "message": "결제 건이 존재하지 않습니다."}`,
			wantType:    "PAYMENT_NOT_FOUND",
			wantMessage: "결제 건이 존재하지 않습니다.",
			wantTyped:   true,
		},
		{
			name:       "error type added after this sdk",
			giveStatus: http.StatusBadRequest,
			giveBody:   `{"type": "SOMETHING_NEW", "message": "?"}`,
			wantType:   "SOMETHING_NEW", wantMessage: "?",
		},
		{
			name:       "not json",
			giveStatus: http.StatusBadGateway,
			giveBody:   `<html>bad gateway</html>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, c := newFakeAPI(t, tt.giveStatus, tt.giveBody)

			_, err := c.Payment.GetPayment(context.Background(), "order-1")

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("GetPayment() error = %T %v, want *APIError", err, err)
			}
			if apiErr.StatusCode != tt.giveStatus || apiErr.Type != tt.wantType || apiErr.Message != tt.wantMessage || string(apiErr.Body) != tt.giveBody {
				t.Errorf("APIError = %+v", apiErr)
			}
			var notFound *PaymentNotFoundError
			if got := errors.As(err, &notFound); got != tt.wantTyped {
				t.Errorf("errors.As(*PaymentNotFoundError) = %v, want %v", got, tt.wantTyped)
			}
			if err.Error() == "" {
				t.Error("Error() is empty")
			}
		})
	}
}

func TestAPIError_pgProvider(t *testing.T) {
	t.Parallel()
	_, c := newFakeAPI(t, http.StatusBadGateway, `{"type": "PG_PROVIDER", "message": "PG사 에러", "pgCode": "F113", "pgMessage": "한도 초과"}`)

	_, err := c.Payment.CancelPayment(context.Background(), "order-1", CancelPaymentBody{Reason: "test"})

	var pgErr *PgProviderError
	if !errors.As(err, &pgErr) {
		t.Fatalf("CancelPayment() error = %v, want *PgProviderError", err)
	}
	if pgErr.PgCode != "F113" || pgErr.PgMessage != "한도 초과" {
		t.Errorf("PgProviderError = %+v", pgErr)
	}
	if got, want := pgErr.Error(), "portone: PG_PROVIDER: PG사 에러"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// The spec maps PLATFORM_CURRENCY_NOT_SUPPORTED to a different type for one operation;
// decoding through each operation's own error one-of keeps both right.
func TestAPIError_perOperationErrors(t *testing.T) {
	t.Parallel()
	_, c := newFakeAPI(t, http.StatusBadRequest, `{"type": "PLATFORM_CURRENCY_NOT_SUPPORTED"}`)

	_, err := c.Platform.Partner.CreatePlatformPartner(context.Background(), CreatePlatformPartnerBody{}, nil)

	var currency *PlatformCurrencyNotSupportedError
	if !errors.As(err, &currency) {
		t.Errorf("CreatePlatformPartner() error = %#v, want *PlatformCurrencyNotSupportedError", err)
	}
}
