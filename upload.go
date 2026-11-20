package octoreview

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Upload stages, reported in UploadError.Stage.
const (
	StageIntent   = "intent"
	StagePut      = "put"
	StageComplete = "complete"
)

// UploadError is a failed UploadFile, UploadBytes or UploadStream. It names the stage that failed
// and the ids to look the upload up by; it never contains the signed upload URL.
type UploadError struct {
	Stage           string
	ReviewID        string
	MediaID         string
	UploadExpiresAt time.Time
	// RequestIDs are the server request ids of the calls this upload made, oldest first.
	RequestIDs []string
	// ProviderCode is the storage provider's error code for a failed PUT (S3's XML <Code>, or the
	// local backend's error code), and Fix says what to change.
	ProviderCode string
	Fix          string
	Err          error
}

func (e *UploadError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "octoreview: upload failed at %s (review %s", e.Stage, e.ReviewID)
	if e.MediaID != "" {
		fmt.Fprintf(&b, ", media %s", e.MediaID)
	}
	b.WriteString(")")
	if e.ProviderCode != "" {
		fmt.Fprintf(&b, ": %s", e.ProviderCode)
	}
	if e.Fix != "" {
		fmt.Fprintf(&b, "; %s", e.Fix)
	}
	if e.Err != nil {
		fmt.Fprintf(&b, ": %s", redactText(e.Err.Error()))
	}
	return b.String()
}

func (e *UploadError) Unwrap() error { return e.Err }

// UploadOption adjusts one upload helper call.
type UploadOption func(*uploadConfig)

type uploadConfig struct {
	progress        func(sent, total int64)
	clientUploadID  string
	position        *int
	width, height   *int
	durationSeconds *float64
	callOptions     []CallOption
}

// WithProgress reports the bytes sent so far during the PUT, and the total.
func WithProgress(fn func(sent, total int64)) UploadOption {
	return func(cfg *uploadConfig) { cfg.progress = fn }
}

// WithClientUploadID makes the intent replayable across processes (one is generated otherwise).
func WithClientUploadID(id string) UploadOption {
	return func(cfg *uploadConfig) { cfg.clientUploadID = id }
}

// WithPosition orders the media item within its review.
func WithPosition(position int) UploadOption {
	return func(cfg *uploadConfig) { cfg.position = &position }
}

// WithDimensions records an image's or video's pixel size.
func WithDimensions(width, height int) UploadOption {
	return func(cfg *uploadConfig) { cfg.width, cfg.height = &width, &height }
}

// WithDuration records a video's length.
func WithDuration(seconds float64) UploadOption {
	return func(cfg *uploadConfig) { cfg.durationSeconds = &seconds }
}

// WithUploadCallOptions passes call options (application, namespace, actor) to the intent,
// complete and cleanup calls.
func WithUploadCallOptions(opts ...CallOption) UploadOption {
	return func(cfg *uploadConfig) { cfg.callOptions = append(cfg.callOptions, opts...) }
}

// WithUploadHTTPClient replaces the HTTP client used for the media PUT. It must not follow
// redirects; the default sets no timeout (cancel with the context) and refuses redirects.
func WithUploadHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) error {
		if httpClient == nil {
			return errors.New("octoreview: WithUploadHTTPClient got a nil client")
		}
		c.uploadClient = httpClient
		return nil
	}
}

func defaultUploadClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

var contentTypes = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp",
	".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime",
}

// UploadFile uploads a file: intent, PUT and complete. The content type comes from the file's
// extension (.jpg, .jpeg, .png, .webp, .mp4, .webm, .mov) and the size from the file.
func (s *MediaService) UploadFile(ctx context.Context, reviewID, path string, opts ...UploadOption) (*Media, error) {
	contentType, ok := contentTypes[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, &UploadError{Stage: StageIntent, ReviewID: reviewID,
			Err: fmt.Errorf("cannot tell the content type of %s; use UploadStream with one of image/jpeg, image/png, image/webp, video/mp4, video/webm, video/quicktime", filepath.Base(path))}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, &UploadError{Stage: StageIntent, ReviewID: reviewID, Err: err}
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, &UploadError{Stage: StageIntent, ReviewID: reviewID, Err: err}
	}
	return s.UploadStream(ctx, reviewID, file, contentType, info.Size(), opts...)
}

