# Caching Reverse Proxy

`package proxy` implements a Go reverse proxy with a bounded shared response
cache.

## Behavior

- **Bounded shared cache**: `NewCache(capacity, clock)` caps the number of
  stored entries; overflow evicts the least recently used variant (LRU).
- **Multiple Vary variants per resource**: each request URI maps to a
  resource index with one entry per Vary variant. Lookups compare the exact
  request header values captured at storage time.
- **Freshness and stale-while-revalidate**: `max-age`, `s-maxage`,
  `Expires`, `no-cache`, `must-revalidate`, `min-fresh`, `max-stale` and
  `stale-while-revalidate` are honored through an injectable `Clock`.
- **Background revalidation**: an expired entry inside its
  `stale-while-revalidate` window is served immediately while a single
  background refresh runs; it outlives the triggering client request.
- **Coalesced synchronous revalidation**: concurrent stale requests share
  one origin revalidation per entry. Client cancellation aborts the leader
  and waiters retry instead of hanging.
- **Atomic 304 index rebuild**: a `304 Not Modified` response updates the
  stored metadata. If it changes `Vary` or switches the entry to
  `no-store`, the resource index is rebuilt (or removed) under the cache
  lock, so readers never observe a partial index.
- **Authorization isolation**: responses to authorized requests are cached
  only with explicit shared-cache permission (`public`, `must-revalidate`,
  `s-maxage`, or `Expires`). They are keyed by a hash of the credential and
  can never be served to an anonymous request or another credential.

## Acceptance tests

Tests use an in-process origin handler (`loopbackTransport`, no sockets) and
`FakeClock` for deterministic time travel:

- `TestMultipleVaryVariants` — multiple Vary variants per URI.
- `TestStaleWhileRevalidate` / `TestBackgroundRevalidationSurvivesClient` —
  stale serving and background refresh.
- `TestConcurrentRevalidationCoalesces` — many concurrent stale requests
  produce exactly one origin revalidation.
- `TestRevalidationCancellation` — canceled leader unblocks waiters, which
  retry and succeed.
- `Test304ChangesVaryRebuildsIndex` / `Test304NoStoreDropsEntry` — atomic
  index rebuild on 304 metadata changes.
- `TestAuthorizationIsolation` — no cross-credential or anonymous reuse.
- `TestBoundedLRUEviction` — capacity bound and LRU order.

Run:

```sh
go test ./... -race -count=1
```
