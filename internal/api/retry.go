package api

import (
	"errors"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"time"
)

// classifyTransport maps a network failure. A dial or DNS failure never
// reached Google, and a read-safe call changes nothing, so both are safe to
// retry. Anything else may have been applied: the caller must verify state.
func classifyTransport(err error, readSafe bool) *Error {
	var opErr *net.OpError
	var dnsErr *net.DNSError
	presend := (errors.As(err, &opErr) && opErr.Op == "dial") || errors.As(err, &dnsErr)
	if readSafe || presend {
		return &Error{Kind: KindTransport, Message: err.Error()}
	}
	return &Error{Kind: KindOutcomeUnknown,
		Message: "the request may or may not have been applied: " + err.Error() + "; verify state before retrying"}
}

// retryAfter returns the Retry-After header as a duration, or 0.
func retryAfter(h http.Header) time.Duration {
	if secs, err := strconv.Atoi(h.Get("Retry-After")); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// jitter spreads retries so concurrent callers do not resynchronize.
func jitter() time.Duration { return time.Duration(rand.Int63n(int64(500 * time.Millisecond))) }
