// Package portone is a small, dependency-free client for the PortOne V2 REST API
// (https://developers.portone.io/api/rest-v2).
//
// It covers what a marketplace server needs around a payment that the browser SDK started:
// looking a payment up to verify it, pre-registering the expected amount, cancelling (refunding),
// the escrow calls (registering shipping, confirming the purchase) and webhook verification.
//
//	client := portone.New(os.Getenv("PORTONE_API_SECRET"), portone.WithStoreID(os.Getenv("PORTONE_STORE_ID")))
//	payment, err := client.GetPayment(ctx, paymentID)
//	if err == nil && payment.Status == portone.PaymentStatusPaid && payment.Amount.Total == expected { ... }
package portone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the PortOne V2 API server.
const DefaultBaseURL = "https://api.portone.io"

// DefaultTimeout bounds a single API call when no *http.Client is supplied.
const DefaultTimeout = 30 * time.Second

// Client calls the PortOne V2 REST API with an API secret. It is safe for concurrent use.
type Client struct {
	secret     string
	storeID    string
	baseURL    string
	httpClient *http.Client
	userAgent  string
}

// Option configures a Client.
type Option func(*Client)

// WithStoreID sets the store ID sent with every request that accepts one.
// It is required when the API secret is shared by several stores (e.g. a platform account).
func WithStoreID(storeID string) Option { return func(c *Client) { c.storeID = storeID } }

// WithBaseURL points the client at another server (a proxy, or a fake in tests).
func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(baseURL, "/") }
}

// WithHTTPClient replaces the default http.Client (30s timeout).
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.httpClient = hc } }

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// New returns a client authenticated with an API secret issued in the PortOne console.
func New(apiSecret string, opts ...Option) *Client {
	c := &Client{
		secret:     apiSecret,
		baseURL:    DefaultBaseURL,
		httpClient: &http.Client{Timeout: DefaultTimeout},
		userAgent:  "donghquinn-pg-portone",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// StoreID returns the store ID the client was configured with.
func (c *Client) StoreID() string { return c.storeID }

// do sends a request and decodes a 2xx JSON body into out (if non-nil).
// Non-2xx responses are returned as *APIError.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("portone: encode request: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return fmt.Errorf("portone: build request: %w", err)
	}
	req.Header.Set("Authorization", "PortOne "+c.secret)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("portone: %s %s: %w", method, path, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("portone: read response: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		apiErr := &APIError{StatusCode: res.StatusCode}
		if json.Unmarshal(raw, apiErr) != nil || apiErr.Type == "" {
			apiErr.Type = "UNKNOWN"
			apiErr.Message = strings.TrimSpace(string(raw))
		}
		return apiErr
	}

	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("portone: decode response: %w", err)
	}
	return nil
}

// paymentPath escapes a merchant-assigned payment ID into a path.
func paymentPath(paymentID, suffix string) string {
	return "/payments/" + url.PathEscape(paymentID) + suffix
}
