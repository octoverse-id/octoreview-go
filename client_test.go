package octoreview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

// script answers each request with the next response (or error) and records the bodies.
type script struct {
	steps  []func(*http.Request) (*http.Response, error)
	bodies []string
	reqs   []*http.Request
}

func (s *script) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		s.bodies = append(s.bodies, string(raw))
	}
	s.reqs = append(s.reqs, req)
	step := s.steps[0]
	if len(s.steps) > 1 {
		s.steps = s.steps[1:]
	}
	return step(req)
}

func answer(status int, body string, headers ...string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		resp := response(status, body)
		for i := 0; i+1 < len(headers); i += 2 {
			resp.Header.Set(headers[i], headers[i+1])
		}
		return resp, nil
	}
}

func fail(err error) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) { return nil, err }
}

const apiKey = "ocr_sentinelkey_never_in_errors"

func newTestClient(t *testing.T, transport http.RoundTripper, opts ...Option) (*Client, *[]time.Duration) {
	t.Helper()
	client, err := New("https://reviews.example.com", apiKey, "tenant-t",
		append([]Option{WithHTTPClient(&http.Client{Transport: transport}), WithApplication("app-t")}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	slept := &[]time.Duration{}
	client.sleep = func(_ context.Context, d time.Duration) error {
		*slept = append(*slept, d)
		return nil
	}
	return client, slept
}

const unavailable = `{"error": {"code": "storage_unavailable", "message": "retry", "details": {}, "request_id": "r-503", "doc_url": "u", "retryable": true}}`

func TestReadsRetryOn503HonoringRetryAfter(t *testing.T) {
	s := &script{steps: []func(*http.Request) (*http.Response, error){
		answer(503, unavailable, "Retry-After", "2"),
		answer(200, `{"data": {"id": "e-1"}}`),
	}}
	client, slept := newTestClient(t, s)
	entity, err := client.Entities.Get(context.Background(), "e-1")
	if err != nil || entity.ID != "e-1" {
		t.Fatalf("got %v, %v", entity, err)
	}
	if len(*slept) != 1 || (*slept)[0] != 2*time.Second {
		t.Fatalf("slept %v, want one 2 s wait", *slept)
	}
}

func TestA429WithoutRetryAfterBacksOff(t *testing.T) {
	s := &script{steps: []func(*http.Request) (*http.Response, error){
		answer(429, `{"error": {"code": "validation_error", "message": "slow down", "details": {}, "request_id": null, "doc_url": "", "retryable": true}}`),
		answer(200, `{"data": {"status": "ok"}}`),
	}}
	client, slept := newTestClient(t, s)
	if _, err := client.Health.Live(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 1 || (*slept)[0] <= 0 || (*slept)[0] > 400*time.Millisecond {
		t.Fatalf("slept %v, want one short backoff", *slept)
	}
}

func TestARetryAfterBeyondTheBoundIsReturned(t *testing.T) {
	s := &script{steps: []func(*http.Request) (*http.Response, error){answer(503, unavailable, "Retry-After", "120")}}
	client, slept := newTestClient(t, s, WithRetry(3, 30*time.Second))
	_, err := client.Entities.Get(context.Background(), "e-1")
	apiErr, ok := AsAPIError(err)
	if !ok || apiErr.Code != CodeStorageUnavailable || apiErr.RetryAfter != 120 || len(*slept) != 0 {
		t.Fatalf("err %v, slept %v", err, *slept)
	}
}

func TestUnsafeWritesAreNeverRetried(t *testing.T) {
	s := &script{steps: []func(*http.Request) (*http.Response, error){answer(503, unavailable, "Retry-After", "1")}}
	client, slept := newTestClient(t, s)
	_, err := client.Reviews.Create(context.Background(), CreateReviewParams{EntityType: "p", ExternalID: "1", AuthorID: "u", Rating: 5})
	if !IsCode(err, CodeStorageUnavailable) || len(s.reqs) != 1 || len(*slept) != 0 {
		t.Fatalf("err %v after %d requests", err, len(s.reqs))
	}
}

func TestANetworkFailureIsRetriedForRetrySafeCalls(t *testing.T) {
	s := &script{steps: []func(*http.Request) (*http.Response, error){
		fail(syscall.ECONNRESET),
		answer(200, `{"data": {"id": "m-1", "status": "ready"}}`),
	}}
	client, _ := newTestClient(t, s)
	media, err := client.Media.Complete(context.Background(), "r-1", "m-1")
	if err != nil || media.Status != MediaReady || len(s.reqs) != 2 {
		t.Fatalf("got %v, %v after %d requests", media, err, len(s.reqs))
	}
}

func TestAKeyedIntentSendsOneGeneratedKeyAcrossRetries(t *testing.T) {
	s := &script{steps: []func(*http.Request) (*http.Response, error){
		answer(503, unavailable, "Retry-After", "0"),
		answer(201, `{"data": {"media": {"id": "m-1"}, "upload": {"method": "PUT", "url": "https://bucket/k", "headers": {"Content-Type": "image/png"}, "expires_at": "2026-11-20T00:00:00Z"}}}`),
	}}
	client, _ := newTestClient(t, s)
	upload, err := client.Media.CreateUpload(context.Background(), "r-1", CreateUploadParams{Kind: KindImage, ContentType: "image/png", SizeBytes: 9})
	if err != nil || upload.Upload == nil || upload.Upload.URL != "https://bucket/k" {
		t.Fatalf("got %+v, %v", upload, err)
	}
	var first, second map[string]any
	_ = json.Unmarshal([]byte(s.bodies[0]), &first)
	_ = json.Unmarshal([]byte(s.bodies[1]), &second)
	key, _ := first["client_upload_id"].(string)
	if !strings.HasPrefix(key, "go-") || second["client_upload_id"] != key {
		t.Fatalf("client_upload_id %v then %v", first["client_upload_id"], second["client_upload_id"])
	}
}

func TestConditionalReads(t *testing.T) {
	s := &script{steps: []func(*http.Request) (*http.Response, error){
		answer(200, `{"data": {"id": "r-1"}}`, "ETag", `"v1"`),
		answer(304, ``),
	}}
	client, _ := newTestClient(t, s)
	review, err := client.Reviews.Get(context.Background(), "r-1", GetReviewParams{})
	if err != nil || review.ETag != `"v1"` {
		t.Fatalf("got %v, %v", review, err)
	}
	_, err = client.Reviews.Get(context.Background(), "r-1", GetReviewParams{}, WithIfNoneMatch(review.ETag))
	if !errors.Is(err, ErrNotModified) || s.reqs[1].Header.Get("If-None-Match") != `"v1"` {
		t.Fatalf("err %v", err)
	}
}

func TestErrorsAreTyped(t *testing.T) {
	body := `{"error": {"code": "review_changed", "message": "The review changed since you loaded it; reload it.", "details": {"expected_version": 1, "current_version": 2}, "request_id": "req-9", "doc_url": "https://docs/errors.md#review_changed", "retryable": false}}`
	s := &script{steps: []func(*http.Request) (*http.Response, error){answer(409, body)}}
	client, _ := newTestClient(t, s)
	_, err := client.Reviews.Publish(context.Background(), "r-1", PublishParams{ExpectedVersion: 1, ExpectedMediaVersion: 1})
	apiErr, ok := AsAPIError(err)
	if !ok || !IsReviewChanged(err) || apiErr.Status != 409 || apiErr.RequestID != "req-9" ||
		apiErr.Details["current_version"] != float64(2) || apiErr.DocURL == "" || apiErr.Retryable {
		t.Fatalf("got %#v", apiErr)
	}
	s = &script{steps: []func(*http.Request) (*http.Response, error){answer(502, `<html>bad gateway</html>`)}}
	client, _ = newTestClient(t, s)
	_, err = client.Reviews.Create(context.Background(), CreateReviewParams{AuthorID: "u", Rating: 1})
	if !IsCode(err, "unexpected_response") {
		t.Fatalf("got %v", err)
	}
}

func TestNoErrorEverCarriesTheKeyOrASignature(t *testing.T) {
	leak := errors.New(`Put "https://bucket.s3.example/k?X-Amz-Signature=abc&X-Amz-Credential=AKIA": dial: ` +
		`/api/v1/media-uploads/c.tokenpart.sig refused; Authorization: Bearer ` + apiKey)
	s := &script{steps: []func(*http.Request) (*http.Response, error){fail(leak)}}
	client, _ := newTestClient(t, s)
	_, err := client.Reviews.Create(context.Background(), CreateReviewParams{AuthorID: "u", Rating: 1})
	text := err.Error()
	for _, secret := range []string{apiKey, "X-Amz-Signature=abc", "AKIA", "c.tokenpart.sig"} {
		if strings.Contains(text, secret) {
			t.Fatalf("the error leaks %q: %s", secret, text)
		}
	}
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("want a TransportError, got %T", err)
	}
}

func TestNamespacesAndApplications(t *testing.T) {
	s := &script{steps: []func(*http.Request) (*http.Response, error){answer(200, `{"data": {}}`)}}
	client, _ := newTestClient(t, s, WithNamespace("store", "s-1"))
	ctx := context.Background()
	_, _ = client.Entities.Get(ctx, "e-1")
	_, _ = client.Entities.Get(ctx, "e-1", InGlobalScope())
	_, _ = client.Entities.Get(ctx, "e-1", InNamespace("store", "s-2"), ForApplication("app-x"))
	headers := func(i int) string {
		r := s.reqs[i]
		return r.Header.Get("X-Namespace-Type") + "/" + r.Header.Get("X-Namespace-ID") + " " + r.URL.Query().Get("application_id")
	}
	if headers(0) != "store/s-1 app-t" || headers(1) != "/ app-t" || headers(2) != "store/s-2 app-x" {
		t.Fatalf("got %q, %q, %q", headers(0), headers(1), headers(2))
	}
	if _, err := client.Entities.Deactivate(ctx, "e-1", IncludeGlobal()); err == nil {
		t.Fatal("IncludeGlobal on a write must be refused")
	}
	_, _ = client.Settings.Get(ctx, "")
	last := s.reqs[len(s.reqs)-1]
	if last.URL.Path != "/api/v1/applications/app-t/settings" || last.Header.Get("X-Namespace-Type") != "" {
		t.Fatalf("settings call %s with namespace %q", last.URL.Path, last.Header.Get("X-Namespace-Type"))
	}
}

func TestReadinessReportsADownDatabaseAsAResult(t *testing.T) {
	s := &script{steps: []func(*http.Request) (*http.Response, error){
		answer(503, `{"data": {"status": "unavailable", "database": "down"}}`),
	}}
	client, slept := newTestClient(t, s, WithRetry(1, 0))
	ready, err := client.Health.Ready(context.Background())
	if err != nil || ready.Status != "unavailable" || ready.Database != "down" || len(*slept) != 0 {
		t.Fatalf("got %+v, %v", ready, err)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("OCTOREVIEW_BASE_URL", "")
	t.Setenv("OCTOREVIEW_API_KEY", "")
	t.Setenv("OCTOREVIEW_TENANT_ID", "")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "OCTOREVIEW_API_KEY") {
		t.Fatalf("want the missing variables named, got %v", err)
	}
	t.Setenv("OCTOREVIEW_BASE_URL", "http://localhost:8000")
	t.Setenv("OCTOREVIEW_API_KEY", "ocr_x")
	t.Setenv("OCTOREVIEW_TENANT_ID", "demo")
	t.Setenv("OCTOREVIEW_APPLICATION_ID", "storefront")
	client, err := FromEnv()
	if err != nil || client.applicationID != "storefront" || client.tenantID != "demo" {
		t.Fatalf("got %+v, %v", client, err)
	}
	if _, err := New("not a url", "ocr_x", "t"); err == nil {
		t.Fatal("a relative base URL must be refused")
	}
}

func TestNullableFields(t *testing.T) {
	raw, err := json.Marshal(UpdateEntityParams{MaxImages: Value[int64](3), MaxVideos: Null[int64]()})
	if err != nil || string(raw) != `{"max_images":3,"max_videos":null}` {
		t.Fatalf("got %s, %v", raw, err)
	}
	var n Nullable[int64]
	_ = json.Unmarshal([]byte(`null`), &n)
	if _, ok := n.Get(); ok || n.IsZero() {
		t.Fatal("null must be set but valueless")
	}
}

func TestCompleteRetriesItsDocumentedConflicts(t *testing.T) {
	conflict := func(code string) string {
		return `{"error": {"code": "` + code + `", "message": "m", "details": {}, "request_id": null, "doc_url": "", "retryable": true}}`
	}
	s := &script{steps: []func(*http.Request) (*http.Response, error){
		answer(409, conflict(CodeCompleteInProgress), "Retry-After", "3"),
		answer(409, conflict(CodeUploadChanged)),
		answer(200, `{"data": {"id": "m-1", "status": "ready"}}`),
	}}
	client, slept := newTestClient(t, s)
	media, err := client.Media.Complete(context.Background(), "r-1", "m-1")
	if err != nil || media.Status != MediaReady || len(s.reqs) != 3 {
		t.Fatalf("got %v, %v after %d requests", media, err, len(s.reqs))
	}
	if (*slept)[0] != 3*time.Second {
		t.Fatalf("slept %v, want the 3 s Retry-After first", *slept)
	}
	// Other operations do not retry a 409.
	s = &script{steps: []func(*http.Request) (*http.Response, error){answer(409, conflict(CodeCompleteInProgress))}}
	client, _ = newTestClient(t, s)
	if _, err := client.Media.Delete(context.Background(), "r-1", "m-1"); err == nil || len(s.reqs) != 1 {
		t.Fatalf("err %v after %d requests", err, len(s.reqs))
	}
}

func TestAnEmptyMetadataMapIsSentToClearIt(t *testing.T) {
	raw, _ := json.Marshal(UpdateReviewParams{Metadata: map[string]any{}, AuthorMetadata: map[string]any{}})
	if string(raw) != `{"metadata":{},"author_metadata":{}}` {
		t.Fatalf("got %s", raw)
	}
	raw, _ = json.Marshal(UpdateEntityParams{})
	if string(raw) != `{}` {
		t.Fatalf("an unset map must be omitted, got %s", raw)
	}
}
