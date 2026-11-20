package octoreview

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// query builds a url.Values from the set fields only.
type query url.Values

func (q query) str(key, value string) {
	if value != "" {
		url.Values(q).Set(key, value)
	}
}

func (q query) num(key string, value *int) {
	if value != nil {
		url.Values(q).Set(key, strconv.Itoa(*value))
	}
}

func (q query) boolean(key string, value *bool) {
	if value != nil {
		url.Values(q).Set(key, strconv.FormatBool(*value))
	}
}

// Page is limit/offset paging for list calls; zero values use the server's defaults (limit 20).
type Page struct {
	Limit  int
	Offset int
}

func (q query) page(p Page) {
	if p.Limit > 0 {
		url.Values(q).Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Offset > 0 {
		url.Values(q).Set("offset", strconv.Itoa(p.Offset))
	}
}

func list[T any](ctx context.Context, c *Client, s spec, cfg callConfig) (*List[T], error) {
	result := &List[T]{}
	s.out = &result.Data
	s.pagination = &result.Pagination
	meta, err := c.call(ctx, s, cfg)
	if err != nil {
		return nil, err
	}
	result.ResponseMeta = meta
	return result, nil
}

// one runs a call that answers a single resource T embedding ResponseMeta.
func one[T any](ctx context.Context, c *Client, s spec, cfg callConfig, meta func(*T, ResponseMeta)) (*T, error) {
	result := new(T)
	s.out = result
	m, err := c.call(ctx, s, cfg)
	if err != nil {
		return nil, err
	}
	meta(result, m)
	return result, nil
}

// --- health ---------------------------------------------------------------------------------

// HealthService reaches the unauthenticated probes.
type HealthService struct{ c *Client }

// Live reports that the server process serves.
func (s *HealthService) Live(ctx context.Context, opts ...CallOption) (*HealthLive, error) {
	return one(ctx, s.c, spec{op: opGetHealthLive}, s.c.config(opts),
		func(r *HealthLive, m ResponseMeta) { r.ResponseMeta = m })
}

// Ready reports whether the server and its database serve; Status is "unavailable" (not an
// error) when the database is down.
func (s *HealthService) Ready(ctx context.Context, opts ...CallOption) (*HealthReady, error) {
	return one(ctx, s.c, spec{op: opGetHealthReady, acceptStatus: http.StatusServiceUnavailable},
		s.c.config(opts), func(r *HealthReady, m ResponseMeta) { r.ResponseMeta = m })
}

// --- entities -------------------------------------------------------------------------------

// EntitiesService manages reviewable entities.
type EntitiesService struct{ c *Client }

// ListEntitiesParams filters Entities.List.
type ListEntitiesParams struct {
	EntityType string
	ExternalID string
	IsActive   *bool
	Page
}

// List returns one page of entities.
func (s *EntitiesService) List(ctx context.Context, params ListEntitiesParams, opts ...CallOption) (*List[Entity], error) {
	q := query{}
	q.str("entity_type", params.EntityType)
	q.str("external_id", params.ExternalID)
	q.boolean("is_active", params.IsActive)
	q.page(params.Page)
	return list[Entity](ctx, s.c, spec{op: opListEntities, query: url.Values(q)}, s.c.config(opts))
}

// UpsertEntityParams creates or updates an entity by its key. ApplicationID defaults to the
// client's. Media limits left unset are not changed; [Null] clears an override.
type UpsertEntityParams struct {
	ApplicationID string                      `json:"application_id"`
	EntityType    string                      `json:"entity_type"`
	ExternalID    string                      `json:"external_id"`
	Metadata      map[string]any              `json:"metadata,omitzero"`
	MaxImages     Nullable[int64]             `json:"max_images,omitzero"`
	MaxVideos     Nullable[int64]             `json:"max_videos,omitzero"`
	MaxImageBytes Nullable[int64]             `json:"max_image_bytes,omitzero"`
	MaxVideoBytes Nullable[int64]             `json:"max_video_bytes,omitzero"`
	_             struct{}                    `json:"-"`
	_             [0]func(UpsertEntityParams) // not comparable: always pass a fresh value
}

// Upsert creates the entity or updates its metadata and media limits (retry-safe).
func (s *EntitiesService) Upsert(ctx context.Context, params UpsertEntityParams, opts ...CallOption) (*Entity, error) {
	cfg := s.c.config(opts)
	if params.ApplicationID == "" {
		params.ApplicationID = s.c.application(cfg)
	}
	return one(ctx, s.c, spec{op: opUpsertEntity, body: params}, cfg, entityMeta)
}

// Lookup finds an entity by entity type and external id in the call's application.
func (s *EntitiesService) Lookup(ctx context.Context, entityType, externalID string, opts ...CallOption) (*Entity, error) {
	q := query{}
	q.str("entity_type", entityType)
	q.str("external_id", externalID)
	return one(ctx, s.c, spec{op: opLookupEntity, query: url.Values(q)}, s.c.config(opts), entityMeta)
}

// Get returns an entity by id.
func (s *EntitiesService) Get(ctx context.Context, entityID string, opts ...CallOption) (*Entity, error) {
	return one(ctx, s.c, spec{op: opGetEntity, pathValues: []string{entityID}}, s.c.config(opts), entityMeta)
}

// UpdateEntityParams changes an entity; unset fields stay as they are.
type UpdateEntityParams struct {
	Metadata      map[string]any  `json:"metadata,omitzero"`
	MaxImages     Nullable[int64] `json:"max_images,omitzero"`
	MaxVideos     Nullable[int64] `json:"max_videos,omitzero"`
	MaxImageBytes Nullable[int64] `json:"max_image_bytes,omitzero"`
	MaxVideoBytes Nullable[int64] `json:"max_video_bytes,omitzero"`
	IsActive      *bool           `json:"is_active,omitempty"`
}

// Update changes an entity's metadata, media limits or active flag (retry-safe).
func (s *EntitiesService) Update(ctx context.Context, entityID string, params UpdateEntityParams, opts ...CallOption) (*Entity, error) {
	return one(ctx, s.c, spec{op: opUpdateEntity, pathValues: []string{entityID}, body: params}, s.c.config(opts), entityMeta)
}

// Deactivate stops new reviews for an entity; its rating stays readable (retry-safe).
func (s *EntitiesService) Deactivate(ctx context.Context, entityID string, opts ...CallOption) (*Entity, error) {
	return one(ctx, s.c, spec{op: opDeactivateEntity, pathValues: []string{entityID}}, s.c.config(opts), entityMeta)
}

func entityMeta(e *Entity, m ResponseMeta) { e.ResponseMeta = m }

// --- ratings --------------------------------------------------------------------------------

// RatingsService reads rating summaries.
type RatingsService struct{ c *Client }

// ForEntity returns an entity's rating summary by entity id.
func (s *RatingsService) ForEntity(ctx context.Context, entityID string, opts ...CallOption) (*EntityRating, error) {
	return one(ctx, s.c, spec{op: opGetEntityRating, pathValues: []string{entityID}}, s.c.config(opts),
		func(r *EntityRating, m ResponseMeta) { r.ResponseMeta = m })
}

// QueryRatingsParams asks for the summaries of many external ids at once (a listing page).
type QueryRatingsParams struct {
	ApplicationID string   `json:"application_id"`
	EntityType    string   `json:"entity_type"`
	ExternalIDs   []string `json:"external_ids"`
}

// Query returns one summary per external id, in request order; unknown ids answer zeros.
func (s *RatingsService) Query(ctx context.Context, params QueryRatingsParams, opts ...CallOption) ([]BatchRating, error) {
	cfg := s.c.config(opts)
	if params.ApplicationID == "" {
		params.ApplicationID = s.c.application(cfg)
	}
	var ratings []BatchRating
	if _, err := s.c.call(ctx, spec{op: opQueryRatings, body: params, out: &ratings}, cfg); err != nil {
		return nil, err
	}
	return ratings, nil
}

// Get returns the summary of {entityType, externalID} without knowing the entity id (the
// ratingsByKey helper; it composes Entities.Lookup). An entity that does not exist yet answers
// [IsNotFound].
func (s *RatingsService) Get(ctx context.Context, entityType, externalID string, opts ...CallOption) (*RatingSummary, error) {
	entity, err := s.c.Entities.Lookup(ctx, entityType, externalID, opts...)
	if err != nil {
		return nil, err
	}
	return &entity.Rating, nil
}

// --- settings -------------------------------------------------------------------------------

// SettingsService manages an application's media policy defaults, moderation mode and quota.
type SettingsService struct{ c *Client }

// Get returns an application's settings; an empty applicationID uses the client's default.
func (s *SettingsService) Get(ctx context.Context, applicationID string, opts ...CallOption) (*ApplicationSettings, error) {
	cfg := s.c.config(opts)
	if applicationID == "" {
		applicationID = s.c.application(cfg)
	}
	return one(ctx, s.c, spec{op: opGetApplicationSettings, pathValues: []string{applicationID}}, cfg, settingsMeta)
}

// PutSettingsParams replaces an application's settings. Every field is sent: a nil value (null)
// inherits the server default.
type PutSettingsParams struct {
	MaxImages           *int64    `json:"max_images"`
	MaxVideos           *int64    `json:"max_videos"`
	MaxImageBytes       *int64    `json:"max_image_bytes"`
	MaxVideoBytes       *int64    `json:"max_video_bytes"`
	AllowedContentTypes *[]string `json:"allowed_content_types"`
	ModerationMode      *string   `json:"moderation_mode"`
	MaxStorageBytes     *int64    `json:"max_storage_bytes"`
}

// Put replaces an application's settings (retry-safe).
func (s *SettingsService) Put(ctx context.Context, applicationID string, params PutSettingsParams, opts ...CallOption) (*ApplicationSettings, error) {
	cfg := s.c.config(opts)
	if applicationID == "" {
		applicationID = s.c.application(cfg)
	}
	return one(ctx, s.c, spec{op: opPutApplicationSettings, pathValues: []string{applicationID}, body: params}, cfg, settingsMeta)
}

func settingsMeta(r *ApplicationSettings, m ResponseMeta) { r.ResponseMeta = m }

// --- reviews --------------------------------------------------------------------------------

// ReviewsService creates, reads, edits and moderates reviews.
type ReviewsService struct{ c *Client }

// ListReviewsParams filters Reviews.List. Without AuthorID or IncludeUnpublished only published
// reviews are returned; IncludeUnpublished needs the reviews:moderate scope.
type ListReviewsParams struct {
	EntityID              string
	EntityType            string
	ExternalID            string
	AuthorID              string
	IncludeUnpublished    *bool
	Status                string
	Rating                *int
	HasMedia              *bool
	IsVerifiedPurchase    *bool
	HasPendingRevision    *bool
	MediaModerationStatus string
	// Sort is "newest" (default), "oldest", "rating_desc" or "rating_asc".
	Sort string
	Page
}

// List returns one page of reviews.
func (s *ReviewsService) List(ctx context.Context, params ListReviewsParams, opts ...CallOption) (*List[Review], error) {
	q := query{}
	q.str("entity_id", params.EntityID)
	q.str("entity_type", params.EntityType)
	q.str("external_id", params.ExternalID)
	q.str("author_id", params.AuthorID)
	q.boolean("include_unpublished", params.IncludeUnpublished)
	q.str("status", params.Status)
	q.num("rating", params.Rating)
	q.boolean("has_media", params.HasMedia)
	q.boolean("is_verified_purchase", params.IsVerifiedPurchase)
	q.boolean("has_pending_revision", params.HasPendingRevision)
	q.str("media_moderation_status", params.MediaModerationStatus)
	q.str("sort", params.Sort)
	q.page(params.Page)
	return list[Review](ctx, s.c, spec{op: opListReviews, query: url.Values(q)}, s.c.config(opts))
}

// CreateReviewParams creates a review. Name the entity by EntityID, or by EntityType and
// ExternalID (created on first review). Derive AuthorID from your own authenticated user.
type CreateReviewParams struct {
	ApplicationID      string         `json:"application_id"`
	EntityID           string         `json:"entity_id,omitempty"`
	EntityType         string         `json:"entity_type,omitempty"`
	ExternalID         string         `json:"external_id,omitempty"`
	AuthorID           string         `json:"author_id"`
	Rating             int            `json:"rating"`
	Title              *string        `json:"title,omitempty"`
	Body               *string        `json:"body,omitempty"`
	Metadata           map[string]any `json:"metadata,omitzero"`
	AuthorMetadata     map[string]any `json:"author_metadata,omitzero"`
	SourceRef          string         `json:"source_ref,omitempty"`
	IsVerifiedPurchase *bool          `json:"is_verified_purchase,omitempty"`
}

// Create creates a review. Not retried: after a lost response a repeat may answer
// review_exists; read the review to learn the outcome.
func (s *ReviewsService) Create(ctx context.Context, params CreateReviewParams, opts ...CallOption) (*Review, error) {
	cfg := s.c.config(opts)
	if params.ApplicationID == "" {
		params.ApplicationID = s.c.application(cfg)
	}
	return one(ctx, s.c, spec{op: opCreateReview, body: params}, cfg, reviewMeta)
}

// GetReviewParams reads one review; AuthorID reads an unpublished review on the author path.
type GetReviewParams struct {
	AuthorID           string
	IncludeUnpublished *bool
}

// Get returns one review.
func (s *ReviewsService) Get(ctx context.Context, reviewID string, params GetReviewParams, opts ...CallOption) (*Review, error) {
	q := query{}
	q.str("author_id", params.AuthorID)
	q.boolean("include_unpublished", params.IncludeUnpublished)
	return one(ctx, s.c, spec{op: opGetReview, pathValues: []string{reviewID}, query: url.Values(q)}, s.c.config(opts), reviewMeta)
}

// UpdateReviewParams edits a review; unset fields stay as they are. On a published review in a
// pre-moderated application the edit becomes a pending revision.
type UpdateReviewParams struct {
	Rating             *int             `json:"rating,omitempty"`
	Title              Nullable[string] `json:"title,omitzero"`
	Body               Nullable[string] `json:"body,omitzero"`
	Metadata           map[string]any   `json:"metadata,omitzero"`
	AuthorMetadata     map[string]any   `json:"author_metadata,omitzero"`
	IsVerifiedPurchase *bool            `json:"is_verified_purchase,omitempty"`
}

// Update edits a review (not retried).
func (s *ReviewsService) Update(ctx context.Context, reviewID string, params UpdateReviewParams, opts ...CallOption) (*Review, error) {
	return one(ctx, s.c, spec{op: opUpdateReview, pathValues: []string{reviewID}, body: params}, s.c.config(opts), reviewMeta)
}

// Remove removes a review: a status change, never a hard delete (not retried).
func (s *ReviewsService) Remove(ctx context.Context, reviewID string, opts ...CallOption) (*Review, error) {
	return one(ctx, s.c, spec{op: opRemoveReview, pathValues: []string{reviewID}}, s.c.config(opts), reviewMeta)
}

// PublishParams carries the version guard: the Version and MediaVersion of the review the
// moderator saw. A changed review answers [IsReviewChanged].
type PublishParams struct {
	ExpectedVersion      int     `json:"expected_version"`
	ExpectedMediaVersion int     `json:"expected_media_version"`
	Reason               *string `json:"reason,omitempty"`
}

// Publish publishes a pending, rejected or hidden review (not retried).
func (s *ReviewsService) Publish(ctx context.Context, reviewID string, params PublishParams, opts ...CallOption) (*Review, error) {
	return one(ctx, s.c, spec{op: opPublishReview, pathValues: []string{reviewID}, body: params}, s.c.config(opts), reviewMeta)
}

// ModerationParams carries an optional reason shown to operators.
type ModerationParams struct {
	Reason *string `json:"reason,omitempty"`
}

// Reject rejects a pending review (not retried).
func (s *ReviewsService) Reject(ctx context.Context, reviewID string, params ModerationParams, opts ...CallOption) (*Review, error) {
	return one(ctx, s.c, spec{op: opRejectReview, pathValues: []string{reviewID}, body: params}, s.c.config(opts), reviewMeta)
}

// Hide hides a published review (not retried).
func (s *ReviewsService) Hide(ctx context.Context, reviewID string, params ModerationParams, opts ...CallOption) (*Review, error) {
	return one(ctx, s.c, spec{op: opHideReview, pathValues: []string{reviewID}, body: params}, s.c.config(opts), reviewMeta)
}

// GetRevision returns a review's pending revision.
func (s *ReviewsService) GetRevision(ctx context.Context, reviewID string, opts ...CallOption) (*Revision, error) {
	return one(ctx, s.c, spec{op: opGetReviewRevision, pathValues: []string{reviewID}}, s.c.config(opts),
		func(r *Revision, m ResponseMeta) { r.ResponseMeta = m })
}

// ApproveRevisionParams names the revision and carries the version guard.
type ApproveRevisionParams struct {
	RevisionID           string `json:"revision_id"`
	ExpectedVersion      int    `json:"expected_version"`
	ExpectedMediaVersion int    `json:"expected_media_version"`
}

// ApproveRevision applies a pending revision (not retried).
func (s *ReviewsService) ApproveRevision(ctx context.Context, reviewID string, params ApproveRevisionParams, opts ...CallOption) (*Review, error) {
	return one(ctx, s.c, spec{op: opApproveReviewRevision, pathValues: []string{reviewID}, body: params}, s.c.config(opts), reviewMeta)
}

// RejectRevisionParams names the revision to drop.
type RejectRevisionParams struct {
	RevisionID string `json:"revision_id"`
}

// RejectRevision drops a pending revision (not retried).
func (s *ReviewsService) RejectRevision(ctx context.Context, reviewID string, params RejectRevisionParams, opts ...CallOption) (*Review, error) {
	return one(ctx, s.c, spec{op: opRejectReviewRevision, pathValues: []string{reviewID}, body: params}, s.c.config(opts), reviewMeta)
}

func reviewMeta(r *Review, m ResponseMeta) { r.ResponseMeta = m }

// --- media ----------------------------------------------------------------------------------

// MediaService uploads and moderates review images and videos.
type MediaService struct{ c *Client }

// ListMediaParams reads a review's media on the author path or as a moderator.
type ListMediaParams struct {
	AuthorID           string
	IncludeUnpublished *bool
}

// List returns every media item of a review (not capped like the review's embedded list).
func (s *MediaService) List(ctx context.Context, reviewID string, params ListMediaParams, opts ...CallOption) ([]Media, error) {
	q := query{}
	q.str("author_id", params.AuthorID)
	q.boolean("include_unpublished", params.IncludeUnpublished)
	var media []Media
	if _, err := s.c.call(ctx, spec{op: opListReviewMedia, pathValues: []string{reviewID}, query: url.Values(q), out: &media}, s.c.config(opts)); err != nil {
		return nil, err
	}
	return media, nil
}

// CreateUploadParams declares one upload. ClientUploadID makes the call retry-safe; the SDK
// generates one when it is empty.
type CreateUploadParams struct {
	Kind            string   `json:"kind"`
	ContentType     string   `json:"content_type"`
	SizeBytes       int64    `json:"size_bytes"`
	ClientUploadID  string   `json:"client_upload_id,omitempty"`
	Position        *int     `json:"position,omitempty"`
	Width           *int     `json:"width,omitempty"`
	Height          *int     `json:"height,omitempty"`
	DurationSeconds *float64 `json:"duration_seconds,omitempty"`
}

// CreateUpload creates an upload intent and returns where to PUT the bytes. Browsers can take
// Upload.URL and Upload.Headers from here and PUT directly.
func (s *MediaService) CreateUpload(ctx context.Context, reviewID string, params CreateUploadParams, opts ...CallOption) (*MediaUpload, error) {
	if params.ClientUploadID == "" {
		id, err := newClientUploadID()
		if err != nil {
			return nil, err
		}
		params.ClientUploadID = id
	}
	return one(ctx, s.c, spec{op: opCreateMediaUpload, pathValues: []string{reviewID}, body: params}, s.c.config(opts),
		func(r *MediaUpload, m ResponseMeta) { r.ResponseMeta = m })
}

// Complete validates the uploaded bytes and publishes the media item (retry-safe).
func (s *MediaService) Complete(ctx context.Context, reviewID, mediaID string, opts ...CallOption) (*Media, error) {
	return one(ctx, s.c, spec{op: opCompleteMediaUpload, pathValues: []string{reviewID, mediaID}}, s.c.config(opts), mediaMeta)
}

// Delete deletes a media item (retry-safe).
func (s *MediaService) Delete(ctx context.Context, reviewID, mediaID string, opts ...CallOption) (*Media, error) {
	return one(ctx, s.c, spec{op: opDeleteMedia, pathValues: []string{reviewID, mediaID}}, s.c.config(opts), mediaMeta)
}

// ApproveMediaParams carries the version guard of the media item's review.
type ApproveMediaParams struct {
	ExpectedVersion      int     `json:"expected_version"`
	ExpectedMediaVersion int     `json:"expected_media_version"`
	Reason               *string `json:"reason,omitempty"`
}

// Approve approves a pending media item (not retried).
func (s *MediaService) Approve(ctx context.Context, reviewID, mediaID string, params ApproveMediaParams, opts ...CallOption) (*Media, error) {
	return one(ctx, s.c, spec{op: opApproveMedia, pathValues: []string{reviewID, mediaID}, body: params}, s.c.config(opts), mediaMeta)
}

// Reject rejects a media item (retry-safe).
func (s *MediaService) Reject(ctx context.Context, reviewID, mediaID string, params ModerationParams, opts ...CallOption) (*Media, error) {
	return one(ctx, s.c, spec{op: opRejectMedia, pathValues: []string{reviewID, mediaID}, body: params}, s.c.config(opts), mediaMeta)
}

func mediaMeta(r *Media, m ResponseMeta) { r.ResponseMeta = m }

func newClientUploadID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("octoreview: generate a client_upload_id: " + err.Error())
	}
	return "go-" + hex.EncodeToString(raw), nil
}