// UploadBytes uploads an in-memory file: intent, PUT and complete.
func (s *MediaService) UploadBytes(ctx context.Context, reviewID string, data []byte, contentType string, opts ...UploadOption) (*Media, error) {
	return s.UploadStream(ctx, reviewID, bytes.NewReader(data), contentType, int64(len(data)), opts...)
}

// UploadStream uploads sizeBytes from r: intent, PUT and complete, returning the ready media.
// r is rewound before each PUT attempt, so it must be seekable. The PUT goes to the signed URL
// with only the headers the server returned and an explicit Content-Length: never the service
// key, tenant or namespace headers.
func (s *MediaService) UploadStream(ctx context.Context, reviewID string, r io.ReadSeeker, contentType string, sizeBytes int64, opts ...UploadOption) (*Media, error) {
	cfg := uploadConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	kind := KindImage
	if strings.HasPrefix(contentType, "video/") {
		kind = KindVideo
	} else if !strings.HasPrefix(contentType, "image/") {
		return nil, &UploadError{Stage: StageIntent, ReviewID: reviewID, Err: fmt.Errorf("content type %q is neither an image nor a video", contentType)}
	}
	intent, err := s.CreateUpload(ctx, reviewID, CreateUploadParams{
		Kind: kind, ContentType: contentType, SizeBytes: sizeBytes, ClientUploadID: cfg.clientUploadID,
		Position: cfg.position, Width: cfg.width, Height: cfg.height, DurationSeconds: cfg.durationSeconds,
	}, cfg.callOptions...)
	if err != nil {
		return nil, &UploadError{Stage: StageIntent, ReviewID: reviewID, RequestIDs: requestIDs(err), Err: err}
	}
	failure := func(stage string, err error) *UploadError {
		return &UploadError{Stage: stage, ReviewID: reviewID, MediaID: intent.Media.ID,
			UploadExpiresAt: intent.Media.UploadExpiresAt, RequestIDs: append(nonEmpty(intent.RequestID), requestIDs(err)...), Err: err}
	}
	if intent.Upload == nil {
		// A replayed intent whose upload already completed (or expired).
		if intent.Media.Status == MediaReady {
			return &intent.Media, nil
		}
		return nil, failure(StageIntent, errors.New("this upload is no longer uploadable; create a new one"))
	}
	if err := s.c.put(ctx, intent.Upload, r, sizeBytes, cfg.progress); err != nil {
		uploadErr := failure(StagePut, err)
		var putErr *putError
		if errors.As(err, &putErr) {
			uploadErr.ProviderCode, uploadErr.Fix = putErr.code, putErr.fix
		}
		// Free the slot now rather than at the cleanup after expiry (best effort).
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, _ = s.Delete(cleanup, reviewID, intent.Media.ID, cfg.callOptions...)
		return nil, uploadErr
	}
	media, err := s.Complete(ctx, reviewID, intent.Media.ID, cfg.callOptions...)
	if err != nil {
		return nil, failure(StageComplete, err)
	}
	return media, nil
}

// putError is a failed PUT with the provider's code and a fix.
type putError struct {
	status int
	code   string
	fix    string
}

func (e *putError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("the storage PUT answered HTTP %d (%s)", e.status, e.code)
	}
	return fmt.Sprintf("the storage PUT answered HTTP %d", e.status)
}

