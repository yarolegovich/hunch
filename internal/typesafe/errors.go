package typesafe

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrRequestTooLarge is returned without contacting the API when an encoded
// request body is over MaxBodySize.
var ErrRequestTooLarge = errors.New("typesafe: request body over 32 MiB")

// ErrNoAPIKey is returned when the client has no API key.
var ErrNoAPIKey = errors.New("typesafe: no API key (set TYPESAFE_API_KEY)")

// APIError is a non-2xx response from the API.
type APIError struct {
	StatusCode int
	Message    string
	Body       []byte
	Header     http.Header
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("typesafe: %d %s", e.StatusCode, http.StatusText(e.StatusCode))
	}
	return fmt.Sprintf("typesafe: %d %s: %s", e.StatusCode, http.StatusText(e.StatusCode), e.Message)
}

// IsAuthError reports whether err is a 401 or 403 from the API.
func IsAuthError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) &&
		(apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden)
}

func newAPIError(resp *http.Response, body []byte) *APIError {
	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    errorMessage(body),
		Body:       body,
		Header:     resp.Header,
	}
}

// errorMessage pulls a human-readable message out of an error body. The error
// body shape is not documented, so it tries the common ones and falls back to
// the raw text.
func errorMessage(body []byte) string {
	var v map[string]any
	if json.Unmarshal(body, &v) == nil {
		for _, k := range []string{"error", "detail", "message"} {
			switch x := v[k].(type) {
			case string:
				return x
			case map[string]any:
				if m, ok := x["message"].(string); ok {
					return m
				}
			case nil:
			default:
				b, _ := json.Marshal(x)
				return string(b)
			}
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}
