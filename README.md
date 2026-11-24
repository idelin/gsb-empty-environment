# readproof

`readproof` is an importable Go package for authenticating and planning
tenant-scoped reads from a content-addressed concatenation tree.

The package is designed to be called by a storage host before it releases
bytes. Planning and acceptance are separate: a peer may produce a certificate,
but the host must call `Accept` with its own signed root, current authorization
view, request range, and validated-byte set.

## Model

- A leaf is `{block, length}`.
- An internal node is an ordered list of child subintervals
  `{digest, offset, length}`. Child intervals may repeat the same subtree.
- `Node.Digest()` is SHA-256 over a canonical, unambiguous encoding. The
  encoding is documented on `Node.Canonical`.
- `Catalog` accepts nodes in any order and does not require children to be
  present when a parent is inserted.
- Nodes are loaded only when a requested path needs them. Shared subtrees are
  never expanded into logical bytes, so a root of length `2^50` is supported
  without per-byte allocation.

## Authorization

A `Credential` grants one tenant access to a half-open interval in root
coordinates for a specific root and generation. A credential is valid only if
it is in the current `AuthorizationView` and its generation is newer than the
latest revocation threshold for that credential ID.

Physical block identity never transfers authorization. Two logical positions
that reference the same block require independent root-coordinate coverage.
Every certificate segment is bound to one leaf path and one credential; a path
is never allowed to cross a shared child boundary.

`Validated` ranges are physical `{block, offset, length}` intervals supplied by
the trusted byte-validation layer. A certificate segment is accepted only when
its physical range is inside one of those intervals.

## Planning and acceptance

```go
import readproof "prescreen.local/ps-c8aecae2d7"

catalog := readproof.NewCatalog()
// Add leaves and internal nodes in any order.

checker := readproof.NewChecker(catalog, verifyRootSignature)
req := readproof.Request{
    Root: readproof.SignedRoot{Digest: root, Signature: signature},
    View: readproof.AuthorizationView{
        Tenant:      "tenant-a",
        Root:        root,
        Credentials: credentials,
        Revocations: revocations,
    },
    Range:       readproof.NewRange(start, end),
    Validated:   validatedPhysicalIntervals,
    MaxSegments: 64, // zero means no explicit limit
}

cert, err := checker.Issue(req)
if err != nil {
    // Inspect *readproof.GapError or *readproof.SegmentLimitError.
}
if err := checker.Accept(cert, req); err != nil {
    // Do not read.
}
```

`Issue` produces an exact-cover certificate. It minimizes segment count with a
farthest-endpoint greedy choice; ties use the smallest credential ID. A
candidate never crosses a leaf or shared-node boundary. If evidence is absent,
the returned `GapError` contains the first root-coordinate gap and a reason.

`Accept` re-verifies the root signature, all path identities/positions, the
current authorization and revocation snapshot, physical placement, validated
byte intervals, and contiguous exact coverage. Signature failures, cycles,
identity mismatches, length overflow, stale revoked credentials, missing
validated bytes, and segment overruns never produce a usable segment.

## Tests

The test suite includes:

- exhaustive cut enumeration on a small tree as an optimality oracle;
- the same physical block at two logical positions with different grants;
- non-aligned root and physical slices;
- revocation and late-arriving old credentials;
- partial manifests where only the requested branch is available;
- a `2^50`-byte tree of repeated subtrees with a one-byte tail read;
- signature, cycle, identity, nested length-overflow, tampering, and segment
  limit failures.

Run:

```sh
go test ./...
go test -race ./...
```
