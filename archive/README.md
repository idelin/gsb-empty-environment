# archive

Importable Go package for the read side of the compressed, read-only archive
service.

```go
import "prescreen.local/ps-920c63cc2f/archive"
```

## Core pieces

- `Manifest`, `Node`, `Block`: raw blocks, dictionary slices, ordinary slices,
  reverse slices, repeated subtrees and concatenation. `Seal`/`Validate`
  enforce the 2^48 expanded-length limit, the content root, the compression
  strategy digest, the version and the manifest signature.
- `Grant`, `Interval`: authorization is expressed only in expanded root
  coordinates. Two references to the same physical block do not share a grant.
- `Resolve`, `Piece`: compact source segments for a root interval. Repeated
  subtrees stay symbolic (`Count`) and are never expanded byte by byte.
- `PlanBatch`, `Plan`: minimum-decode-cost planning under cache capacity,
  optional cache checkpoints and separate decode/returned-byte quotas.
  `ErrNoFeasiblePlan` and `ErrInsufficientEvidence` are distinct.
- `CheckPlan`, `Audit`: compact re-verification of every source segment,
  authorization, cache eviction, the decoded-page account and the
  root-coordinate-deduplicated returned-byte account.
- `Execute`, `PageCache`, `ReadBatch`: whole-batch authorization happens before
  the first decoder callback; output is returned in request order even when
  physical work was reordered. Execution is transactional, so a cancelled
  callback can be retried without committing a partial cache or resource
  account.

## Tests

`go test ./...` includes the small-graph full state-space comparison and the
adversarial scenarios: shared physical blocks at different root positions,
reverse-slice boundaries, half-covered grants, old-version/old-strategy cache
checkpoints, dense page extraction (no adjacent-byte leaks), and
cancel-then-retry.
