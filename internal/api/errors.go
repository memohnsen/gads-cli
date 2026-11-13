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
	// KindGuardrail: a safety rule refused the call before anything was
	// sent (dry-run default, --apply/--confirm-spend gates, read-only mode,
	// the budget cap, a required PAUSED status, a blocked method).
	KindGuardrail      = "guardrail"
	KindPartialFailure = "partial_failure"
	KindConflict       = "conflict"
	// KindOutputFailed: the call succeeded, but its response could not be
	// written where it was asked to go. The change, if any, has happened.
	KindOutputFailed = "output_failed"
	// KindAuditFailed: the call succeeded, but its audit-log line could not
	// be written. The change has happened; do not repeat it.
	KindAuditFailed = "audit_failed"
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

// Guardrailf builds a guardrail refusal: a safety rule stopped the call
// before any request was sent. It exits 2, like a usage error.
func Guardrailf(format string, a ...any) *Error {
	return &Error{Kind: KindGuardrail, Message: fmt.Sprintf(format, a...)}
}

// IsLocal reports whether e was raised before anything was sent.
func (e *Error) IsLocal() bool { return e.Kind == KindUsage || e.Kind == KindGuardrail }

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
