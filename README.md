# Transactional HTTP/2 header decoding for tenant-safe gateways

`netsec` is an embeddable Go header-block layer for a reverse proxy. It
implements HPACK decoding, HEADERS/CONTINUATION assembly, connection and stream
ordering checks, and per-tenant compression isolation.

The attack this package prevents is dynamic-table poisoning across a canceled
stream: a peer sends an incrementally indexed `Authorization` in a HEADERS
fragment, cancels the stream with `RST_STREAM`, and later causes another
tenant's request to reference that entry. Here, HPACK changes are decoded into
a private table clone and are committed only after the complete block has
passed every check. Cancellation, budget failure, semantic rejection and
compression errors discard the clone.

## Invariants

- HEADERS is followed only by same-stream CONTINUATION frames until
  END_HEADERS. Any other interleaving is a connection error, except RST_STREAM
  which cancels the block.
- `MaxCompressedBlockBytes`, `MaxDecompressedBytes`, `MaxHeaderListSize`,
  `MaxHeaderCount` and `MaxStringLength` are enforced before publication.
- Pseudo-headers must precede regular headers, must be known and unique.
  Field names must be lowercase; configured sensitive names cannot use
  incremental indexing.
- A compression context belongs to one `(peer connection, tenant)` pair.
  Switching tenants resets it only after a synchronized SETTINGS generation
  for the target tenant is present.
- A late CONTINUATION is consumed as part of the canceled sequence but cannot
  attach to a new stream. Opening a new stream before the canceled sequence
  reaches END_HEADERS is a connection error.

## Usage

```go
decoder := netsec.NewDecoder(netsec.Config{
    MaxDynamicTableSize:     4096,
    MaxCompressedBlockBytes: 64 << 10,
    MaxDecompressedBytes:    128 << 10,
})

// A gateway binds the peer/tenant context at a stream boundary.
_ = decoder.BindTenant("conn-42", "tenant-a", settingsGeneration)

for {
    frame, err := netsec.ReadFrame(conn, 0)
    // ...
    result, err := decoder.ProcessFrame(frame)
    if err != nil {
        var streamErr *netsec.StreamError
        if errors.As(err, &streamErr) {
            // Reset one stream; the connection can continue.
        }
        var connErr *netsec.ConnectionError
        if errors.As(err, &connErr) {
            // The HPACK context is unusable; close the connection.
        }
        continue
    }
    if result.Fields != nil {
        // Complete, validated header list.
    }
}
```

`Decoder.Commits()` exposes `TableCommit` records. Each record contains the
compressed digest, before/after sizes, evictions, and an ordered operation
list. `TableCommit.Replay` reproduces the committed dynamic table on a
`DynamicTable` initialized to `InitialMaxSize`.

`ContextPool` is a convenience layer for connection-pool reuse: it keeps one
decoder per peer connection and requires a synchronized `SettingsSync` record
when the same underlying socket changes tenants.

## Tests

`go test ./...` covers:

- a canceled fragmented block that indexes `Authorization`, proving a later
  request still resolves an older safe entry;
- legal indexed references split across HEADERS/CONTINUATION boundaries;
- dynamic table size updates and their rollback;
- an oversized Huffman string that rolls back earlier fields in the block;
- pseudo-headers after regular headers, duplicate pseudo-headers and uppercase
  names;
- two tenants sharing one peer socket, including the SETTINGS gate and reset;
- connection ordering, orphan/late CONTINUATION, replayable commit records and
  RFC 7541 Huffman/request vectors.
