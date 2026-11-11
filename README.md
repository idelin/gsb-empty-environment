# llhls

Low-Latency HLS media-playlist generator for a Go origin server. The
component maintains segments and partial segments, renders an LL-HLS playlist,
and serves Blocking Playlist Reload plus Skip Delivery directly as an
`http.Handler`. All time-dependent behavior is driven through a `Clock`, so a
virtual clock can deterministically exercise encoder output and concurrent
blocking requests in tests.

## Features

- `#EXT-X-PART-INF`, `#EXT-X-PART`, and `#EXT-X-PRELOAD-HINT` for the
  in-progress segment, with `INDEPENDENT=YES` on the first part of a segment.
- `#EXT-X-SERVER-CONTROL` advertising `CAN-BLOCK-RELOAD`, `PART-HOLD-BACK`,
  and `CAN-SKIP-UNTIL`.
- Delta updates via `_HLS_skip=YES`: old segments inside the skip window are
  replaced by a single `#EXT-X-SKIP:SKIPPED-SEGMENTS=n` tag while
  `#EXT-X-MEDIA-SEQUENCE` and the live-edge preload hint stay unchanged.
- Blocking reload with `_HLS_msn` / `_HLS_part`. The response is held until
  the requested part or full segment appears, the timeout elapses, or the
  request is canceled.
- Spec error handling: a request more than one Media Sequence Number past the
  playlist returns `400 Bad Request`; an already aged-out MSN returns the
  current playlist immediately.
- Monotonic sequence numbers. Pruning old segments never reuses or rolls back
  an MSN, and `#EXT-X-MEDIA-SEQUENCE` always tracks the oldest retained
  segment.

## Layout

- `clock.go` — `Clock`/`Timer` interfaces, a wall-clock implementation, and a
  `VirtualClock` whose timers fire synchronously inside `Advance`.
- `playlist.go` — segment/part state, configuration and validation, pruning,
  and full/delta playlist rendering.
- `handler.go` — `http.Handler` implementation of blocking reload and skip
  delivery.
- `encoder.go` — optional clock-driven helper that publishes parts on a fixed
  cadence, modeling a live encoder.
- `*_test.go` — model, clock, handler, concurrency, and encoder tests. The
  handler tests call `ServeHTTP` in-process with `httptest.ResponseRecorder`,
  so they need no open network port.

## Usage

```go
pl, err := llhls.New(llhls.Config{
    TargetDuration: 6 * time.Second,
    PartTarget:     time.Second,
    KeepSegments:   6,
})
if err != nil {
    log.Fatal(err)
}

// Encoder side: publish each partial segment as it is produced.
go func() {
    ticker := time.NewTicker(time.Second)
    for range ticker.C {
        pl.PublishPart(time.Second, true)
    }
}()

// Origin side: mount the playlist on any path.
http.Handle("/live/playlist.m3u8", pl)
```

For deterministic tests, inject a `*VirtualClock` through `Config.Clock`,
publish parts (or run `Encoder`) on goroutines, and advance simulated time
with `clock.Advance(...)`. Blocking requests wait on the same virtual timers.

## Spec behavior

- `_HLS_msn` without `_HLS_part` blocks until the full segment is complete.
- `_HLS_msn` with `_HLS_part` blocks until that 0-based part exists.
- `_HLS_msn` greater than the last playlist MSN plus one yields
  `400 Bad Request` (RFC 8216, Blocking Playlist Reload).
- `_HLS_part` without `_HLS_msn`, or malformed parameters, yield
  `400 Bad Request`.
- A blocked request that times out returns `200` with the current playlist.
- `CAN-SKIP-UNTIL` defaults to `6 * TargetDuration` and rejects smaller
  values, matching the spec minimum.

## Tests

```sh
go test -race ./...
go test -cover ./...
```

The suite asserts every response (status code, headers, tag presence, part and
segment URIs, skip counts, and sequence numbers) and runs concurrent blocking
requests against a shared playlist under the race detector.
