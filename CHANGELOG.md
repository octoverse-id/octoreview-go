# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- `APIError.Details` is redacted like the message: every string, recursively (a free-form
  `details.reason` could carry a signed URL into logs).
- A 304 returns `*NotModifiedError` with the response's `RequestID`; `errors.Is(err,
  ErrNotModified)` still holds.
- `TransportError.Err` is never serialized (`json:"-"`): the HTTP client's `*url.Error` holds the
  signed upload URL, which JSON-logging an `UploadError` exposed.

### Added

- The client core: `New` and `FromEnv`, client defaults with per-call overrides (application,
  namespace, explicit global scope, `IncludeGlobal`), `User-Agent: octoreview-go/<version>`, and
  every REST operation of the server's `docs/sdk-surface.yaml` as
  `Client.<Resource>.<Method>(ctx, params)`, plus the `Ratings.Get` helper. Retries follow each
  operation's retry class with a bounded `Retry-After`; `Media.CreateUpload` generates its
  `ClientUploadID`. Reads expose `ETag` and accept `WithIfNoneMatch` (`ErrNotModified`). Errors
  are typed (`*APIError` with the 39 code constants, `*TransportError`) and never carry the API
  key, a query string or a token path segment.
- The contract gate checks the type each method decodes into against the schema the contract
  names for that operation's response, both directions (server spec section 18).
- Uploads: `Media.UploadFile`, `UploadBytes` and `UploadStream` (intent, PUT, complete). The PUT
  uses a separate redirect-refusing client with only the signed headers and an explicit
  `Content-Length`, retries on the same URL until it expires, and deletes the pending media on a
  terminal failure. `*UploadError` reports the stage, ids, request ids and the provider's code
  with a fix, never the signed URL. Progress, an injectable upload client and context
  cancellation are supported.
- Webhooks: `VerifyWebhook` / `client.Webhooks.Verify` (constant time, 300 s tolerance) and typed
  event decoding (`Review`, `Media`, `RatingChanged`, `AuthorErased`), checked against the
  vendored `events.md`.
- `examples/quickstart`, the integration suite (`make integration`, build tag `integration`)
  against the pinned server image, captured response fixtures (`make fixtures`) decoded
  offline, the real `integration` CI job, and the weekly `contract-drift` workflow.
- The offline contract gate against the vendored server contract (`make contract`,
  `make vendor-contract`), and real `lint`, `test (oldstable)`, `test (stable)`, `contract` and
  `vuln` CI jobs.
- Repository bootstrap: Apache-2.0 license, governance files (AGENTS.md, CONTRIBUTING,
  SECURITY, Code of Conduct), issue forms, PR template, Dependabot with auto-merge for minor and
  patch updates, `llms.txt` with a link check, rulesets mirror and export script, and a CI
  skeleton whose required job names are final (placeholder jobs are replaced as code lands).
