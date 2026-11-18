package main

import (
	"go/token"
	"strings"
	"unicode"
)

// initialisms are words Go spells in all caps (paymentId → PaymentID).
var initialisms = map[string]string{
	"id": "ID", "ids": "IDs", "url": "URL", "urls": "URLs", "uri": "URI",
	"api": "API", "http": "HTTP", "json": "JSON", "ip": "IP", "uuid": "UUID", "html": "HTML",
}

// words splits camelCase, PascalCase and snake_case identifiers into words.
func words(s string) []string {
	var out []string
	rs := []rune(s)
	start := 0
	flush := func(end int) {
		if end > start {
			out = append(out, string(rs[start:end]))
		}
		start = end
	}
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ':
			flush(i)
			start = i + 1
		case i > start && unicode.IsUpper(r):
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush(i)
			}
		}
	}
	flush(len(rs))
	return out
}

func capitalize(w string) string {
	if w == "" {
		return w
	}
	rs := []rune(w)
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

// exported turns a JSON or operation name into an exported Go identifier with initialisms.
func exported(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		if v, ok := initialisms[strings.ToLower(w)]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(capitalize(w))
		}
	}
	return b.String()
}

// local turns a name into an unexported Go identifier (paymentId → paymentID).
func local(s string) string {
	ws := words(s)
	if len(ws) == 0 {
		return s
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(ws[0]))
	for _, w := range ws[1:] {
		if v, ok := initialisms[strings.ToLower(w)]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(capitalize(w))
		}
	}
	name := b.String()
	if token.IsKeyword(name) {
		name += "_"
	}
	return name
}

// typeName keeps the spec's type names (they already are PascalCase) but drops characters Go rejects.
func typeName(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

// enumConst names an enum value: Currency + "KRW" → CurrencyKrw, PaymentStatus + "PAY_PENDING" → PaymentStatusPayPending.
func enumConst(typ, value string) string {
	var b strings.Builder
	b.WriteString(typ)
	for _, w := range words(value) {
		b.WriteString(capitalize(strings.ToLower(w)))
	}
	return b.String()
}

// snake names files: payment.billingKey → payment_billing_key.
func snake(s string) string {
	ws := words(s)
	for i, w := range ws {
		ws[i] = strings.ToLower(w)
	}
	return strings.Join(ws, "_")
}
