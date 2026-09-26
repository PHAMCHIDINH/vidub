package translator

import (
	"errors"
	"fmt"
)

// APIError is a non-200 answer from a translation API.
type APIError struct {
	Provider string
	Status   int
	Body     string // truncated response body
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s API error (status %d): %s", e.Provider, e.Status, e.Body)
}

// Permanent reports errors that retrying cannot fix: a rejected API key
// (401, 403) or an account without balance (DeepSeek answers 402).
func (e *APIError) Permanent() bool {
	return e.Status == 401 || e.Status == 402 || e.Status == 403
}

// Retryable reports errors worth another try: rate limits and server errors.
func (e *APIError) Retryable() bool {
	return e.Status == 429 || e.Status >= 500
}

// IsPermanent reports whether err wraps an APIError that retrying cannot fix.
func IsPermanent(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Permanent()
}
