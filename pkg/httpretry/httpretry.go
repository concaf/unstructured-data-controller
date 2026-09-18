/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package httpretry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ExternalServiceBackoff defines capped exponential backoff for external HTTP
// service calls (docling, embedding, etc.). The backoff grows exponentially
// from 1s up to the 2-minute cap, then retries at 2-minute intervals.
// Sequence: 1s → 2s → 4s → 8s → 16s → 32s → 64s → 120s → 120s → ...
var ExternalServiceBackoff = wait.Backoff{
	Duration: 1 * time.Second,
	Factor:   2.0,
	Cap:      2 * time.Minute,
	Steps:    math.MaxInt32, // grow until the cap, then hold at the cap
	Jitter:   0.2,           // 20% jitter to avoid thundering herd
}

// RetryableHTTPError represents a transient HTTP error that can be retried.
type RetryableHTTPError struct {
	StatusCode int
}

func (e *RetryableHTTPError) Error() string {
	return fmt.Sprintf("retryable HTTP status %d: %s", e.StatusCode, http.StatusText(e.StatusCode))
}

// IsRetryableHTTPError returns true for transient HTTP errors that are worth
// retrying: rate limits (429) and server errors (500, 502, 503, 504).
func IsRetryableHTTPError(err error) bool {
	var retryable *RetryableHTTPError
	if !errors.As(err, &retryable) {
		return false
	}
	switch retryable.StatusCode {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// RetryWithContext retries fn with capped exponential backoff, respecting
// context cancellation. Unlike retry.OnError which uses time.Sleep,
// ExponentialBackoffWithContext interrupts the backoff sleep when the context
// is cancelled — preventing a controller worker from being blocked during shutdown.
func RetryWithContext(ctx context.Context, backoff wait.Backoff, isRetryable func(error) bool, fn func() error) error {
	return wait.ExponentialBackoffWithContext(ctx, backoff, func(_ context.Context) (bool, error) {
		err := fn()
		if err == nil {
			return true, nil
		}
		if isRetryable(err) {
			return false, nil
		}
		return false, err
	})
}

// RetryTransport wraps an http.RoundTripper with automatic retry on transient
// HTTP errors (429, 5xx) using capped exponential backoff. Use it as the
// Transport on any http.Client to get retry behavior transparently.
type RetryTransport struct {
	Base http.RoundTripper
}

// NewRetryTransport creates a RetryTransport wrapping the given base transport.
func NewRetryTransport(base http.RoundTripper) *RetryTransport {
	return &RetryTransport{Base: base}
}

func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	err := RetryWithContext(req.Context(), ExternalServiceBackoff, IsRetryableHTTPError, func() error {
		var reqErr error
		resp, reqErr = t.Base.RoundTrip(req)
		if reqErr != nil {
			return reqErr
		}
		if retryableErr := CheckResponseForRetryableError(resp.StatusCode); retryableErr != nil {
			if closeErr := resp.Body.Close(); closeErr != nil {
				logger := log.FromContext(req.Context())
				logger.Error(closeErr, "failed to close response body before retry")
			}
			return retryableErr
		}
		return nil
	})
	return resp, err
}

// CheckResponseForRetryableError returns a RetryableHTTPError if the HTTP
// status code indicates a transient failure, or nil if the response is OK
// or a non-retryable error.
func CheckResponseForRetryableError(statusCode int) error {
	if statusCode >= 200 && statusCode < 300 {
		return nil
	}
	retryableErr := &RetryableHTTPError{StatusCode: statusCode}
	if IsRetryableHTTPError(retryableErr) {
		return retryableErr
	}
	return nil
}
