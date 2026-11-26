//go:build integration

package octoreview

// The integration suite against a real server (`make integration`, CI `integration`). It reads
// the harness's .octoreview-dev.env from OCTOREVIEW_DEV_ENV. With OCTOREVIEW_WRITE_FIXTURES=1 it
// also saves one response per operation into testdata/fixtures (`make fixtures`), which
// fixtures_test.go decodes offline.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 56))

// fixtureRecorder saves the first successful response body of each operation.
type fixtureRecorder struct {
	next  http.RoundTripper
	mu    sync.Mutex
	saved map[string]bool
}

func (f *fixtureRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := f.next.RoundTrip(req)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, err
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if readErr != nil {
		return resp, err
	}
	path := strings.TrimPrefix(req.URL.EscapedPath(), "/api/v1")
	for _, op := range operations {
		if op.Method == req.Method && pathMatches(op.Path, path) {
			f.mu.Lock()
			if !f.saved[op.ID] {
				f.saved[op.ID] = true
				_ = os.WriteFile(filepath.Join("testdata", "fixtures", op.ID+".json"), body, 0o644)
			}
			f.mu.Unlock()
		}
	}
	return resp, err
}

func integrationClient(t *testing.T) *Client {
	t.Helper()
	path := os.Getenv("OCTOREVIEW_DEV_ENV")
	if path == "" {
		t.Skip("set OCTOREVIEW_DEV_ENV to a harness .octoreview-dev.env (make integration)")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if key, value, ok := strings.Cut(scanner.Text(), "="); ok {
			values[key] = value
		}
	}
	transport := http.DefaultTransport
	if os.Getenv("OCTOREVIEW_WRITE_FIXTURES") == "1" {
		_ = os.MkdirAll(filepath.Join("testdata", "fixtures"), 0o755)
		transport = &fixtureRecorder{next: transport, saved: map[string]bool{}}
	}
	client, err := New(values["OCTOREVIEW_BASE_URL"], values["OCTOREVIEW_API_KEY"], values["OCTOREVIEW_TENANT_ID"],
		WithApplication(values["OCTOREVIEW_APPLICATION_ID"]), WithActor("go-integration"),
		WithHTTPClient(&http.Client{Timeout: 60 * time.Second, Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// must returns value or stops the test (through the recover in fatalOnPanic): Go cannot spread a
// two-value call into a call that also takes t.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func fatalOnPanic(t *testing.T) {
	if r := recover(); r != nil {
		t.Fatal(r)
	}
}

func TestIntegrationTheMainFlows(t *testing.T) {
	client := integrationClient(t)
	defer fatalOnPanic(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	run := fmt.Sprintf("go-%d", time.Now().UnixNano())

	live := must(client.Health.Live(ctx))
	ready := must(client.Health.Ready(ctx))
	if live.Status != "ok" || ready.Database != "ok" {
		t.Fatalf("health %+v %+v", live, ready)
	}

	// An entity allowing two images: the third is refused (the epic's acceptance check).
	entity := must(client.Entities.Upsert(ctx, UpsertEntityParams{EntityType: "product", ExternalID: run,
		Metadata: map[string]any{"source": "go"}, MaxImages: Value[int64](2)}))
	review := must(client.Reviews.Create(ctx, CreateReviewParams{EntityID: entity.ID, AuthorID: run + "-a",
		Rating: 4, Title: Ptr("Good"), IsVerifiedPurchase: Ptr(true)}))
	if review.Status != StatusPublished {
		t.Fatalf("auto-publish left the review %s", review.Status)
	}
	for i := 0; i < 2; i++ {
		media := must(client.Media.UploadBytes(ctx, review.ID, pngBytes, "image/png", WithPosition(i)))
		if media.Status != MediaReady || media.URL == nil {
			t.Fatalf("upload %d: %+v", i, media)
		}
		served, err := http.Get(*media.URL)
		if err != nil || served.StatusCode != http.StatusOK {
			t.Fatalf("fetch the photo: %v %v", served, err)
		}
		_ = served.Body.Close()
	}
	_, err := client.Media.UploadBytes(ctx, review.ID, pngBytes, "image/png")
	var uploadErr *UploadError
	if !errors.As(err, &uploadErr) || uploadErr.Stage != StageIntent || !IsCode(err, CodeMediaLimitExceeded) {
		t.Fatalf("the third image: %v", err)
	}

	summary := must(client.Ratings.Get(ctx, "product", run))
	batch := must(client.Ratings.Query(ctx, QueryRatingsParams{EntityType: "product", ExternalIDs: []string{run, run + "-missing"}}))
	forEntity := must(client.Ratings.ForEntity(ctx, entity.ID))
	if summary.RatingCount != 1 || len(batch) != 2 || batch[1].EntityID != nil || forEntity.RatingCount != 1 {
		t.Fatalf("ratings %+v %+v %+v", summary, batch, forEntity)
	}
	// The server answers conditional reads from 1.0.0-rc.4 (its spec 5.3). The rating summary's
	// ETag does not carry the signing epoch, so an unchanged summary always answers 304.
	if forEntity.ETag == "" {
		t.Fatal("the rating summary carries no ETag")
	}
	if _, err := client.Ratings.ForEntity(ctx, entity.ID, WithIfNoneMatch(forEntity.ETag)); !errors.Is(err, ErrNotModified) {
		t.Fatalf("a conditional rating read: %v", err)
	}
	listed := must(client.Reviews.List(ctx, ListReviewsParams{EntityID: entity.ID, HasMedia: Ptr(true)}))
	if listed.Pagination.Count != 1 || len(listed.Data[0].Media) != 2 {
		t.Fatalf("list %+v", listed)
	}
	media := must(client.Media.List(ctx, review.ID, ListMediaParams{}))
	if len(media.Data) != 2 || media.Pagination.Count != 2 {
		t.Fatalf("media %+v", media)
	}
	second := must(client.Media.List(ctx, review.ID, ListMediaParams{Page: Page{Limit: 1, Offset: 1}}))
	if len(second.Data) != 1 || second.Data[0].ID != media.Data[1].ID || second.Pagination.Limit != 1 {
		t.Fatalf("second page %+v", second)
	}

	// Pre-moderation: a new review waits; the version guard protects publishing.
	// Put replaces every setting, so both calls send them all: only the moderation mode changes,
	// and the original settings come back afterwards.
	app := must(client.Settings.Get(ctx, ""))
	original := settingsOf(app)
	preModeration := original
	preModeration.ModerationMode = Ptr(ModerationPreModeration)
	must(client.Settings.Put(ctx, "", preModeration))
	defer func() { _, _ = client.Settings.Put(context.Background(), "", original) }()
	pending := must(client.Reviews.Create(ctx, CreateReviewParams{EntityID: entity.ID, AuthorID: run + "-b", Rating: 2}))
	if pending.Status != StatusPending {
		t.Fatalf("pre-moderation left the review %s", pending.Status)
	}
	_, err = client.Reviews.Publish(ctx, pending.ID, PublishParams{ExpectedVersion: pending.Version + 1, ExpectedMediaVersion: pending.MediaVersion})
	if !IsReviewChanged(err) {
		t.Fatalf("a stale version: %v", err)
	}
	published := must(client.Reviews.Publish(ctx, pending.ID, PublishParams{ExpectedVersion: pending.Version, ExpectedMediaVersion: pending.MediaVersion}))
	if published.Status != StatusPublished {
		t.Fatalf("publish: %s", published.Status)
	}

	// An edit of a published review under pre-moderation becomes a revision.
	edited := must(client.Reviews.Update(ctx, published.ID, UpdateReviewParams{Rating: Ptr(3)}))
	revision := must(client.Reviews.GetRevision(ctx, edited.ID))
	approved := must(client.Reviews.ApproveRevision(ctx, edited.ID, ApproveRevisionParams{
		RevisionID: revision.ID, ExpectedVersion: edited.Version, ExpectedMediaVersion: edited.MediaVersion}))
	if approved.Rating != 3 {
		t.Fatalf("the approved revision left rating %d", approved.Rating)
	}

	// Media on a published review waits for moderation under pre-moderation.
	photo := must(client.Media.UploadBytes(ctx, approved.ID, pngBytes, "image/png"))
	current := must(client.Reviews.Get(ctx, approved.ID, GetReviewParams{}))
	moderated := must(client.Media.Approve(ctx, approved.ID, photo.ID, ApproveMediaParams{
		ExpectedVersion: current.Version, ExpectedMediaVersion: current.MediaVersion}))
	if moderated.ModerationStatus != "approved" {
		t.Fatalf("media moderation: %s", moderated.ModerationStatus)
	}
	must(client.Media.Reject(ctx, approved.ID, photo.ID, ModerationParams{Reason: Ptr("blurry")}))
	must(client.Media.Delete(ctx, approved.ID, photo.ID))

	must(client.Reviews.Hide(ctx, approved.ID, ModerationParams{Reason: Ptr("spam")}))
	rejectable := must(client.Reviews.Create(ctx, CreateReviewParams{EntityID: entity.ID, AuthorID: run + "-c", Rating: 1}))
	must(client.Reviews.Reject(ctx, rejectable.ID, ModerationParams{}))
	must(client.Reviews.Remove(ctx, rejectable.ID))
	revised := must(client.Reviews.Update(ctx, review.ID, UpdateReviewParams{Body: Value("an edit to drop")}))
	dropped := must(client.Reviews.GetRevision(ctx, revised.ID))
	must(client.Reviews.RejectRevision(ctx, revised.ID, RejectRevisionParams{RevisionID: dropped.ID}))

	logs := must(client.AuditLogs.List(ctx, ListAuditLogsParams{AggregateID: review.ID}))
	if logs.Pagination.Count == 0 {
		t.Fatal("no audit rows for the review")
	}
	erased := must(client.Authors.Erase(ctx, EraseAuthorParams{AuthorID: run + "-a"}))
	if erased.ReviewsRedacted != 1 {
		t.Fatalf("erase %+v", erased)
	}
	found := must(client.Entities.Lookup(ctx, "product", run))
	got := must(client.Entities.Get(ctx, found.ID))
	listedEntities := must(client.Entities.List(ctx, ListEntitiesParams{ExternalID: run}))
	updated := must(client.Entities.Update(ctx, got.ID, UpdateEntityParams{MaxImages: Null[int64]()}))
	deactivated := must(client.Entities.Deactivate(ctx, updated.ID))
	if listedEntities.Pagination.Count != 1 || deactivated.IsActive || updated.MediaPolicy.MaxImages != nil {
		t.Fatalf("entities %+v %+v", listedEntities, deactivated)
	}
}

// settingsOf turns read settings into the Put parameters that keep them as they are.
func settingsOf(app *ApplicationSettings) PutSettingsParams {
	params := PutSettingsParams{
		MaxImages: app.MaxImages, MaxVideos: app.MaxVideos, MaxImageBytes: app.MaxImageBytes,
		MaxVideoBytes: app.MaxVideoBytes, ModerationMode: app.ModerationMode, MaxStorageBytes: app.MaxStorageBytes,
	}
	if app.AllowedContentTypes != nil {
		types := append([]string(nil), app.AllowedContentTypes...)
		params.AllowedContentTypes = &types
	}
	return params
}