var providerFixes = map[string]string{
	"SignatureDoesNotMatch": "send exactly upload.headers and the declared Content-Length; the URL signs both, and the clock must be right",
	"AccessDenied":          "the URL expired or its signer cannot write to the bucket; create a new upload",
	"ExpiredToken":          "the URL's signing credential expired; create a new upload",
	"RequestTimeTooSkewed":  "this machine's clock is off; sync it",
	"EntityTooLarge":        "the body is larger than the declared size",
	"NoSuchBucket":          "the server's bucket setting is wrong; tell its operator",
	CodeUploadTooLarge:      "the body is larger than the declared size",
	CodeUploadTokenExpired:  "the upload URL expired; create a new upload",
	CodeUploadTokenInvalid:  "the upload URL is not valid; create a new upload",
	CodeUploadAlreadyCompleted: "this upload already completed; create a new one to upload another " +
		"file",
}

// put sends the bytes to the signed URL, retrying transient failures until the URL expires.
func (c *Client) put(ctx context.Context, target *UploadTarget, r io.ReadSeeker, size int64, progress func(int64, int64)) error {
	const attempts = 3
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if _, err := r.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("rewind the stream: %w", err)
		}
		retryIn, err := c.putOnce(ctx, target, r, size, progress)
		if err == nil {
			return nil
		}
		lastErr = err
		if retryIn < 0 || attempt == attempts || ctx.Err() != nil {
			return err
		}
		if retryIn == 0 {
			retryIn = backoff(attempt)
		}
		if time.Now().Add(retryIn).After(target.ExpiresAt) {
			return err // the URL would expire before the retry
		}
		if sleepErr := c.sleep(ctx, retryIn); sleepErr != nil {
			return err
		}
	}
	return lastErr
}

func (c *Client) putOnce(ctx context.Context, target *UploadTarget, r io.Reader, size int64, progress func(int64, int64)) (time.Duration, error) {
	body := io.LimitReader(r, size)
	if progress != nil {
		body = &progressReader{r: body, total: size, report: progress}
	}
	method := target.Method
	if method == "" {
		method = http.MethodPut
	}
	req, err := http.NewRequestWithContext(ctx, method, target.URL, body)
	if err != nil {
		return -1, errors.New("the upload URL is not usable")
	}
	// Only the signed headers: never Authorization, X-Tenant-ID or namespace headers.
	for name, value := range target.Headers {
		req.Header.Set(name, value)
	}
	req.ContentLength = size // explicit Content-Length, never chunked
	resp, err := c.uploadClient.Do(req)
	if err != nil {
		redacted := &TransportError{Method: method, URL: redactText(strings.SplitN(target.URL, "?", 2)[0]), Err: err}
		if ctx.Err() == nil && retriableNetworkError(err) {
			return 0, redacted
		}
		return -1, redacted
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return 0, nil
	}
	failure := &putError{status: resp.StatusCode, code: providerCode(raw)}
	failure.fix = providerFixes[failure.code]
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		failure.fix = "the upload URL redirected, which uploads never follow; check the storage endpoint"
		return -1, failure
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds >= 0 {
			wait := time.Duration(seconds) * time.Second
			if wait > c.maxRetryAfter {
				return -1, failure
			}
			return wait, failure
		}
		return 0, failure
	}
	return -1, failure
}

// providerCode reads S3's XML <Code>, or the local backend's JSON error code.
func providerCode(raw []byte) string {
	var xmlErr struct {
		Code string `xml:"Code"`
	}
	if xml.Unmarshal(raw, &xmlErr) == nil && xmlErr.Code != "" {
		return xmlErr.Code
	}
	var jsonErr struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &jsonErr) == nil {
		return jsonErr.Error.Code
	}
	return ""
}

type progressReader struct {
	r      io.Reader
	sent   int64
	total  int64
	report func(sent, total int64)
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.sent += int64(n)
		p.report(p.sent, p.total)
	}
	return n, err
}

func requestIDs(err error) []string {
	if apiErr, ok := AsAPIError(err); ok && apiErr.RequestID != "" {
		return []string{apiErr.RequestID}
	}
	return nil
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
