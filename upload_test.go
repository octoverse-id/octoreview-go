package octoreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer is the octoreview API (intent, complete, delete) plus a storage endpoint for PUTs.
type fakeServer struct {
	t       *testing.T
	api     *httptest.Server
	storage *httptest.Server

	mu         sync.Mutex
	puts       []*http.Request
	putBodies  [][]byte
	putAnswers []func(http.ResponseWriter)
	deleted    []string
	completeAt int
	intents    []map[string]any
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{t: t}
	f.storage = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.puts = append(f.puts, r)
		f.putBodies = append(f.putBodies, body)
		var reply func(http.ResponseWriter)
		if len(f.putAnswers) > 0 {
			reply, f.putAnswers = f.putAnswers[0], f.putAnswers[1:]
		}
		f.mu.Unlock()
		if reply != nil {
			reply(w)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	f.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "req-"+r.Method)
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/media"):
			var intent map[string]any
			_ = json.NewDecoder(r.Body).Decode(&intent)
			f.mu.Lock()
			f.intents = append(f.intents, intent)
			f.mu.Unlock()
			expires := time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339)
			_, _ = io.WriteString(w, `{"data": {"media": {"id": "m-1", "status": "pending", "upload_expires_at": "`+expires+`"},
				"upload": {"method": "PUT", "url": "`+f.storage.URL+`/bucket/key?X-Amz-Signature=secret-sig",
				"headers": {"Content-Type": "`+intent["content_type"].(string)+`"}, "expires_at": "`+expires+`"}}}`)
		case strings.HasSuffix(r.URL.Path, "/complete"):
			f.mu.Lock()
			f.completeAt++
			f.mu.Unlock()
			_, _ = io.WriteString(w, `{"data": {"id": "m-1", "status": "ready", "url": "https://cdn/m-1.png"}}`)
		case r.Method == http.MethodDelete:
			f.mu.Lock()
			f.deleted = append(f.deleted, r.URL.Path)
			f.mu.Unlock()
			_, _ = io.WriteString(w, `{"data": {"id": "m-1", "status": "deleting"}}`)
		default:
			t.Errorf("unexpected API call %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(func() { f.api.Close(); f.storage.Close() })
	return f
}

func (f *fakeServer) client(t *testing.T, opts ...Option) *Client {
	t.Helper()
	client, err := New(f.api.URL, apiKey, "tenant-u", append([]Option{WithNamespace("store", "s-1")}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }
	return client
}

func TestUploadBytesSendsOnlyTheSignedHeaders(t *testing.T) {
	f := newFakeServer(t)
	data := []byte("\x89PNG\r\n\x1a\nrest-of-the-photo")
	media, err := f.client(t).Media.UploadBytes(context.Background(), "r-1", data, "image/png")
	if err != nil || media.Status != MediaReady {
		t.Fatalf("got %v, %v", media, err)
	}
	put := f.puts[0]
	for _, header := range []string{"Authorization", "X-Tenant-ID", "X-Namespace-Type", "X-Namespace-ID", "X-Actor-ID"} {
		if put.Header.Get(header) != "" {
			t.Errorf("the PUT sent %s", header)
		}
	}
	if put.Header.Get("Content-Type") != "image/png" || put.ContentLength != int64(len(data)) || len(put.TransferEncoding) != 0 {
		t.Errorf("PUT headers %v, length %d, encoding %v", put.Header, put.ContentLength, put.TransferEncoding)
	}
	if !bytes.Equal(f.putBodies[0], data) || f.intents[0]["kind"] != KindImage || f.intents[0]["size_bytes"] != float64(len(data)) {
		t.Errorf("intent %v", f.intents[0])
	}
}

func TestUploadFileInfersTypeAndSizeAndStreamsLargeBodies(t *testing.T) {
	f := newFakeServer(t)
	path := filepath.Join(t.TempDir(), "clip.MP4")
	large := bytes.Repeat([]byte("v"), 11<<20) // above 10 MiB
	if err := os.WriteFile(path, large, 0o600); err != nil {
		t.Fatal(err)
	}
	var lastSent, total int64
	_, err := f.client(t).Media.UploadFile(context.Background(), "r-1", path,
		WithProgress(func(sent, all int64) { lastSent, total = sent, all }))
	if err != nil {
		t.Fatal(err)
	}
	if f.intents[0]["content_type"] != "video/mp4" || f.intents[0]["kind"] != KindVideo || int64(len(f.putBodies[0])) != int64(len(large)) {
		t.Errorf("intent %v, PUT %d bytes", f.intents[0], len(f.putBodies[0]))
	}
	if lastSent != total || total != int64(len(large)) {
		t.Errorf("progress ended at %d of %d", lastSent, total)
	}
	if _, err := f.client(t).Media.UploadFile(context.Background(), "r-1", filepath.Join(t.TempDir(), "x.gif")); err == nil {
		t.Error("an unsupported extension must be refused")
	}
}

func TestAFailedPutIsRetriedFromTheStart(t *testing.T) {
	f := newFakeServer(t)
	f.putAnswers = []func(http.ResponseWriter){func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) }}
	data := []byte("\xff\xd8\xff-jpeg-bytes")
	if _, err := f.client(t).Media.UploadBytes(context.Background(), "r-1", data, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if len(f.puts) != 2 || !bytes.Equal(f.putBodies[1], data) {
		t.Fatalf("%d PUTs, second body %q", len(f.puts), f.putBodies[len(f.putBodies)-1])
	}
}

