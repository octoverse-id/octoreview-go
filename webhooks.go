package octoreview

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Event types (the server's docs/events.md). The contract gate checks them against the vendored
// copy, with each payload type's fields.
const (
	EventReviewCreated           = "review.created"
	EventReviewUpdated           = "review.updated"
	EventReviewRevisionSubmitted = "review.revision_submitted"
	EventReviewStatusChanged     = "review.status_changed"
	EventReviewPublished         = "review.published"
	EventReviewRemoved           = "review.removed"
	EventMediaReady              = "review_media.ready"
	EventMediaInvalid            = "review_media.invalid"
	EventMediaDeleted            = "review_media.deleted"
	EventMediaModerated          = "review_media.moderated"
	EventEntityRatingChanged     = "entity.rating_changed"
	EventAuthorErased            = "author.erased"
)

var (
	reviewEvents = []string{EventReviewCreated, EventReviewUpdated, EventReviewRevisionSubmitted,
		EventReviewStatusChanged, EventReviewPublished, EventReviewRemoved}
	mediaEvents = []string{EventMediaReady, EventMediaInvalid, EventMediaDeleted, EventMediaModerated}
)

// SignatureHeader is the webhook header carrying `t=<unix seconds>,v1=<hex HMAC-SHA256>`.
const SignatureHeader = "X-Octoreview-Signature"

// DefaultWebhookTolerance is how far a signature's timestamp may be from the clock.
const DefaultWebhookTolerance = 300 * time.Second

// ErrInvalidSignature is a webhook whose signature header is missing, malformed, from the wrong
// secret, or outside the tolerance. Reject the delivery with a 4xx.
var ErrInvalidSignature = errors.New("octoreview: invalid webhook signature")

// Event is one delivered outbox event. Data is the type's thin payload: decode it with Review,
// Media, RatingChanged or AuthorErased. Deduplicate on ID (delivery is at least once).
type Event struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	OccurredAt    time.Time       `json:"occurred_at"`
	TenantID      string          `json:"tenant_id"`
	ApplicationID *string         `json:"application_id"`
	NamespaceType *string         `json:"namespace_type"`
	NamespaceID   *string         `json:"namespace_id"`
	ResourceType  string          `json:"resource_type"`
	ResourceID    string          `json:"resource_id"`
	RequestID     *string         `json:"request_id"`
	Data          json.RawMessage `json:"data"`
}

// ReviewEvent is the payload of every review.* event. Compare Version and MediaVersion with what
// you stored before applying it: events have no global order.
type ReviewEvent struct {
	ReviewID           string  `json:"review_id"`
	EntityID           string  `json:"entity_id"`
	EntityType         string  `json:"entity_type"`
	ExternalID         string  `json:"external_id"`
	Status             string  `json:"status"`
	PreviousStatus     *string `json:"previous_status"`
	Rating             int     `json:"rating"`
	IsVerifiedPurchase bool    `json:"is_verified_purchase"`
	Version            int     `json:"version"`
	MediaVersion       int     `json:"media_version"`
}

// MediaEvent is the payload of every review_media.* event; Reason is set on review_media.invalid.
type MediaEvent struct {
	MediaID          string `json:"media_id"`
	ReviewID         string `json:"review_id"`
	EntityID         string `json:"entity_id"`
	Kind             string `json:"kind"`
	Status           string `json:"status"`
	ModerationStatus string `json:"moderation_status"`
	Reason           string `json:"reason,omitempty"`
}

// RatingChangedEvent is entity.rating_changed: replace your stored summary with it.
type RatingChangedEvent struct {
	EntityID            string    `json:"entity_id"`
	EntityType          string    `json:"entity_type"`
	ExternalID          string    `json:"external_id"`
	RatingCount         int       `json:"rating_count"`
	RatingAverage       *float64  `json:"rating_average"`
	Histogram           Histogram `json:"histogram"`
	VerifiedReviewCount int       `json:"verified_review_count"`
}

