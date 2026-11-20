package octoreview

import "time"

// Review statuses (docs/spec.md section 4).
const (
	StatusPending   = "pending"
	StatusPublished = "published"
	StatusRejected  = "rejected"
	StatusHidden    = "hidden"
	StatusRemoved   = "removed"
)

// Media kinds and statuses.
const (
	KindImage = "image"
	KindVideo = "video"

	MediaPending  = "pending"
	MediaReady    = "ready"
	MediaInvalid  = "invalid"
	MediaDeleting = "deleting"
	MediaDeleted  = "deleted"
)

// Moderation modes of an application.
const (
	ModerationAutoPublish   = "auto_publish"
	ModerationPreModeration = "pre_moderation"
)

// Pagination is a list's limit/offset window and the total count.
type Pagination struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Count  int `json:"count"`
}

// List is one page of a list operation.
type List[T any] struct {
	ResponseMeta
	Data       []T
	Pagination Pagination
}

// HealthLive answers while the process serves.
type HealthLive struct {
	ResponseMeta
	Status string `json:"status"`
}

// HealthReady also reports the database: Status "unavailable" when it is down.
type HealthReady struct {
	ResponseMeta
	Status   string `json:"status"`
	Database string `json:"database"`
}

// Histogram counts published reviews per star, "1" to "5".
type Histogram map[string]int

// HistogramPercent is the share of each star, one decimal each.
type HistogramPercent map[string]float64

// RatingSummary aggregates an entity's published reviews.
type RatingSummary struct {
	RatingCount int `json:"rating_count"`
	// RatingAverage has two decimals; nil when RatingCount is 0.
	RatingAverage       *float64         `json:"rating_average"`
	Histogram           Histogram        `json:"histogram"`
	HistogramPercent    HistogramPercent `json:"histogram_percent"`
	VerifiedReviewCount int              `json:"verified_review_count"`
}

// EntityRating is an entity's rating summary.
type EntityRating struct {
	ResponseMeta
	RatingSummary
	EntityID string `json:"entity_id"`
}

// BatchRating is one external id's summary in a ratings query; EntityID is nil when the entity
// does not exist yet (all zeros).
type BatchRating struct {
	RatingSummary
	ExternalID    string  `json:"external_id"`
	EntityID      *string `json:"entity_id"`
	NamespaceType *string `json:"namespace_type"`
	NamespaceID   *string `json:"namespace_id"`
}

// MediaPolicy is an entity's media overrides; nil inherits the application, then the server.
type MediaPolicy struct {
	MaxImages     *int64 `json:"max_images"`
	MaxVideos     *int64 `json:"max_videos"`
	MaxImageBytes *int64 `json:"max_image_bytes"`
	MaxVideoBytes *int64 `json:"max_video_bytes"`
}

