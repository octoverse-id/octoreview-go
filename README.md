# Octoreview Go SDK

The official Go client for [Octoreview](https://github.com/octoverse-id/octoreview), the
multi-tenant review and rating service with image and video review media.

```sh
go get github.com/octoverse-id/octoreview-go
```

Dependency-free (`net/http` only), Go 1.24 or newer. No release yet: `v0.1.0` ships with the
server's `1.0.0-rc.1` ([`docs/versioning.md`](docs/versioning.md)).

## Usage

```go
client, err := octoreview.FromEnv() // OCTOREVIEW_BASE_URL, _API_KEY, _TENANT_ID, _APPLICATION_ID
if err != nil {
	log.Fatal(err)
}
ctx := context.Background()

review, err := client.Reviews.Create(ctx, octoreview.CreateReviewParams{
	EntityType: "product",
	ExternalID: "sku-1",
	AuthorID:   currentUser.ID, // from your own authentication, never from request input
	Rating:     5,
	Title:      octoreview.Ptr("Fits perfectly"),
})

summary, err := client.Ratings.Get(ctx, "product", "sku-1")         // one entity's stars
page, err := client.Ratings.Query(ctx, octoreview.QueryRatingsParams{ // a listing page's stars
	EntityType: "product", ExternalIDs: []string{"sku-1", "sku-2"},
})
```

`New(baseURL, apiKey, tenantID, ...Option)` builds a client explicitly. Client options set
defaults (`WithApplication`, `WithNamespace`, `WithActor`, `WithHTTPClient`, `WithRetry`), and
call options override them per call (`ForApplication`, `InNamespace`, `InGlobalScope`,
`IncludeGlobal`, `AsActor`, `WithIfNoneMatch`, `WithRequestID`). Every method takes a
`context.Context` first.

**Render safely.** Review titles and bodies are user input: escape them when you render them
(`html/template` does), for example `{{ .Review.Title }}` rather than building HTML by hand.

**Moderation.** `Reviews.Publish`, `Reviews.ApproveRevision` and `Media.Approve` take the review's
`Version` and `MediaVersion` as the moderator saw them. A review that changed since then answers
`octoreview.IsReviewChanged(err)`: reload it and show the new content.

## Errors

API errors are `*octoreview.APIError` (`AsAPIError`, `IsCode`, `IsNotFound`): HTTP status,
`Code` (a `Code...` constant), `Message` ending with the fix, `Details`, `RequestID`, `DocURL`,
`Retryable` and `RetryAfter`. Network failures are `*octoreview.TransportError`. Neither ever
contains the API key, a query string or a token path segment.

A read called with `WithIfNoneMatch(result.ETag)` returns `octoreview.ErrNotModified` while the
resource is unchanged.

## Retries

Each method's retry class comes from the server's `docs/sdk-surface.yaml`. Retries back off and
honor `Retry-After` up to `WithRetry`'s bound (default 3 attempts, 30 s).

<!-- retry-table:start -->
| Retry class | Behavior | Methods |
| --- | --- | --- |
| `safe` | a read: retried on timeouts, 503 and 429, honoring Retry-After | `AuditLogs.List`, `Entities.Get`, `Entities.List`, `Entities.Lookup`, `Health.Live`, `Health.Ready`, `Media.List`, `Ratings.ForEntity`, `Ratings.Query`, `Reviews.GetRevision`, `Reviews.Get`, `Reviews.List`, `Settings.Get` |
| `idempotent` | repeats to the same state: retried like a read | `Authors.Erase`, `Entities.Deactivate`, `Entities.Update`, `Entities.Upsert`, `Media.Complete`, `Media.Delete`, `Media.Reject`, `Settings.Put` |
| `keyed` | idempotent with its `ClientUploadID`, which the SDK fills in: retried like a read | `Media.CreateUpload` |
| `unsafe` | never retried; after a lost response, read the resource to learn the outcome | `Media.Approve`, `Reviews.ApproveRevision`, `Reviews.Create`, `Reviews.Hide`, `Reviews.Publish`, `Reviews.RejectRevision`, `Reviews.Reject`, `Reviews.Remove`, `Reviews.Update` |
<!-- retry-table:end -->

## Working on this repository

Read [`AGENTS.md`](AGENTS.md) first (humans too): it holds the rules, gates and development
pipeline. `make help` lists the targets; `make check` is the fast gate, and it includes the
offline contract gate against the vendored server contract
([`docs/architecture.md`](docs/architecture.md)).

## License

[Apache License 2.0](LICENSE).
