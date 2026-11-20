package octoreview

// Responses captured from the real server by `make fixtures` (testdata/fixtures), decoded offline
// through the SDK's own envelope decoding. Each check asserts non-default values, so a renamed or
// retyped field cannot decode silently to its zero value (server docs/spec.md section 18).

import (
	"os"
	"path/filepath"
	"testing"
)

func decodeFixture[T any](t *testing.T, opID string) T {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", opID+".json"))
	if err != nil {
		t.Fatalf("%s: no fixture (run make fixtures): %v", opID, err)
	}
	var out T
	var pagination Pagination
	if err := decodeEnvelope(raw, spec{op: operation{ID: opID}, out: &out, pagination: &pagination}); err != nil {
		t.Fatalf("%s: %v", opID, err)
	}
	return out
}

func check(t *testing.T, opID string, ok bool) {
	t.Helper()
	if !ok {
		t.Errorf("%s: the fixture decoded to default values", opID)
	}
}

func TestEveryFixtureDecodesWithRealValues(t *testing.T) {
	reviewOK := func(r Review) bool {
		return r.ID != "" && r.EntityID != "" && r.Rating > 0 && r.Version > 0 && !r.CreatedAt.IsZero() && r.Status != ""
	}
	entityOK := func(e Entity) bool {
		return e.ID != "" && e.EntityType != "" && e.ExternalID != "" && !e.CreatedAt.IsZero() && e.Rating.Histogram != nil
	}
	mediaOK := func(m Media) bool {
		return m.ID != "" && m.ReviewID != "" && m.SizeBytes > 0 && m.ContentType != "" && !m.CreatedAt.IsZero()
	}
	checks := map[string]func(){
		"getHealthLive":  func() { check(t, "getHealthLive", decodeFixture[HealthLive](t, "getHealthLive").Status == "ok") },
		"getHealthReady": func() { check(t, "getHealthReady", decodeFixture[HealthReady](t, "getHealthReady").Database == "ok") },
		"listEntities": func() {
			list := decodeFixture[[]Entity](t, "listEntities")
			check(t, "listEntities", len(list) > 0 && entityOK(list[0]))
		},
		"upsertEntity":     func() { check(t, "upsertEntity", entityOK(decodeFixture[Entity](t, "upsertEntity"))) },
		"lookupEntity":     func() { check(t, "lookupEntity", entityOK(decodeFixture[Entity](t, "lookupEntity"))) },
		"getEntity":        func() { check(t, "getEntity", entityOK(decodeFixture[Entity](t, "getEntity"))) },
		"updateEntity":     func() { check(t, "updateEntity", entityOK(decodeFixture[Entity](t, "updateEntity"))) },
		"deactivateEntity": func() { check(t, "deactivateEntity", !decodeFixture[Entity](t, "deactivateEntity").IsActive) },
		"getEntityRating": func() {
			r := decodeFixture[EntityRating](t, "getEntityRating")
			check(t, "getEntityRating", r.EntityID != "" && r.RatingCount > 0 && r.RatingAverage != nil && r.HistogramPercent != nil)
		},
		"queryRatings": func() {
			rows := decodeFixture[[]BatchRating](t, "queryRatings")
			check(t, "queryRatings", len(rows) > 0 && rows[0].ExternalID != "" && rows[0].RatingCount > 0)
		},
		"getApplicationSettings": func() {
			s := decodeFixture[ApplicationSettings](t, "getApplicationSettings")
			check(t, "getApplicationSettings", s.TenantID != "" && s.ApplicationID != "")
		},
		"putApplicationSettings": func() {
			s := decodeFixture[ApplicationSettings](t, "putApplicationSettings")
			check(t, "putApplicationSettings", s.ModerationMode != nil && *s.ModerationMode != "")
		},
		"listReviews": func() {
			list := decodeFixture[[]Review](t, "listReviews")
			check(t, "listReviews", len(list) > 0 && reviewOK(list[0]) && len(list[0].Media) > 0)
		},
		"createReview":  func() { check(t, "createReview", reviewOK(decodeFixture[Review](t, "createReview"))) },
		"getReview":     func() { check(t, "getReview", reviewOK(decodeFixture[Review](t, "getReview"))) },
		"updateReview":  func() { check(t, "updateReview", reviewOK(decodeFixture[Review](t, "updateReview"))) },
		"removeReview":  func() { check(t, "removeReview", decodeFixture[Review](t, "removeReview").Status == StatusRemoved) },
		"publishReview": func() { check(t, "publishReview", decodeFixture[Review](t, "publishReview").PublishedAt != nil) },
		"rejectReview":  func() { check(t, "rejectReview", decodeFixture[Review](t, "rejectReview").Status == StatusRejected) },
		"hideReview": func() {
			r := decodeFixture[Review](t, "hideReview")
			check(t, "hideReview", r.Status == StatusHidden && r.StatusReason != nil)
		},
		"getReviewRevision": func() {
			r := decodeFixture[Revision](t, "getReviewRevision")
			check(t, "getReviewRevision", r.ID != "" && r.ReviewID != "" && r.BaseVersion > 0 && !r.CreatedAt.IsZero())
		},
		"approveReviewRevision": func() {
			check(t, "approveReviewRevision", reviewOK(decodeFixture[Review](t, "approveReviewRevision")))
		},
		"rejectReviewRevision": func() {
			check(t, "rejectReviewRevision", reviewOK(decodeFixture[Review](t, "rejectReviewRevision")))
		},
		"listReviewMedia": func() {
			list := decodeFixture[[]Media](t, "listReviewMedia")
			check(t, "listReviewMedia", len(list) > 0 && mediaOK(list[0]) && list[0].URL != nil)
		},
		"createMediaUpload": func() {
			u := decodeFixture[MediaUpload](t, "createMediaUpload")
			check(t, "createMediaUpload", u.Media.ID != "" && u.Upload != nil && u.Upload.URL != "" && !u.Upload.ExpiresAt.IsZero())
		},
		"completeMediaUpload": func() {
			m := decodeFixture[Media](t, "completeMediaUpload")
			check(t, "completeMediaUpload", mediaOK(m) && m.Status == MediaReady)
		},
		"deleteMedia": func() { check(t, "deleteMedia", mediaOK(decodeFixture[Media](t, "deleteMedia"))) },
		"approveMedia": func() {
			check(t, "approveMedia", decodeFixture[Media](t, "approveMedia").ModerationStatus == "approved")
		},
		"rejectMedia": func() { check(t, "rejectMedia", decodeFixture[Media](t, "rejectMedia").ModerationStatus == "rejected") },
		"eraseAuthor": func() { check(t, "eraseAuthor", decodeFixture[EraseResult](t, "eraseAuthor").ReviewsRedacted > 0) },
		"listAuditLogs": func() {
			list := decodeFixture[[]AuditLog](t, "listAuditLogs")
			check(t, "listAuditLogs", len(list) > 0 && list[0].Action != "" && list[0].OperationID != "" && !list[0].CreatedAt.IsZero())
		},
	}
	for _, op := range operations {
		run, ok := checks[op.ID]
		if !ok {
			t.Errorf("%s has no fixture check", op.ID)
			continue
		}
		run()
	}
}
