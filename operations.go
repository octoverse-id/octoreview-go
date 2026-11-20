package octoreview

// retryClass is an operation's retry safety (the server's docs/sdk-surface.yaml).
type retryClass string

const (
	// retrySafe is a read: retried on timeouts, 503 and 429, honoring Retry-After.
	retrySafe retryClass = "safe"
	// retryIdempotent repeats to the same state and answer: retried like a read.
	retryIdempotent retryClass = "idempotent"
	// retryKeyed is idempotent once its idempotency key is sent; the SDK fills one in.
	retryKeyed retryClass = "keyed"
	// retryUnsafe is never retried automatically.
	retryUnsafe retryClass = "unsafe"
)

// operation is one REST operation: the contract gate checks every entry against the vendored
// docs/openapi.yaml (method, path) and docs/sdk-surface.yaml (Go method, retry class,
// parameters), in both directions.
type operation struct {
	ID     string
	Method string
	// Path is relative to /api/v1, with {name} path parameters.
	Path  string
	Retry retryClass
	// GoMethod is the surface name, Resource.Method.
	GoMethod string
	// Params is every parameter the method can send: path, query and body fields, plus
	// application_id and include_global, which come from client defaults or call options.
	Params []string
}

var (
	opGetHealthLive  = operation{"getHealthLive", "GET", "/health/live", retrySafe, "Health.Live", nil}
	opGetHealthReady = operation{"getHealthReady", "GET", "/health/ready", retrySafe, "Health.Ready", nil}

	opListEntities = operation{"listEntities", "GET", "/entities", retrySafe, "Entities.List",
		[]string{"application_id", "include_global", "entity_type", "external_id", "is_active", "limit", "offset"}}
	opUpsertEntity = operation{"upsertEntity", "PUT", "/entities", retryIdempotent, "Entities.Upsert",
		[]string{"application_id", "entity_type", "external_id", "metadata", "max_images", "max_videos", "max_image_bytes", "max_video_bytes"}}
	opLookupEntity = operation{"lookupEntity", "GET", "/entities/lookup", retrySafe, "Entities.Lookup",
		[]string{"application_id", "entity_type", "external_id", "include_global"}}
	opGetEntity = operation{"getEntity", "GET", "/entities/{entity_id}", retrySafe, "Entities.Get",
		[]string{"entity_id", "application_id", "include_global"}}
	opUpdateEntity = operation{"updateEntity", "PATCH", "/entities/{entity_id}", retryIdempotent, "Entities.Update",
		[]string{"entity_id", "application_id", "metadata", "max_images", "max_videos", "max_image_bytes", "max_video_bytes", "is_active"}}
	opDeactivateEntity = operation{"deactivateEntity", "DELETE", "/entities/{entity_id}", retryIdempotent, "Entities.Deactivate",
		[]string{"entity_id", "application_id"}}

	opGetEntityRating = operation{"getEntityRating", "GET", "/entities/{entity_id}/rating", retrySafe, "Ratings.ForEntity",
		[]string{"entity_id", "application_id", "include_global"}}
	opQueryRatings = operation{"queryRatings", "POST", "/entities/ratings/query", retrySafe, "Ratings.Query",
		[]string{"application_id", "entity_type", "external_ids", "include_global"}}

	opGetApplicationSettings = operation{"getApplicationSettings", "GET", "/applications/{application_id}/settings", retrySafe, "Settings.Get",
		[]string{"application_id"}}
	opPutApplicationSettings = operation{"putApplicationSettings", "PUT", "/applications/{application_id}/settings", retryIdempotent, "Settings.Put",
		[]string{"application_id", "max_images", "max_videos", "max_image_bytes", "max_video_bytes", "allowed_content_types", "moderation_mode", "max_storage_bytes"}}

	opListReviews = operation{"listReviews", "GET", "/reviews", retrySafe, "Reviews.List",
		[]string{"application_id", "include_global", "entity_id", "entity_type", "external_id", "author_id", "include_unpublished", "status", "rating", "has_media", "is_verified_purchase", "has_pending_revision", "media_moderation_status", "sort", "limit", "offset"}}
	opCreateReview = operation{"createReview", "POST", "/reviews", retryUnsafe, "Reviews.Create",
		[]string{"application_id", "author_id", "rating", "entity_id", "entity_type", "external_id", "title", "body", "metadata", "author_metadata", "source_ref", "is_verified_purchase"}}
	opGetReview = operation{"getReview", "GET", "/reviews/{review_id}", retrySafe, "Reviews.Get",
		[]string{"review_id", "application_id", "include_global", "author_id", "include_unpublished"}}
	opUpdateReview = operation{"updateReview", "PATCH", "/reviews/{review_id}", retryUnsafe, "Reviews.Update",
		[]string{"review_id", "application_id", "rating", "title", "body", "metadata", "author_metadata", "is_verified_purchase"}}
	opRemoveReview = operation{"removeReview", "DELETE", "/reviews/{review_id}", retryUnsafe, "Reviews.Remove",
		[]string{"review_id", "application_id"}}
	opPublishReview = operation{"publishReview", "POST", "/reviews/{review_id}/publish", retryUnsafe, "Reviews.Publish",
		[]string{"review_id", "expected_version", "expected_media_version", "application_id", "reason"}}
	opRejectReview = operation{"rejectReview", "POST", "/reviews/{review_id}/reject", retryUnsafe, "Reviews.Reject",
		[]string{"review_id", "application_id", "reason"}}
	opHideReview = operation{"hideReview", "POST", "/reviews/{review_id}/hide", retryUnsafe, "Reviews.Hide",
		[]string{"review_id", "application_id", "reason"}}
	opGetReviewRevision = operation{"getReviewRevision", "GET", "/reviews/{review_id}/revision", retrySafe, "Reviews.GetRevision",
		[]string{"review_id", "application_id"}}
	opApproveReviewRevision = operation{"approveReviewRevision", "POST", "/reviews/{review_id}/revision/approve", retryUnsafe, "Reviews.ApproveRevision",
		[]string{"review_id", "revision_id", "expected_version", "expected_media_version", "application_id"}}
	opRejectReviewRevision = operation{"rejectReviewRevision", "POST", "/reviews/{review_id}/revision/reject", retryUnsafe, "Reviews.RejectRevision",
		[]string{"review_id", "revision_id", "application_id"}}

	opListReviewMedia = operation{"listReviewMedia", "GET", "/reviews/{review_id}/media", retrySafe, "Media.List",
		[]string{"review_id", "application_id", "include_global", "author_id", "include_unpublished"}}
	opCreateMediaUpload = operation{"createMediaUpload", "POST", "/reviews/{review_id}/media", retryKeyed, "Media.CreateUpload",
		[]string{"review_id", "kind", "content_type", "size_bytes", "application_id", "client_upload_id", "position", "width", "height", "duration_seconds"}}
	opCompleteMediaUpload = operation{"completeMediaUpload", "POST", "/reviews/{review_id}/media/{media_id}/complete", retryIdempotent, "Media.Complete",
		[]string{"review_id", "media_id", "application_id"}}
	opDeleteMedia = operation{"deleteMedia", "DELETE", "/reviews/{review_id}/media/{media_id}", retryIdempotent, "Media.Delete",
		[]string{"review_id", "media_id", "application_id"}}
	opApproveMedia = operation{"approveMedia", "POST", "/reviews/{review_id}/media/{media_id}/approve", retryUnsafe, "Media.Approve",
		[]string{"review_id", "media_id", "expected_version", "expected_media_version", "application_id", "reason"}}
	opRejectMedia = operation{"rejectMedia", "POST", "/reviews/{review_id}/media/{media_id}/reject", retryIdempotent, "Media.Reject",
		[]string{"review_id", "media_id", "application_id", "reason"}}

	opEraseAuthor = operation{"eraseAuthor", "POST", "/authors/erase", retryIdempotent, "Authors.Erase",
		[]string{"application_id", "author_id"}}
	opListAuditLogs = operation{"listAuditLogs", "GET", "/audit-logs", retrySafe, "AuditLogs.List",
		[]string{"application_id", "include_global", "action", "aggregate_type", "aggregate_id", "actor_id", "limit", "offset"}}
)

// operations is the whole table, for the contract gate.
var operations = []operation{
	opGetHealthLive, opGetHealthReady,
	opListEntities, opUpsertEntity, opLookupEntity, opGetEntity, opUpdateEntity, opDeactivateEntity,
	opGetEntityRating, opQueryRatings,
	opGetApplicationSettings, opPutApplicationSettings,
	opListReviews, opCreateReview, opGetReview, opUpdateReview, opRemoveReview, opPublishReview,
	opRejectReview, opHideReview, opGetReviewRevision, opApproveReviewRevision, opRejectReviewRevision,
	opListReviewMedia, opCreateMediaUpload, opCompleteMediaUpload, opDeleteMedia, opApproveMedia,
	opRejectMedia,
	opEraseAuthor, opListAuditLogs,
}
