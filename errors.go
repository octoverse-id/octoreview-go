package octoreview

import (
	"errors"
	"fmt"
)

// Error codes the server sends (docs/errors.md). The contract gate checks this set against the
// vendored contract's ErrorCode enum, in both directions.
const (
	CodeValidationError            = "validation_error"
	CodeInvalidNamespace           = "invalid_namespace"
	CodeApplicationMismatch        = "application_mismatch"
	CodeMediaTooLarge              = "media_too_large"
	CodeContentTypeNotAllowed      = "content_type_not_allowed"
	CodeAuthenticationRequired     = "authentication_required"
	CodeInvalidAPIKey              = "invalid_api_key"
	CodePermissionDenied           = "permission_denied"
	CodeUploadTokenInvalid         = "upload_token_invalid"
	CodeMediaTokenInvalid          = "media_token_invalid"
	CodeNotFound                   = "not_found"
	CodeMethodNotAllowed           = "method_not_allowed"
	CodeNotAcceptable              = "not_acceptable"
	CodeReviewExists               = "review_exists"
	CodeEntityInactive             = "entity_inactive"
	CodeInvalidTransition          = "invalid_transition"
	CodeReviewChanged              = "review_changed"
	CodeRevisionSuperseded         = "revision_superseded"
	CodeRevisionStale              = "revision_stale"
	CodeSourceRefImmutable         = "source_ref_immutable"
	CodeReviewNotAcceptingMedia    = "review_not_accepting_media"
	CodeMediaLimitExceeded         = "media_limit_exceeded"
	CodeStorageQuotaExceeded       = "storage_quota_exceeded"
	CodeUploadExpired              = "upload_expired"
	CodeUploadNotFound             = "upload_not_found"
	CodeUploadChanged              = "upload_changed"
	CodeCompleteInProgress         = "complete_in_progress"
	CodeUploadAlreadyCompleted     = "upload_already_completed"
	CodeUploadTokenExpired         = "upload_token_expired"
	CodeMediaTokenExpired          = "media_token_expired"
	CodeContentLengthRequired      = "content_length_required"
	CodeUploadTooLarge             = "upload_too_large"
	CodeUnsupportedMediaType       = "unsupported_media_type"
	CodeRangeNotSatisfiable        = "range_not_satisfiable"
	CodeMediaRejected              = "media_rejected"
	CodeInternalError              = "internal_error"
	CodeStorageUnavailable         = "storage_unavailable"
	CodeStorageCredentialsExpiring = "storage_credentials_expiring"
	CodeTooManyCompletions         = "too_many_completions"
)

// errorCodes lists every constant above, for the contract gate.
var errorCodes = []string{
	CodeValidationError, CodeInvalidNamespace, CodeApplicationMismatch, CodeMediaTooLarge,
	CodeContentTypeNotAllowed, CodeAuthenticationRequired, CodeInvalidAPIKey,
	CodePermissionDenied, CodeUploadTokenInvalid, CodeMediaTokenInvalid, CodeNotFound,
	CodeMethodNotAllowed, CodeNotAcceptable, CodeReviewExists, CodeEntityInactive,
	CodeInvalidTransition, CodeReviewChanged, CodeRevisionSuperseded, CodeRevisionStale,
	CodeSourceRefImmutable, CodeReviewNotAcceptingMedia, CodeMediaLimitExceeded,
	CodeStorageQuotaExceeded, CodeUploadExpired, CodeUploadNotFound, CodeUploadChanged,
	CodeCompleteInProgress, CodeUploadAlreadyCompleted, CodeUploadTokenExpired,
	CodeMediaTokenExpired, CodeContentLengthRequired, CodeUploadTooLarge,
	CodeUnsupportedMediaType, CodeRangeNotSatisfiable, CodeMediaRejected, CodeInternalError,
	CodeStorageUnavailable, CodeStorageCredentialsExpiring, CodeTooManyCompletions,
}

// APIError is an error response from the server: the octoreview error envelope plus the HTTP
// status. Code is one of the Code constants; Details carries the code-specific fields
// docs/errors.md lists. It never contains the API key.
type APIError struct {
	Status    int
	Code      string
	Message   string
	Details   map[string]any
	RequestID string
	DocURL    string
	Retryable bool
	// RetryAfter is the server's Retry-After, in seconds, when it sent one.
	RetryAfter int
}

func (e *APIError) Error() string {
	id := e.RequestID
	if id == "" {
		id = "none"
	}
	return fmt.Sprintf("octoreview: %s (HTTP %d, request id %s): %s", e.Code, e.Status, id, e.Message)
}

// AsAPIError returns the *APIError inside err, if any.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	ok := errors.As(err, &apiErr)
	return apiErr, ok
}

// IsCode reports whether err is an *APIError with the given code.
func IsCode(err error, code string) bool {
	apiErr, ok := AsAPIError(err)
	return ok && apiErr.Code == code
}

// IsNotFound reports a 404 not_found.
func IsNotFound(err error) bool { return IsCode(err, CodeNotFound) }

// IsReviewChanged reports the version guard refusing a moderation call: reload the review and
// show the moderator the new content.
func IsReviewChanged(err error) bool { return IsCode(err, CodeReviewChanged) }

// ErrNotModified matches a read called with [WithIfNoneMatch] when the resource still has that
// ETag (HTTP 304). Use errors.Is; the error itself is a [*NotModifiedError].
var ErrNotModified = errors.New("octoreview: not modified")

// NotModifiedError is the 304 answer to a conditional read. It matches [ErrNotModified] with
// errors.Is and carries the response's request id.
type NotModifiedError struct {
	// RequestID is the 304 response's X-Request-ID.
	RequestID string
}

func (e *NotModifiedError) Error() string {
	if e.RequestID == "" {
		return ErrNotModified.Error()
	}
	return fmt.Sprintf("%s (request id %s)", ErrNotModified.Error(), e.RequestID)
}

// Is makes errors.Is(err, ErrNotModified) hold.
func (e *NotModifiedError) Is(target error) bool { return target == ErrNotModified }

// TransportError is a failure to reach the server or read its answer (no HTTP response, or an
// unreadable one). Its message never includes the API key, query strings or token path segments.
type TransportError struct {
	Method string
	// URL is redacted: no query string and no token path segment.
	URL string
	// Err is the HTTP client's error, kept for errors.Is and errors.As (context.DeadlineExceeded,
	// net.Error). Its own text can hold the signed URL: Error() prints it redacted, and it is
	// never serialized (json:"-").
	Err error `json:"-"`
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("octoreview: %s %s: %s", e.Method, e.URL, redactText(e.Err.Error()))
}

func (e *TransportError) Unwrap() error { return e.Err }
