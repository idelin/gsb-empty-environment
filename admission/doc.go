// Package admission implements write admission for a Bazel-style remote
// build cache (REAPI ActionCache, ContentAddressableStorage and ByteStream).
// It is designed to be embedded as the gRPC front-end of an existing cache
// service without taking a dependency on grpc-go or the REAPI generated
// packages: the embedding layer authenticates callers, converts inbound
// protobuf messages into the domain types in this package, and calls
// Guard.Admit* or Front.Intercept before forwarding accepted calls to the
// cache backend.
//
// # Security policy
//
// The guard enforces four invariants. Every rejection emits a structured
// Logger.Event so poisoning attempts are auditable.
//
//   - ActionCache writes require a trusted Identity. Pull-request builds
//     (and any unauthenticated call) are read-only and receive
//     PermissionDenied before any output is inspected.
//   - CAS and ByteStream uploads are accepted from any writer, but only
//     under the digest the bytes actually have: sha256 is recomputed and
//     compared with the 64-lowercase-hex hash and size carried in the
//     resource name. Forged digests and size mismatches never reach the
//     store.
//   - An ActionResult is admitted only when every referenced output file,
//     output-directory tree, stdout and stderr digest already exists in CAS
//     with a matching stored size (FailedPrecondition otherwise).
//   - Storing a different ActionResult under an existing action digest is
//     rejected as AlreadyExists; an identical re-upload is idempotent.
//
// # Embedding
//
// Implement Store against the real cache backend (the in-memory
// MemoryStore is provided for tests and reference behavior). Derive an
// Identity from authenticated request metadata and propagate it with
// WithIdentity. In a unary interceptor, dispatch on the full gRPC method
// name via Front.Intercept; for streaming ByteStream.Write, create a
// BlobWriter with Guard.NewBlobWriter, feed each chunk to Write and call
// Commit on WriteCompleted.
package admission
