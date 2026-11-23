package octoreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// maxResponseBytes bounds a response body read into memory.
const maxResponseBytes = 16 << 20

// ResponseMeta is what every result carries about the response that produced it.
type ResponseMeta struct {
	// ETag identifies this version of the resource; pass it back with [WithIfNoneMatch].
	ETag string `json:"-"`
	// RequestID is the server's X-Request-ID, for its logs and support.
	RequestID string `json:"-"`
}

// Operations whose application_id is a body field, not a query parameter.
var bodyApplication = map[string]bool{
	opUpsertEntity.ID: true, opQueryRatings.ID: true, opCreateReview.ID: true, opEraseAuthor.ID: true,
}

// retriedConflicts are 409s an operation retries like a 503 (the contract's completeMediaUpload
// says so): another completion holds the lease, or the upload changed and must be revalidated.
var retriedConflicts = map[string]map[string]bool{
	opCompleteMediaUpload.ID: {CodeCompleteInProgress: true, CodeUploadChanged: true},
}

// Operations the contract declares without namespace headers.
var noNamespace = map[string]bool{
	opGetHealthLive.ID: true, opGetHealthReady.ID: true, opGetApplicationSettings.ID: true,
	opPutApplicationSettings.ID: true, opEraseAuthor.ID: true,
}

// spec is one call of an operation.
type spec struct {
	op         operation
	pathValues []string // in the order of the {placeholders} in op.Path
	query      url.Values
	body       any
	out        any // decoded from the envelope's data
	// acceptStatus is a non-2xx status whose body is still a data envelope (readiness 503).
	acceptStatus int
	pagination   *Pagination
}

func (c *Client) config(opts []CallOption) callConfig {
	var cfg callConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// application resolves the application for a call: the call's, else the client's default.
func (c *Client) application(cfg callConfig) string {
	if cfg.applicationID != nil {
		return *cfg.applicationID
	}
	return c.applicationID
}

func (c *Client) call(ctx context.Context, s spec, cfg callConfig) (ResponseMeta, error) {
	if ctx == nil {
		return ResponseMeta{}, errors.New("octoreview: nil context")
	}
	endpoint, err := c.endpoint(s, cfg)
	if err != nil {
		return ResponseMeta{}, err
	}
	var payload []byte
	if s.body != nil {
		if payload, err = json.Marshal(s.body); err != nil {
			return ResponseMeta{}, fmt.Errorf("octoreview: encode %s body: %w", s.op.ID, err)
		}
	}
	attempts := 1
	if s.op.Retry != retryUnsafe {
		attempts = c.maxAttempts
	}
	for attempt := 1; ; attempt++ {
		meta, retryIn, err := c.attempt(ctx, s, cfg, endpoint, payload)
		if err == nil || retryIn < 0 || attempt >= attempts {
			return meta, err
		}
		if retryIn == 0 {
			retryIn = backoff(attempt)
		}
		if sleepErr := c.sleep(ctx, retryIn); sleepErr != nil {
			return meta, err
		}
	}
}

// attempt sends one request. retryIn is -1 when the error must not be retried, 0 for the default
// backoff, or the server's Retry-After.
func (c *Client) attempt(ctx context.Context, s spec, cfg callConfig, endpoint *url.URL, payload []byte) (ResponseMeta, time.Duration, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, s.op.Method, endpoint.String(), body)
	if err != nil {
		return ResponseMeta{}, -1, fmt.Errorf("octoreview: build %s request: %w", s.op.ID, err)
	}
	c.setHeaders(req, s, cfg, payload != nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		transportErr := &TransportError{Method: s.op.Method, URL: redactURL(endpoint), Err: err}
		if ctx.Err() == nil && retriableNetworkError(err) {
			return ResponseMeta{}, 0, transportErr
		}
		return ResponseMeta{}, -1, transportErr
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		if err == nil {
			err = errors.New("the response is larger than 16 MiB")
		}
		return ResponseMeta{}, -1, &TransportError{Method: s.op.Method, URL: redactURL(endpoint), Err: err}
	}
	meta := ResponseMeta{ETag: resp.Header.Get("ETag"), RequestID: resp.Header.Get("X-Request-ID")}
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return meta, -1, &NotModifiedError{RequestID: meta.RequestID}
	case resp.StatusCode >= 200 && resp.StatusCode < 300, resp.StatusCode == s.acceptStatus:
		return meta, -1, decodeEnvelope(raw, s)
	}
	apiErr := parseError(resp, raw)
	if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusTooManyRequests ||
		retriedConflicts[s.op.ID][apiErr.Code] {
		wait := time.Duration(apiErr.RetryAfter) * time.Second
		if wait > c.maxRetryAfter {
			return meta, -1, apiErr // longer than this client is willing to wait
		}
		return meta, wait, apiErr
	}
	return meta, -1, apiErr
}

func (c *Client) endpoint(s spec, cfg callConfig) (*url.URL, error) {
	path := s.op.Path
	for _, value := range s.pathValues {
		start := strings.Index(path, "{")
		end := strings.Index(path, "}")
		if start < 0 || end < start {
			return nil, fmt.Errorf("octoreview: %s has more path values than placeholders", s.op.ID)
		}
		if value == "" {
			return nil, fmt.Errorf("octoreview: %s needs %s", s.op.ID, path[start+1:end])
		}
		path = path[:start] + url.PathEscape(value) + path[end+1:]
	}
	if strings.Contains(path, "{") {
		return nil, fmt.Errorf("octoreview: %s is missing a path value", s.op.ID)
	}
	query := url.Values{}
	maps.Copy(query, s.query)
	if has(s.op.Params, "application_id") && !bodyApplication[s.op.ID] && !strings.Contains(s.op.Path, "{application_id}") {
		if app := c.application(cfg); app != "" {
			query.Set("application_id", app)
		}
	}
	if cfg.includeGlobal {
		if !has(s.op.Params, "include_global") {
			return nil, fmt.Errorf("octoreview: %s does not take IncludeGlobal", s.op.GoMethod)
		}
		query.Set("include_global", "true")
	}
	endpoint := c.baseURL.JoinPath("/api/v1")
	endpoint.RawPath = endpoint.EscapedPath() + path
	endpoint.Path, _ = url.PathUnescape(endpoint.RawPath)
	endpoint.RawQuery = query.Encode()
	return endpoint, nil
}

