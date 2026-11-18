package main

import "testing"

func TestExported(t *testing.T) {
	tests := []struct {
		give string
		want string
	}{
		{give: "paymentId", want: "PaymentID"},
		{give: "pgTxId", want: "PgTxID"},
		{give: "receiptUrl", want: "ReceiptURL"},
		{give: "partnerIds", want: "PartnerIDs"},
		{give: "identityVerificationId", want: "IdentityVerificationID"},
		{give: "getB2bTaxInvoice", want: "GetB2bTaxInvoice"},
		{give: "billingKey", want: "BillingKey"},
		{give: "test", want: "Test"},
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			if got := exported(tt.give); got != tt.want {
				t.Errorf("exported(%q) = %q, want %q", tt.give, got, tt.want)
			}
		})
	}
}

func TestLocal(t *testing.T) {
	tests := []struct {
		give string
		want string
	}{
		{give: "paymentId", want: "paymentID"},
		{give: "id", want: "id"},
		{give: "type", want: "type_"},
		{give: "accountNumber", want: "accountNumber"},
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			if got := local(tt.give); got != tt.want {
				t.Errorf("local(%q) = %q, want %q", tt.give, got, tt.want)
			}
		})
	}
}

func TestEnumConst(t *testing.T) {
	tests := []struct {
		giveType, giveValue string
		want                string
	}{
		{giveType: "PaymentStatus", giveValue: "PAY_PENDING", want: "PaymentStatusPayPending"},
		{giveType: "Currency", giveValue: "KRW", want: "CurrencyKrw"},
		{giveType: "PgProvider", giveValue: "KCP_V2", want: "PgProviderKcpV2"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := enumConst(tt.giveType, tt.giveValue); got != tt.want {
				t.Errorf("enumConst(%q, %q) = %q, want %q", tt.giveType, tt.giveValue, got, tt.want)
			}
		})
	}
}

func TestSnake(t *testing.T) {
	if got, want := snake("payment.billingKey"), "payment_billing_key"; got != want {
		t.Errorf("snake() = %q, want %q", got, want)
	}
}
