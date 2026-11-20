# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- The client core: `New` and `FromEnv`, client defaults with per-call overrides (application,
  namespace, explicit global scope, `IncludeGlobal`), `User-Agent: octoreview-go/<version>`, and
  every REST operation of the server's `docs/sdk-surface.yaml` as
  `Client.<Resource>.<Method>(ctx, params)`, plus the `Ratings.Get` helper. Retries follow each
  operation's retry class with a bounded `Retry-After`; `Media.CreateUpload` generates its
  `ClientUploadID`. Reads expose `ETag` and accept `WithIfNoneMatch` (`ErrNotModified`). Errors
  are typed (`*APIError` with the 39 code constants, `*TransportError`) and never carry the API
  key, a query string or a token path segment.
- The offline contract gate against the vendored server contract (`make contract`,
  `make vendor-contract`), and real `lint`, `test (oldstable)`, `test (stable)`, `contract` and
  `vuln` CI jobs.
- Repository bootstrap: Apache-2.0 license, governance files (AGENTS.md, CONTRIBUTING,
  SECURITY, Code of Conduct), issue forms, PR template, Dependabot with auto-merge for minor and
  patch updates, `llms.txt` with a link check, rulesets mirror and export script, and a CI
  skeleton whose required job names are final (placeholder jobs are replaced as code lands).
