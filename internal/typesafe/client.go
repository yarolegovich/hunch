// Package typesafe is a small client for TypeSafe's System One API
// (https://docs.typesafe.ai).
//
//	c := typesafe.NewClient(typesafe.WithAPIKey(key))
//	res, err := c.Ask(ctx, typesafe.Request{
//		State: logLine,
//		Questions: typesafe.Questions{
//			"auth": typesafe.Noul("Is this an authentication failure?"),
//			"sev":  typesafe.Choice("Severity", "low", "medium", "high"),
//		},
//	})
package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	DefaultBaseURL = "https://api.typesafe.ai"
	DefaultModel   = "jev-latest"

	// MaxBodySize is the API's limit on a request body, compressed or not.
	MaxBodySize = 32 << 20

	// CompressThreshold is the body size from which requests are zstd
	// compressed, matching the official SDKs.
	CompressThreshold = 1 << 10

	EnvAPIKey  = "TYPESAFE_API_KEY"
	EnvBaseURL = "TYPESAFE_BASE_URL"
	EnvModel   = "TYPESAFE_DEFAULT_MODEL"

	longContextHeader = "TypeSafe-Danger-Zone-Long-Context"
)

// Request is one System One call: a state and the questions to ask about it.
type Request struct {
	// Model overrides the client's default model.
	Model string `json:"model"`
	// State is the content to evaluate: a string, or any JSON-encodable value.
	State     any       `json:"state"`
	Questions Questions `json:"questions"`
	// LongContext raises the context limits to 262k tokens for this request.
	// It is slower and less accurate; use it only when needed.
	LongContext bool `json:"-"`
}

// Validate reports obvious mistakes before a request is sent.
func (r Request) Validate() error {
	if len(r.Questions) == 0 {
		return errors.New("typesafe: request has no questions")
	}
	for id, q := range r.Questions {
		if err := q.Validate(); err != nil {
			return fmt.Errorf("typesafe: question %q: %w", id, err)
		}
	}
	return nil
}

// Model is one entry of GET /v1/models.
type Model struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ReleaseDate string `json:"release_date"`
}

// RetryPolicy controls how failed calls are retried. The defaults match the
// official SDKs.
type RetryPolicy struct {
	// MaxRetries is the number of retries after the first attempt.
	MaxRetries int
	// InitialBackoff is doubled after each attempt up to MaxBackoff.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	// Jitter is the fraction of each delay randomly subtracted, from 0 to 1.
	Jitter float64
	// MaxRetryAfter caps a server-requested delay; longer ones use backoff.
	MaxRetryAfter time.Duration
}

// DefaultRetryPolicy retries 408, 429, 5xx (including 529) and connection
// errors twice.
var DefaultRetryPolicy = RetryPolicy{
	MaxRetries:     2,
	InitialBackoff: 500 * time.Millisecond,
	MaxBackoff:     5 * time.Second,
	Jitter:         0.25,
	MaxRetryAfter:  time.Minute,
}

// Client calls the TypeSafe API. It is safe for concurrent use.
type Client struct {
	apiKey    string
	baseURL   string
	model     string
	userAgent string
	http      *http.Client
	retry     RetryPolicy
	compress  bool
	onRetry   func(attempt int, err error, delay time.Duration)
	encoder   *zstd.Encoder
	headers   http.Header
}

// Option configures a Client.
type Option func(*Client)

// WithAPIKey sets the API key. It defaults to $TYPESAFE_API_KEY.
func WithAPIKey(key string) Option { return func(c *Client) { c.apiKey = key } }

// WithBaseURL sets the API root. It defaults to $TYPESAFE_BASE_URL, then
// DefaultBaseURL.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithModel sets the default model. It defaults to $TYPESAFE_DEFAULT_MODEL,
// then DefaultModel.
func WithModel(m string) Option { return func(c *Client) { c.model = m } }

// WithHTTPClient sets the underlying HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithRetryPolicy replaces the retry policy.
func WithRetryPolicy(p RetryPolicy) Option { return func(c *Client) { c.retry = p } }

// WithMaxRetries sets the number of retries after the first attempt.
func WithMaxRetries(n int) Option { return func(c *Client) { c.retry.MaxRetries = n } }

// WithCompression turns zstd compression of large request bodies on or off.
// It is on by default.
func WithCompression(on bool) Option { return func(c *Client) { c.compress = on } }

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithHeader adds a header to every request, e.g. a gateway's attribution
// headers.
func WithHeader(key, value string) Option {
	return func(c *Client) {
		if c.headers == nil {
			c.headers = http.Header{}
		}
		c.headers.Add(key, value)
	}
}

// WithRetryHook is called before each retry with the failed attempt number
// (starting at 1), its error and the delay before the next attempt.
func WithRetryHook(f func(attempt int, err error, delay time.Duration)) Option {
	return func(c *Client) { c.onRetry = f }
}

