package octoreview

import (
	"errors"
	"net/http"
	"time"
)

// Option configures a Client.
type Option func(*Client) error

// WithApplication sets the default application id, sent wherever an operation takes
// application_id. A call overrides it with [ForApplication].
func WithApplication(applicationID string) Option {
	return func(c *Client) error {
		c.applicationID = applicationID
		return nil
	}
}

// WithNamespace sets the default namespace (X-Namespace-Type and X-Namespace-ID). A call
// overrides it with [InNamespace] or [InGlobalScope].
func WithNamespace(namespaceType, namespaceID string) Option {
	return func(c *Client) error {
		if namespaceType == "" || namespaceID == "" {
			return errors.New("octoreview: WithNamespace needs both a type and an id")
		}
		c.namespace = &namespace{namespaceType, namespaceID}
		return nil
	}
}

// WithActor sets the default X-Actor-ID recorded in audit logs for writes.
func WithActor(actorID string) Option {
	return func(c *Client) error {
		c.actorID = actorID
		return nil
	}
}

// WithHTTPClient replaces the HTTP client used for API calls (default: 30 s timeout).
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) error {
		if httpClient == nil {
			return errors.New("octoreview: WithHTTPClient got a nil client")
		}
		c.httpClient = httpClient
		return nil
	}
}

// WithRetry sets how many times a retry-safe call is attempted (default 3) and the longest
// Retry-After the client waits for (default 30 s); a longer one returns the error instead.
func WithRetry(maxAttempts int, maxRetryAfter time.Duration) Option {
	return func(c *Client) error {
		if maxAttempts < 1 {
			return errors.New("octoreview: WithRetry needs at least one attempt")
		}
		c.maxAttempts = maxAttempts
		c.maxRetryAfter = maxRetryAfter
		return nil
	}
}

// CallOption adjusts one call.
type CallOption func(*callConfig)

type callConfig struct {
	applicationID *string
	namespace     *namespace
	global        bool
	includeGlobal bool
	ifNoneMatch   string
	actorID       string
	requestID     string
}

// ForApplication overrides the client's default application for one call.
func ForApplication(applicationID string) CallOption {
	return func(cfg *callConfig) { cfg.applicationID = &applicationID }
}

// InNamespace overrides the client's default namespace for one call.
func InNamespace(namespaceType, namespaceID string) CallOption {
	return func(cfg *callConfig) {
		cfg.namespace = &namespace{namespaceType, namespaceID}
		cfg.global = false
	}
}

// InGlobalScope sends no namespace for one call, even when the client has a default one.
func InGlobalScope() CallOption {
	return func(cfg *callConfig) {
		cfg.namespace = nil
		cfg.global = true
	}
}

// IncludeGlobal also returns global (namespace-less) rows on a namespaced read.
func IncludeGlobal() CallOption {
	return func(cfg *callConfig) { cfg.includeGlobal = true }
}

// WithIfNoneMatch makes a read conditional: when the resource still has etag, the call returns
// [ErrNotModified].
func WithIfNoneMatch(etag string) CallOption {
	return func(cfg *callConfig) { cfg.ifNoneMatch = etag }
}

// AsActor sets X-Actor-ID for one write.
func AsActor(actorID string) CallOption {
	return func(cfg *callConfig) { cfg.actorID = actorID }
}

// WithRequestID sends X-Request-ID so the call can be found in the server's logs.
func WithRequestID(requestID string) CallOption {
	return func(cfg *callConfig) { cfg.requestID = requestID }
}

type namespace struct {
	Type string
	ID   string
}
