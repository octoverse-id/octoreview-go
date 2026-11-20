# Architecture

How the Go SDK is built. The server's `docs/spec.md` section 18 is the contract for every SDK;
this page records how this one meets it.

## Layout

| File | Owns |
| --- | --- |
| `client.go`, `options.go` | `New`, `FromEnv`, client options (defaults) and call options (per-call overrides) |
| `operations.go` | One table of every REST operation: id, method, path, retry class, surface name and the parameters it can send |
| `transport.go` | Building requests (headers, path escaping, application and namespace defaults), retries, the data envelope, error parsing and redaction |
| `resources.go` | `Client.<Resource>.<Method>(ctx, params, ...CallOption)` for every operation, plus the `Ratings.Get` helper |
| `types.go`, `nullable.go` | Response models and `Nullable[T]` for PATCH fields where null clears an override |
| `errors.go` | `*APIError`, the 39 code constants, `TransportError`, `ErrNotModified` |

Dependency-free: only the standard library, so the module adds nothing to a consumer's graph.

## The contract gate

`make check` runs the gate offline (`make contract` runs it alone) against
`testdata/contract/`: the server's `docs/openapi.yaml` and `docs/sdk-surface.yaml`, vendored as
JSON by `make vendor-contract` (Go's standard library reads JSON, not YAML). `SOURCE` names the
server commit. The gate checks:

- every operation in `operations.go` exists in the contract with the same method and path, and
  every surface operation exists here (both directions);
- each operation's Go method name, retry class and parameter set equal the surface's, and every
  parameter it sends is declared by the contract;
- every surface method and helper exists on the client (reflection);
- the error code constants equal the contract's `ErrorCode` enum;
- a recording transport calls every operation with self-describing values (`review_id-13`,
  `entity_type-3`): method, path, credentials, declared headers only, and each value under its
  own name, so a swapped field fails;
- the README's retry table matches the operation table.

- every event payload type (`webhooks.go`) carries the fields the vendored `events.md` documents
  for its event types, and no undocumented ones;
- every response fixture in `testdata/fixtures`, captured from the real server by
  `make fixtures`, decodes with non-default values.

`.github/workflows/contract-drift.yml` runs weekly. It vendors the newest server contract (the
newest `-rc.N` tag until a final release exists), runs the gate against it and the integration
suite against `:edge`, and opens an issue when either fails.

## Uploads (`upload.go`)

`UploadFile`, `UploadBytes` and `UploadStream` compose `CreateUpload`, a PUT and `Complete`.

- **The PUT** uses its own `*http.Client` (`WithUploadHTTPClient`), which refuses redirects and
  has no global timeout (the context cancels). It sends only the signed headers plus an explicit
  `Content-Length`, so credentials never reach a storage provider.
- **Retries:** the PUT retries up to 3 times on the same URL while it is valid, rewinding the
  stream each time. `Complete` retries its documented conflicts through the transport.
- **Failures:** a terminal PUT failure deletes the pending media, and the storage provider's
  XML `<Code>` (or the local backend's JSON code) becomes `UploadError.ProviderCode` with a fix.

## The integration suite

`make integration` boots the pinned server image as one container: SQLite, the local backend,
and the demo seed through the server's compose start script. It copies out
`.octoreview-dev.env`, runs `examples/quickstart`, then runs `integration_test.go` (build tag
`integration`) over the main flows:
- entity media limits, two uploads and the third refused;
- ratings by key and in batch;
- pre-moderation, the version guard and revisions;
- media moderation, erasure, audit logs and health.

`make fixtures` also saves one response per operation into `testdata/fixtures`.

## Models are written by hand

Spec section 18 allows at most one day spent generating models from the OpenAPI document. The
decision is to keep them hand-written. The surface is 31 operations over 12 models; a generator
would add a build-time dependency and produce types with weaker names (`ReviewResponseData`) and
no `Nullable` tri-state for PATCH fields. The contract gate, not generation, keeps the types
honest: it fails on any drift in operations, parameters or error codes.
