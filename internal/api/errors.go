package api

import "fmt"

// Error kinds. Every error the CLI reports carries exactly one.
const (
	KindAuth           = "auth"
	KindForbidden      = "forbidden"
	KindNotFound       = "not_found"
	KindValidation     = "validation"
	KindRateLimited    = "rate_limited"
	KindServer         = "server"
	KindTransport      = "transport"
	KindOutcomeUnknown = "outcome_unknown"
	KindIncomplete     = "incomplete"
	KindUsage          = "usage"
	KindPartialFailure = "partial_failure"
	KindConflict       = "conflict"
	// KindOutputFailed: the call succeeded, but its response could not be
	// written where it was asked to go. The change, if any, has happened.
	KindOutputFailed = "output_failed"
)

// Error is the single error type the CLI reports, shaped for JSON output.
type Error struct {
	Kind      string `json:"kind"`
	Message   string `json:"error"`
	Status    int    `json:"status,omitempty"`
	Details   any    `json:"details"`
	RequestID string `json:"request_id,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// Usagef builds a caller error: something wrong before any request is sent.
func Usagef(format string, a ...any) *Error {
	return &Error{Kind: KindUsage, Message: fmt.Sprintf(format, a...)}
}

func kindForStatus(status int) string {
	switch {
	case status == 401:
		return KindAuth
	case status == 403:
		return KindForbidden
	case status == 404 || status == 410:
		return KindNotFound
	case status == 409:
		return KindConflict
	case status == 429:
		return KindRateLimited
	case status >= 500:
		return KindServer
	default:
		return KindValidation
	}
}
