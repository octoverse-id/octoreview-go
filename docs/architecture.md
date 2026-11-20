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

Decoding response fixtures captured from the container harness, and the weekly
`contract-drift.yml` run against the latest server release, arrive with the integration suite.

## Models are written by hand

Spec section 18 allows at most one day spent generating models from the OpenAPI document. The
decision is to keep them hand-written. The surface is 31 operations over 12 models; a generator
would add a build-time dependency and produce types with weaker names (`ReviewResponseData`) and
no `Nullable` tri-state for PATCH fields. The contract gate, not generation, keeps the types
honest: it fails on any drift in operations, parameters or error codes.