// --- privacy and audit ------------------------------------------------------------------------

// AuthorsService erases an author's data.
type AuthorsService struct{ c *Client }

// EraseAuthorParams names the author to erase within one application.
type EraseAuthorParams struct {
	ApplicationID string `json:"application_id"`
	AuthorID      string `json:"author_id"`
}

// Erase redacts one batch of the author's reviews and deletes their media; call again while
// Remaining is above 0 (retry-safe).
func (s *AuthorsService) Erase(ctx context.Context, params EraseAuthorParams, opts ...CallOption) (*EraseResult, error) {
	cfg := s.c.config(opts)
	if params.ApplicationID == "" {
		params.ApplicationID = s.c.application(cfg)
	}
	return one(ctx, s.c, spec{op: opEraseAuthor, body: params}, cfg,
		func(r *EraseResult, m ResponseMeta) { r.ResponseMeta = m })
}

// AuditLogsService reads the audit log.
type AuditLogsService struct{ c *Client }

// ListAuditLogsParams filters AuditLogs.List.
type ListAuditLogsParams struct {
	Action        string
	AggregateType string
	AggregateID   string
	ActorID       string
	Page
}

// List returns one page of audit log rows, newest first.
func (s *AuditLogsService) List(ctx context.Context, params ListAuditLogsParams, opts ...CallOption) (*List[AuditLog], error) {
	q := query{}
	q.str("action", params.Action)
	q.str("aggregate_type", params.AggregateType)
	q.str("aggregate_id", params.AggregateID)
	q.str("actor_id", params.ActorID)
	q.page(params.Page)
	return list[AuditLog](ctx, s.c, spec{op: opListAuditLogs, query: url.Values(q)}, s.c.config(opts))
}
