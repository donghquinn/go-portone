// Package portone is a Go client for the PortOne V2 REST API (https://developers.portone.io/api/rest-v2),
// generated from the same OpenAPI document as PortOne's official server SDKs
// (https://github.com/portone-io/server-sdk), plus webhook verification.
//
// APIs are grouped the way the official SDKs group them:
//
//	client, err := portone.NewClient(os.Getenv("PORTONE_API_SECRET"))
//	payment, err := client.Payment.GetPayment(ctx, paymentID)
//	if paid, ok := payment.(*portone.PaidPayment); ok && paid.Amount.Total == expected { ... }
//
// One-of types such as [Payment] are interfaces; a type switch tells the variants apart.
// API errors are [*APIError] values wrapping a typed error such as [*PaymentNotFoundError], for use with [errors.As].
package portone

//go:generate go run ./internal/codegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"
)

const _modulePath = "github.com/donghquinn/go-portone"

// DefaultBaseURL is the PortOne V2 API server.
const DefaultBaseURL = "https://api.portone.io"

const (
	_defaultTimeout = 30 * time.Second
	// _maxResponseSize bounds a response body; sheet downloads are the largest responses.
	_maxResponseSize = 64 << 20
)

type config struct {
	secret     string
	storeID    string
	baseURL    string
	userAgent  string
	httpClient *http.Client
}

// Option configures a [Client].
type Option interface {
	apply(*config)
}

type storeIDOption string

func (o storeIDOption) apply(c *config) { c.storeID = string(o) }

// WithStoreID sets the (sub-)store ID sent with every request that accepts one.
// It is required when the API secret serves several stores. A request body that sets StoreID itself overrides it.
func WithStoreID(storeID string) Option { return storeIDOption(storeID) }

type baseURLOption string

func (o baseURLOption) apply(c *config) { c.baseURL = strings.TrimRight(string(o), "/") }

// WithBaseURL points the client at another server, such as a proxy or a fake in tests. Default: [DefaultBaseURL].
func WithBaseURL(baseURL string) Option { return baseURLOption(baseURL) }

type httpClientOption struct{ client *http.Client }

func (o httpClientOption) apply(c *config) { c.httpClient = o.client }

// WithHTTPClient replaces the default http.Client, which has a 30-second timeout.
func WithHTTPClient(client *http.Client) Option { return httpClientOption{client} }

type userAgentOption string

func (o userAgentOption) apply(c *config) { c.userAgent = string(o) }

// WithUserAgent replaces the User-Agent header. Default: "go-portone/" + the module version, e.g. "go-portone/v1.0.2".
func WithUserAgent(userAgent string) Option { return userAgentOption(userAgent) }

// NewClient returns a client authenticated with an API secret issued in the PortOne console.
func NewClient(apiSecret string, opts ...Option) (*Client, error) {
	if apiSecret == "" {
		return nil, errors.New("portone: api secret is empty")
	}
	cfg := &config{
		secret:     apiSecret,
		baseURL:    DefaultBaseURL,
		userAgent:  defaultUserAgent(),
		httpClient: &http.Client{Timeout: _defaultTimeout},
	}
	for _, opt := range opts {
		opt.apply(cfg)
	}
	if u, err := url.Parse(cfg.baseURL); err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("portone: invalid base url %q", cfg.baseURL)
	}
	if cfg.httpClient == nil {
		return nil, errors.New("portone: http client is nil")
	}
	return newClient(cfg), nil
}

// defaultUserAgent names the SDK and, when the build records it, the module version a program depends on.
func defaultUserAgent() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "go-portone"
	}
	version := info.Main.Version // when this module is the main module, e.g. in its own tests
	for _, dep := range info.Deps {
		if dep.Path == _modulePath {
			version = dep.Version
			if dep.Replace != nil {
				version = dep.Replace.Version
			}
		}
	}
	if version == "" || version == "(devel)" {
		return "go-portone"
	}
	return "go-portone/" + version
}

// Ptr returns a pointer to v, for optional request fields: portone.Ptr(int64(1000)).
func Ptr[T any](v T) *T { return &v }

// do sends a request and returns the body of a 2xx response.
// Other responses become *APIError, with decodeErr turning the body into the operation's typed error.
func (c *config) do(ctx context.Context, method, path string, query url.Values, body any, decodeErr func([]byte) error) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("portone: encode request: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, fmt.Errorf("portone: build request: %w", err)
	}
	req.Header.Set("Authorization", "PortOne "+c.secret)
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("portone: %s %s: %w", method, path, err)
	}
	defer res.Body.Close() //nolint:errcheck // read-only body; a close error changes nothing

	raw, err := io.ReadAll(io.LimitReader(res.Body, _maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("portone: read response: %w", err)
	}
	if len(raw) > _maxResponseSize {
		return nil, fmt.Errorf("portone: response larger than %d bytes", _maxResponseSize)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, newAPIError(res.StatusCode, raw, decodeErr)
	}
	return raw, nil
}

func (c *config) setStoreID(query url.Values) {
	if c.storeID != "" {
		query.Set("storeId", c.storeID)
	}
}

func (c *config) storeIDPtr() *string {
	if c.storeID == "" {
		return nil
	}
	id := c.storeID
	return &id
}

// setJSONQuery sends a GET/DELETE request body as a JSON-encoded query parameter, as the API expects.
func setJSONQuery(query url.Values, key string, v any) error {
	buf, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("portone: encode request: %w", err)
	}
	query.Set(key, string(buf))
	return nil
}

func decodeObject[T any](raw []byte) (*T, error) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func decodeResponse[T any](raw []byte, decode func([]byte) (T, error)) (T, error) {
	v, err := decode(raw)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("portone: decode response: %w", err)
	}
	return v, nil
}
