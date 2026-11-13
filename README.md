# netsec: tenant-safe inference batch ledger

`example.org/netsec/q0148` is an importable Go package. It implements a
canonical inference batch ledger, a frozen-row streaming decoder contract, and
a deterministic search for cross-tenant callback mapping bugs.

## Batch ledger

```go
ledger := netsec.NewLedger()

err := ledger.Submit(netsec.Request{
    TenantID:         "tenant-a",
    RequestID:        "req-1", // unique only inside tenant-a
    ModelGeneration:  7,
    InputTokens:      []uint32{11, 12},
    MaxOutputTokens:  32,
    Deadline:         time.Now().Add(time.Minute),
    CancelGeneration: 3,
    DecodeStrategy:   "greedy",
})

batch, err := ledger.NextBatch()
result := netsec.NewTensorResult(batch) // rows carry rowIndex + identity digest
for i := range result.Rows {
    result.Rows[i].Tokens = outputsByRow[i]
    result.Rows[i].Done = true
}
deliveries, err := ledger.ApplyResult(result)
```

Important properties:

- Requests are grouped only when `ModelGeneration` and `DecodeStrategy` match.
- Batch rows are sorted by arrival sequence, then `RequestID`; tenant ID and
  identity digest break ties deterministically.
- `RequestID` is namespaced by `TenantID`. Two tenants may both use `"req-1"`.
- `BatchID` is cryptographically random by default. Each row also gets an
  immutable `RowIndex`, `InputLength`, `MaxOutputTokens`, and `IdentityDigest`.
- Callback rows may be returned in any order, but `RowIndex` and
  `IdentityDigest` must match the canonical batch. The row count must match.
- `ApplyResult` validates every row before committing anything. Duplicate
  callback results are idempotent.
- `Cancel` only removes that row's future delivery eligibility. Other indices
  are not shifted or compacted.
- A result arriving after its row deadline is dropped. A model-generation
  switch invalidates the entire in-flight batch.

For token history, an incomplete row may only append tokens. A completed row is
frozen; any callback that changes its tokens or marks it incomplete again is
rejected as a whole.

## Streaming

```go
deliveries, err := ledger.Stream(batch.BatchID, func(step netsec.StreamStep) (netsec.StreamResult, error) {
    // step.Rows[i].RowIndex is stable for every step.
    // step.FrozenMask[i] is true for completed rows.
    result := netsec.NewStreamResult(step)
    // Fill result.Rows, preserving frozen rows exactly.
    return result, nil
})
```

Each step is committed atomically. `ApplyStreamResult` checks output limits,
identity digests, append-only progress, and frozen-row equality before any
row is delivered.

## Deterministic leak search

```go
result, err := netsec.SearchShortestLeak(netsec.LeakSearchConfig{
    Requests: requests,
    Mapping:  netsec.MappingByCompletionOrder,
    MaxDepth: 6,
})
if result.Found {
    // result.Trace.Actions is the shortest action sequence.
}
```

The search explores cancellation, completion, duplicate-result, and
model-generation-switch actions in breadth-first deterministic order.
`MarkerModel` encodes each request's tenant, request ID, generation, output
limit, and exact input sequence into output tokens, including empty inputs. A
found trace reports the tenant A destination row, tenant B source row, marker
token, and shortest action sequence.

## Verification

```sh
go test ./...
go test -race ./...
go vet ./...
```