// NewClient returns a client configured from opts and the environment.
func NewClient(opts ...Option) *Client {
	c := &Client{
		apiKey:    os.Getenv(EnvAPIKey),
		baseURL:   os.Getenv(EnvBaseURL),
		model:     os.Getenv(EnvModel),
		userAgent: "typesafe-go",
		http:      &http.Client{Timeout: 2 * time.Minute},
		retry:     DefaultRetryPolicy,
		compress:  true,
	}
	for _, o := range opts {
		o(c)
	}
	if c.baseURL == "" {
		c.baseURL = DefaultBaseURL
	}
	c.baseURL = strings.TrimRight(c.baseURL, "/")
	if c.model == "" {
		c.model = DefaultModel
	}
	if c.compress {
		// The API accepts zstd windows of at most 8 MiB.
		c.encoder, _ = zstd.NewWriter(nil, zstd.WithWindowSize(8<<20))
	}
	return c
}

// DefaultModel returns the model used when a request names none.
func (c *Client) DefaultModel() string { return c.model }

// Body returns the JSON body Ask would send for r.
func (c *Client) Body(r Request) ([]byte, error) {
	if r.Model == "" {
		r.Model = c.model
	}
	return json.Marshal(r)
}

// Ask evaluates the request's state against its questions.
func (c *Client) Ask(ctx context.Context, r Request) (*Response, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	body, err := c.Body(r)
	if err != nil {
		return nil, fmt.Errorf("typesafe: encode request: %w", err)
	}
	if len(body) > MaxBodySize {
		return nil, fmt.Errorf("%w (%d bytes)", ErrRequestTooLarge, len(body))
	}
	header := http.Header{"Content-Type": {"application/json"}}
	if c.encoder != nil && len(body) >= CompressThreshold {
		body = c.encoder.EncodeAll(body, make([]byte, 0, len(body)/4))
		header.Set("Content-Encoding", "zstd")
	}
	if r.LongContext {
		header.Set(longContextHeader, "true")
	}
	var res Response
	if err := c.do(ctx, http.MethodPost, "/v1/systemone", header, body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ListModels returns the model names the account can send.
func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	var res struct {
		Models []Model `json:"models"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/models", http.Header{}, nil, &res); err != nil {
		return nil, err
	}
	return res.Models, nil
}

func (c *Client) do(ctx context.Context, method, path string, header http.Header, body []byte, out any) error {
	if c.apiKey == "" {
		return ErrNoAPIKey
	}
	for k, v := range c.headers {
		header[k] = v
	}
	header.Set("Authorization", "Bearer "+c.apiKey)
	header.Set("Accept", "application/json")
	header.Set("User-Agent", c.userAgent)

	for attempt := 1; ; attempt++ {
		retryAfter, err := c.attempt(ctx, method, path, header, body, out)
		if err == nil {
			return nil
		}
		if attempt > c.retry.MaxRetries || !retryable(err) || ctx.Err() != nil {
			return err
		}
		delay := c.backoff(attempt, retryAfter)
		if c.onRetry != nil {
			c.onRetry(attempt, err, delay)
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
	}
}

// attempt makes one HTTP call. It returns any server-requested retry delay.
func (c *Client) attempt(ctx context.Context, method, path string, header http.Header, body []byte, out any) (time.Duration, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header = header.Clone()
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, &connError{err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, &connError{err}
	}
	if resp.StatusCode/100 != 2 {
		return retryAfter(resp.Header), newAPIError(resp, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return 0, fmt.Errorf("typesafe: decode response: %w", err)
	}
	return 0, nil
}

// connError marks transport failures, which are retried.
type connError struct{ err error }

func (e *connError) Error() string { return "typesafe: " + e.err.Error() }
func (e *connError) Unwrap() error { return e.err }

// retryable reports whether a failed attempt is worth repeating. Cancellation
// of the caller's context is checked separately.
func retryable(err error) bool {
	var ce *connError
	if errors.As(err, &ce) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		s := apiErr.StatusCode
		return s == http.StatusRequestTimeout || s == http.StatusTooManyRequests || s >= 500
	}
	return false
}

func (c *Client) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 && retryAfter <= c.retry.MaxRetryAfter {
		return retryAfter
	}
	d := c.retry.InitialBackoff << (attempt - 1)
	if d > c.retry.MaxBackoff || d <= 0 {
		d = c.retry.MaxBackoff
	}
	if c.retry.Jitter > 0 {
		d -= time.Duration(rand.Float64() * c.retry.Jitter * float64(d))
	}
	return d
}

// retryAfter reads retry-after-ms or retry-after (seconds or an HTTP date).
func retryAfter(h http.Header) time.Duration {
	if v := h.Get("Retry-After-Ms"); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil && ms > 0 {
			return time.Duration(ms * float64(time.Millisecond))
		}
	}
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if s, err := strconv.ParseFloat(v, 64); err == nil && s > 0 {
		return time.Duration(s * float64(time.Second))
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}