func (c *Client) setHeaders(req *http.Request, s spec, cfg callConfig, hasBody bool) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	if cfg.requestID != "" {
		req.Header.Set("X-Request-ID", cfg.requestID)
	}
	if s.op.ID == opGetHealthLive.ID || s.op.ID == opGetHealthReady.ID {
		return // unauthenticated probes
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("X-Tenant-ID", c.tenantID)
	if !noNamespace[s.op.ID] {
		ns := c.namespace
		if cfg.global {
			ns = nil
		} else if cfg.namespace != nil {
			ns = cfg.namespace
		}
		if ns != nil {
			req.Header.Set("X-Namespace-Type", ns.Type)
			req.Header.Set("X-Namespace-ID", ns.ID)
		}
	}
	// X-Actor-ID goes on writes only. queryRatings is a POST but a read (retry class safe), and
	// the contract does not declare the header there.
	if s.op.Retry != retrySafe {
		actor := c.actorID
		if cfg.actorID != "" {
			actor = cfg.actorID
		}
		if actor != "" {
			req.Header.Set("X-Actor-ID", actor)
		}
	}
	if s.op.Method == http.MethodGet && cfg.ifNoneMatch != "" {
		req.Header.Set("If-None-Match", cfg.ifNoneMatch)
	}
}

type envelope struct {
	Data       json.RawMessage `json:"data"`
	Pagination *Pagination     `json:"pagination"`
}

func decodeEnvelope(raw []byte, s spec) error {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Data == nil {
		return fmt.Errorf("octoreview: %s answered without a data envelope", s.op.ID)
	}
	if s.out != nil {
		if err := json.Unmarshal(env.Data, s.out); err != nil {
			return fmt.Errorf("octoreview: decode %s: %w", s.op.ID, err)
		}
	}
	if s.pagination != nil && env.Pagination != nil {
		*s.pagination = *env.Pagination
	}
	return nil
}

func parseError(resp *http.Response, raw []byte) *APIError {
	apiErr := &APIError{Status: resp.StatusCode, RequestID: resp.Header.Get("X-Request-ID")}
	if seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && seconds >= 0 {
		apiErr.RetryAfter = seconds
	}
	var body struct {
		Error struct {
			Code      string         `json:"code"`
			Message   string         `json:"message"`
			Details   map[string]any `json:"details"`
			RequestID *string        `json:"request_id"`
			DocURL    string         `json:"doc_url"`
			Retryable bool           `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Error.Code == "" {
		apiErr.Code = "unexpected_response"
		apiErr.Message = fmt.Sprintf("the server answered HTTP %d without an error envelope", resp.StatusCode)
		apiErr.Retryable = resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusTooManyRequests
		return apiErr
	}
	apiErr.Code = body.Error.Code
	apiErr.Message = redactText(body.Error.Message)
	// Redacted too: a free-form field (details.reason) can carry a signed URL.
	apiErr.Details, _ = redactValue(body.Error.Details).(map[string]any)
	apiErr.DocURL = body.Error.DocURL
	apiErr.Retryable = body.Error.Retryable
	if body.Error.RequestID != nil && *body.Error.RequestID != "" {
		apiErr.RequestID = *body.Error.RequestID
	}
	return apiErr
}

// retriableNetworkError reports a failure that left the request unanswered: a timeout, a refused
// or reset connection, or an early EOF.
func retriableNetworkError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

// backoff is 250 ms doubled per attempt with ±20% jitter, capped at 5 s.
func backoff(attempt int) time.Duration {
	base := min(250*time.Millisecond<<(attempt-1), 5*time.Second)
	jitter := 0.8 + rand.Float64()*0.4 //nolint:gosec // jitter, not security
	return time.Duration(float64(base) * jitter)
}

func has(values []string, want string) bool { return slices.Contains(values, want) }

var (
	queryInURL   = regexp.MustCompile(`(https?://[^\s?"']+)\?[^\s"']*`)
	tokenSegment = regexp.MustCompile(`/(media-uploads|media-files)/[^/\s?"']+`)
	serviceKey   = regexp.MustCompile(`ocr_[A-Za-z0-9_\-]+`)
	bearer       = regexp.MustCompile(`(?i)bearer\s+[^\s"']+`)
)

// redactText removes query strings (signatures), token path segments and service keys.
// redactValue copies a decoded JSON value with every string redacted, for structured error fields.
func redactValue(value any) any {
	switch v := value.(type) {
	case string:
		return redactText(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = redactValue(item)
		}
		return out
	}
	return value
}

func redactText(text string) string {
	text = queryInURL.ReplaceAllString(text, "$1?<redacted>")
	text = tokenSegment.ReplaceAllString(text, "/$1/<redacted>")
	text = bearer.ReplaceAllString(text, "Bearer <redacted>")
	return serviceKey.ReplaceAllString(text, "ocr_<redacted>")
}

// redactURL is a URL without its query string or token segment, for errors.
func redactURL(u *url.URL) string {
	clean := *u
	clean.RawQuery = ""
	clean.User = nil
	return redactText(clean.String())
}