// Entity is an external resource that can be reviewed, such as product/sku-1.
type Entity struct {
	ResponseMeta
	ID            string         `json:"id"`
	TenantID      string         `json:"tenant_id"`
	ApplicationID string         `json:"application_id"`
	NamespaceType *string        `json:"namespace_type"`
	NamespaceID   *string        `json:"namespace_id"`
	EntityType    string         `json:"entity_type"`
	ExternalID    string         `json:"external_id"`
	Metadata      map[string]any `json:"metadata"`
	IsActive      bool           `json:"is_active"`
	MediaPolicy   MediaPolicy    `json:"media_policy"`
	Rating        RatingSummary  `json:"rating"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// ApplicationSettings are an application's media policy defaults, moderation mode and quota.
type ApplicationSettings struct {
	ResponseMeta
	TenantID            string   `json:"tenant_id"`
	ApplicationID       string   `json:"application_id"`
	MaxImages           *int64   `json:"max_images"`
	MaxVideos           *int64   `json:"max_videos"`
	MaxImageBytes       *int64   `json:"max_image_bytes"`
	MaxVideoBytes       *int64   `json:"max_video_bytes"`
	AllowedContentTypes []string `json:"allowed_content_types"`
	ModerationMode      *string  `json:"moderation_mode"`
	MaxStorageBytes     *int64   `json:"max_storage_bytes"`
	StoredBytes         int64    `json:"stored_bytes"`
	ReservedBytes       int64    `json:"reserved_bytes"`
}

// Media is one image or video of a review. URL is nil when the caller may not see it, or when
// signing failed (URLUnavailable "signing").
type Media struct {
	ResponseMeta
	ID               string     `json:"id"`
	ReviewID         string     `json:"review_id"`
	Kind             string     `json:"kind"`
	Status           string     `json:"status"`
	ModerationStatus string     `json:"moderation_status"`
	ContentType      string     `json:"content_type"`
	SizeBytes        int64      `json:"size_bytes"`
	Width            *int       `json:"width"`
	Height           *int       `json:"height"`
	DurationSeconds  *float64   `json:"duration_seconds"`
	Position         int        `json:"position"`
	UploadExpiresAt  time.Time  `json:"upload_expires_at"`
	CreatedAt        time.Time  `json:"created_at"`
	URL              *string    `json:"url"`
	URLExpiresAt     *time.Time `json:"url_expires_at,omitempty"`
	URLUnavailable   string     `json:"url_unavailable,omitempty"`
}

// Review is one review of an entity.
type Review struct {
	ResponseMeta
	ID                 string         `json:"id"`
	TenantID           string         `json:"tenant_id"`
	ApplicationID      string         `json:"application_id"`
	NamespaceType      *string        `json:"namespace_type"`
	NamespaceID        *string        `json:"namespace_id"`
	EntityID           string         `json:"entity_id"`
	EntityType         string         `json:"entity_type"`
	ExternalID         string         `json:"external_id"`
	AuthorID           string         `json:"author_id"`
	AuthorMetadata     map[string]any `json:"author_metadata"`
	Rating             int            `json:"rating"`
	Title              *string        `json:"title"`
	Body               *string        `json:"body"`
	Status             string         `json:"status"`
	StatusReason       *string        `json:"status_reason"`
	Metadata           map[string]any `json:"metadata"`
	SourceRef          *string        `json:"source_ref"`
	IsVerifiedPurchase bool           `json:"is_verified_purchase"`
	// Version and MediaVersion feed the moderation version guard (Publish, ApproveRevision,
	// Media.Approve): pass the values the moderator saw.
	Version        int        `json:"version"`
	MediaVersion   int        `json:"media_version"`
	Media          []Media    `json:"media"`
	MediaTruncated bool       `json:"media_truncated"`
	PublishedAt    *time.Time `json:"published_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Revision is an edit to a published review waiting for moderation.
type Revision struct {
	ResponseMeta
	ID          string    `json:"id"`
	ReviewID    string    `json:"review_id"`
	Rating      int       `json:"rating"`
	Title       *string   `json:"title"`
	Body        *string   `json:"body"`
	BaseVersion int       `json:"base_version"`
	CreatedAt   time.Time `json:"created_at"`
}

// UploadTarget is where to PUT a media file: send exactly Headers plus an explicit
// Content-Length, never the service key.
type UploadTarget struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// MediaUpload is an upload intent's answer. Upload is nil when a replayed intent is no longer
// uploadable.
type MediaUpload struct {
	ResponseMeta
	Media  Media         `json:"media"`
	Upload *UploadTarget `json:"upload"`
}

// EraseResult reports one erasure batch; call Erase again while Remaining is above 0.
type EraseResult struct {
	ResponseMeta
	ReviewsRedacted     int `json:"reviews_redacted"`
	PendingMediaDeletes int `json:"pending_media_deletes"`
	Remaining           int `json:"remaining"`
}

// AuditLog is one recorded change.
type AuditLog struct {
	ID            string         `json:"id"`
	TenantID      string         `json:"tenant_id"`
	ApplicationID *string        `json:"application_id"`
	NamespaceType *string        `json:"namespace_type"`
	NamespaceID   *string        `json:"namespace_id"`
	Action        string         `json:"action"`
	AggregateType string         `json:"aggregate_type"`
	AggregateID   string         `json:"aggregate_id"`
	ActorID       *string        `json:"actor_id"`
	RequestID     *string        `json:"request_id"`
	OperationID   string         `json:"operation_id"`
	Changes       map[string]any `json:"changes"`
	Metadata      map[string]any `json:"metadata"`
	CreatedAt     time.Time      `json:"created_at"`
}

// Ptr returns a pointer to v, for optional fields such as CreateReviewParams.Title.
func Ptr[T any](v T) *T { return &v }