// AuthorErasedEvent is author.erased: delete every copy you hold of this author's reviews and
// personal data, and drop older events about the author that arrive later.
type AuthorErasedEvent struct {
	AuthorID        string    `json:"author_id"`
	ErasedAt        time.Time `json:"erased_at"`
	ReviewsRedacted int       `json:"reviews_redacted"`
}

func (e *Event) decode(family []string, out any) error {
	if !has(family, e.Type) {
		return fmt.Errorf("octoreview: a %s event has no %T payload", e.Type, out)
	}
	if err := json.Unmarshal(e.Data, out); err != nil {
		return fmt.Errorf("octoreview: decode the %s payload: %w", e.Type, err)
	}
	return nil
}

// Review decodes a review.* payload.
func (e *Event) Review() (*ReviewEvent, error) {
	out := &ReviewEvent{}
	return out, e.decode(reviewEvents, out)
}

// Media decodes a review_media.* payload.
func (e *Event) Media() (*MediaEvent, error) {
	out := &MediaEvent{}
	return out, e.decode(mediaEvents, out)
}

// RatingChanged decodes an entity.rating_changed payload.
func (e *Event) RatingChanged() (*RatingChangedEvent, error) {
	out := &RatingChangedEvent{}
	return out, e.decode([]string{EventEntityRatingChanged}, out)
}

// AuthorErased decodes an author.erased payload.
func (e *Event) AuthorErased() (*AuthorErasedEvent, error) {
	out := &AuthorErasedEvent{}
	return out, e.decode([]string{EventAuthorErased}, out)
}

// VerifyOption adjusts webhook verification.
type VerifyOption func(*verifyConfig)

type verifyConfig struct {
	tolerance time.Duration
	now       func() time.Time
}

// WithTolerance replaces the default 300 s timestamp tolerance.
func WithTolerance(d time.Duration) VerifyOption {
	return func(cfg *verifyConfig) { cfg.tolerance = d }
}

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) VerifyOption {
	return func(cfg *verifyConfig) { cfg.now = now }
}

// VerifyWebhook checks a delivery's X-Octoreview-Signature against the raw request body and the
// webhook secret (constant time, 300 s tolerance), then decodes the event. Verify before you
// parse, and pass the body exactly as received.
func VerifyWebhook(body []byte, signatureHeader, secret string, opts ...VerifyOption) (*Event, error) {
	cfg := verifyConfig{tolerance: DefaultWebhookTolerance, now: time.Now}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if secret == "" {
		return nil, errors.New("octoreview: the webhook secret is empty")
	}
	var timestamp string
	var signatures []string
	for _, part := range strings.Split(signatureHeader, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		switch {
		case !ok:
			continue
		case key == "t":
			timestamp = value
		case key == "v1":
			signatures = append(signatures, value)
		}
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || len(signatures) == 0 {
		return nil, fmt.Errorf("%w: the header is not t=<seconds>,v1=<hex>", ErrInvalidSignature)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	expected := mac.Sum(nil)
	matched := false
	for _, candidate := range signatures {
		decoded, err := hex.DecodeString(candidate)
		if err == nil && hmac.Equal(decoded, expected) {
			matched = true
		}
	}
	if !matched {
		return nil, fmt.Errorf("%w: no v1 signature matches this secret and body", ErrInvalidSignature)
	}
	if skew := cfg.now().Sub(time.Unix(seconds, 0)); skew > cfg.tolerance || skew < -cfg.tolerance {
		return nil, fmt.Errorf("%w: the timestamp is %s from this clock (tolerance %s)", ErrInvalidSignature, skew.Round(time.Second), cfg.tolerance)
	}
	event := &Event{}
	if err := json.Unmarshal(body, event); err != nil {
		return nil, fmt.Errorf("octoreview: decode the webhook event: %w", err)
	}
	return event, nil
}

// WebhooksService verifies deliveries; it uses no client state, so VerifyWebhook works without a
// client too.
type WebhooksService struct{ c *Client }

// Verify is [VerifyWebhook].
func (s *WebhooksService) Verify(body []byte, signatureHeader, secret string, opts ...VerifyOption) (*Event, error) {
	return VerifyWebhook(body, signatureHeader, secret, opts...)
}
