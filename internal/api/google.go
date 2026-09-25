package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// googleFailure is what the CLI reads from a Google error body:
// {"error":{"code","message","status","details":[{"@type":"...GoogleAdsFailure","errors":[...],"requestId"}]}}.
type googleFailure struct {
	Message   string
	Status    string
	Errors    []adsError
	RequestID string
}

type adsError struct {
	ErrorCode map[string]string `json:"errorCode"`
	Message   string            `json:"message"`
	Location  *struct {
		FieldPathElements []struct {
			FieldName string `json:"fieldName"`
			Index     *int   `json:"index"`
		} `json:"fieldPathElements"`
	} `json:"location"`
	Details *struct {
		QuotaErrorDetails *struct {
			RetryDelay string `json:"retryDelay"`
		} `json:"quotaErrorDetails"`
	} `json:"details"`
}

type rpcStatus struct {
	Message string            `json:"message"`
	Status  string            `json:"status"`
	Details []json.RawMessage `json:"details"`
}

func parseFailure(body []byte) googleFailure {
	var env struct {
		Error rpcStatus `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil {
		return googleFailure{}
	}
	return fromStatus(env.Error)
}

func fromStatus(s rpcStatus) googleFailure {
	f := googleFailure{Message: s.Message, Status: s.Status}
	for _, raw := range s.Details {
		var d struct {
			Type      string     `json:"@type"`
			Errors    []adsError `json:"errors"`
			RequestID string     `json:"requestId"`
		}
		if json.Unmarshal(raw, &d) != nil || !strings.HasSuffix(d.Type, ".GoogleAdsFailure") {
			continue
		}
		f.Errors = append(f.Errors, d.Errors...)
		if f.RequestID == "" {
			f.RequestID = d.RequestID
		}
	}
	return f
}

// code renders an error code as "category.VALUE": {"fieldError":"REQUIRED"}
// becomes "fieldError.REQUIRED".
func (e adsError) code() string {
	for k, v := range e.ErrorCode {
		return k + "." + v
	}
	return ""
}

// path renders the field path: mutate_operations[0].campaign_operation.create.name.
func (e adsError) path() string {
	if e.Location == nil {
		return ""
	}
	var parts []string
	for _, el := range e.Location.FieldPathElements {
		p := el.FieldName
		if el.Index != nil {
			p += "[" + strconv.Itoa(*el.Index) + "]"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ".")
}

// summary is the part of the message after "HTTP <code>": Google's first
// error with its field path, else Google's own message.
func (f googleFailure) summary() string {
	if len(f.Errors) > 0 {
		e := f.Errors[0]
		s := ": " + e.code() + ": " + e.Message
		if p := e.path(); p != "" {
			s += " (" + p + ")"
		}
		if n := len(f.Errors) - 1; n > 0 {
			s += fmt.Sprintf(" (+%d more in details)", n)
		}
		return s
	}
	if f.Message != "" {
		return ": " + f.Message
	}
	return ""
}

func (f googleFailure) has(value string) bool {
	for _, e := range f.Errors {
		for _, v := range e.ErrorCode {
			if v == value {
				return true
			}
		}
	}
	return false
}

func (f googleFailure) concurrent() bool { return f.has("CONCURRENT_MODIFICATION") }

func (f googleFailure) retryDelay() (time.Duration, bool) {
	for _, e := range f.Errors {
		if e.Details != nil && e.Details.QuotaErrorDetails != nil {
			if d, err := time.ParseDuration(e.Details.QuotaErrorDetails.RetryDelay); err == nil && d >= 0 {
				return d, true
			}
		}
	}
	return 0, false
}

var (
	planningMethod = regexp.MustCompile(`(?i)(keyword|reach|audience|insight|plannable|benchmark)`)
	mutateMethod   = regexp.MustCompile(`(?i)[.:]mutate`)
)

// isPlanning reports a planning read or generator (keyword ideas, forecasts,
// reach, audience insights). The keyword-plan and audience mutates share the
// words but not the access rule.
func isPlanning(methodID string) bool {
	return planningMethod.MatchString(methodID) && !mutateMethod.MatchString(methodID)
}

// hint appends the next step for the errors people hit while setting up.
func hint(r Request, f googleFailure, status int) string {
	switch {
	case f.has("CLOUD_PROJECT_NOT_APPROVED_FOR_PRODUCTION"):
		return "; hint: the Google Cloud project still has Test access, which reaches test accounts only; apply for Explorer or Basic access on the project's Google Ads API page in the Cloud Console"
	case f.has("USER_PERMISSION_DENIED"):
		return "; hint: the service account has no access to this customer; add its email in Google Ads under Admin, Access and security, or set login-customer-id to the manager account it was added to"
	case f.has("EU_POLITICAL_ADVERTISING_DECLARATION_REQUIRED"):
		return `; hint: a campaign in this account lacks the EU political advertising declaration; find it with: googleads search --query "SELECT campaign.id, campaign.name FROM campaign WHERE campaign.missing_eu_political_advertising_declaration = TRUE"`
	case status == http.StatusForbidden && isPlanning(r.MethodID):
		return "; hint: Explorer access blocks planning services (Keyword Planner, reach, audience insights); Basic access is required"
	}
	return ""
}

// PartialFailure inspects a 2xx response to a request sent with
// partialFailure: a non-empty partialFailureError means some operations
// failed. It returns nil when nothing failed.
func PartialFailure(r Request, resp *Response) *Error {
	var env struct {
		PartialFailureError json.RawMessage `json:"partialFailureError"`
	}
	if json.Unmarshal(resp.Body, &env) != nil || len(env.PartialFailureError) == 0 || string(env.PartialFailureError) == "null" {
		return nil
	}
	var s rpcStatus
	if json.Unmarshal(env.PartialFailureError, &s) != nil || (s.Message == "" && len(s.Details) == 0) {
		return nil
	}
	var details any
	json.Unmarshal(env.PartialFailureError, &details)
	f := fromStatus(s)
	rid := resp.Header.Get("request-id")
	if rid == "" {
		rid = f.RequestID
	}
	return &Error{
		Kind:      KindPartialFailure,
		Message:   fmt.Sprintf("%s %s: some operations failed%s; the full response is on stdout", r.Method, r.Path, f.summary()),
		Status:    resp.Status,
		Details:   details,
		RequestID: rid,
	}
}