func TestATerminalPutFailureExplainsAndCleansUp(t *testing.T) {
	f := newFakeServer(t)
	f.putAnswers = []func(http.ResponseWriter){func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<?xml version="1.0"?><Error><Code>SignatureDoesNotMatch</Code><Message>no</Message></Error>`)
	}}
	_, err := f.client(t).Media.UploadBytes(context.Background(), "r-1", []byte("png"), "image/png")
	var uploadErr *UploadError
	if !errors.As(err, &uploadErr) || uploadErr.Stage != StagePut || uploadErr.ProviderCode != "SignatureDoesNotMatch" ||
		uploadErr.MediaID != "m-1" || uploadErr.Fix == "" || uploadErr.UploadExpiresAt.IsZero() {
		t.Fatalf("got %#v", err)
	}
	if len(f.puts) != 1 || len(f.deleted) != 1 || !strings.HasSuffix(f.deleted[0], "/reviews/r-1/media/m-1") {
		t.Fatalf("%d PUTs, deleted %v", len(f.puts), f.deleted)
	}
	if strings.Contains(err.Error(), "secret-sig") || strings.Contains(err.Error(), apiKey) {
		t.Fatalf("the error leaks the signed URL or key: %s", err)
	}
}

func TestRedirectsAreNeverFollowed(t *testing.T) {
	f := newFakeServer(t)
	f.putAnswers = []func(http.ResponseWriter){func(w http.ResponseWriter) {
		w.Header().Set("Location", "https://elsewhere.example/steal")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}}
	_, err := f.client(t).Media.UploadBytes(context.Background(), "r-1", []byte("png"), "image/png")
	var uploadErr *UploadError
	if !errors.As(err, &uploadErr) || !strings.Contains(uploadErr.Fix, "redirect") || len(f.puts) != 1 {
		t.Fatalf("got %v after %d PUTs", err, len(f.puts))
	}
}

func TestACompleteRejectionReportsItsStage(t *testing.T) {
	f := newFakeServer(t)
	f.api.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/complete") {
			w.Header().Set("X-Request-ID", "req-complete")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"error": {"code": "media_rejected", "message": "The bytes are not a PNG.", "details": {"reason": "type_mismatch"}, "request_id": "req-complete", "doc_url": "", "retryable": false}}`)
			return
		}
		expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		w.Header().Set("X-Request-ID", "req-intent")
		_, _ = io.WriteString(w, `{"data": {"media": {"id": "m-9"}, "upload": {"method": "PUT", "url": "`+f.storage.URL+`/k", "headers": {}, "expires_at": "`+expires+`"}}}`)
	})
	_, err := f.client(t).Media.UploadBytes(context.Background(), "r-1", []byte("not a png"), "image/png")
	var uploadErr *UploadError
	if !errors.As(err, &uploadErr) || uploadErr.Stage != StageComplete || !IsCode(err, CodeMediaRejected) ||
		strings.Join(uploadErr.RequestIDs, ",") != "req-intent,req-complete" {
		t.Fatalf("got %#v", err)
	}
}
