package octoreview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client calls the Octoreview REST API (/api/v1). It is safe for concurrent use.
type Client struct {
	baseURL       *url.URL
	apiKey        string
	tenantID      string
	applicationID string
	namespace     *namespace
	actorID       string
	httpClient    *http.Client
	maxAttempts   int
	maxRetryAfter time.Duration
	userAgent     string
	sleep         func(context.Context, time.Duration) error

	Health    *HealthService
	Entities  *EntitiesService
	Ratings   *RatingsService
	Settings  *SettingsService
	Reviews   *ReviewsService
	Media     *MediaService
	Authors   *AuthorsService
	AuditLogs *AuditLogsService
}

// New returns a client for the server at baseURL (for example https://reviews.example.com),
// authenticating with a service key for one tenant.
func New(baseURL, apiKey, tenantID string, opts ...Option) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("octoreview: base URL %q must be an absolute http(s) URL", baseURL)
	}
	if apiKey == "" {
		return nil, errors.New("octoreview: the API key is empty; pass a service key (ocr_...)")
	}
	if tenantID == "" {
		return nil, errors.New("octoreview: the tenant id is empty")
	}
	c := &Client{
		baseURL:       parsed,
		apiKey:        apiKey,
		tenantID:      tenantID,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		maxAttempts:   3,
		maxRetryAfter: 30 * time.Second,
		userAgent:     "octoreview-go/" + Version,
		sleep:         sleepContext,
	}
	for i, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("octoreview: option %d is nil", i)
		}
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	c.Health = &HealthService{c}
	c.Entities = &EntitiesService{c}
	c.Ratings = &RatingsService{c}
	c.Settings = &SettingsService{c}
	c.Reviews = &ReviewsService{c}
	c.Media = &MediaService{c}
	c.Authors = &AuthorsService{c}
	c.AuditLogs = &AuditLogsService{c}
	return c, nil
}

// FromEnv builds a client from OCTOREVIEW_BASE_URL, OCTOREVIEW_API_KEY, OCTOREVIEW_TENANT_ID and
// the optional OCTOREVIEW_APPLICATION_ID (the compose stack's `make dev-env` writes all four).
// Options passed here apply after the environment.
func FromEnv(opts ...Option) (*Client, error) {
	var missing []string
	value := func(name string) string {
		v := os.Getenv(name)
		if v == "" {
			missing = append(missing, name)
		}
		return v
	}
	baseURL, apiKey, tenantID := value("OCTOREVIEW_BASE_URL"), value("OCTOREVIEW_API_KEY"), value("OCTOREVIEW_TENANT_ID")
	if len(missing) > 0 {
		return nil, fmt.Errorf("octoreview: set %s (a .octoreview-dev.env from `make dev-env` has them)", strings.Join(missing, ", "))
	}
	if app := os.Getenv("OCTOREVIEW_APPLICATION_ID"); app != "" {
		opts = append([]Option{WithApplication(app)}, opts...)
	}
	return New(baseURL, apiKey, tenantID, opts...)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
