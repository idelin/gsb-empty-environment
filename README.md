# hedged

Hedged (speculative) fetching over two injectable data sources, Go standard
library only.

- Cache miss: the primary source is queried first; the second source starts
  only after `HedgeDelay` (or immediately if the primary fails first).
- Candidates carry a version + CRC32 checksum. The first **valid** result
  with `Version >= MinVersion` wins; an early but stale result cannot win.
- The winner cancels losing candidates; late results are discarded and never
  overwrite the winner (or the cache).
- Speculative requests consume a global quota (`SpeculativeQuota`) shared by
  all in-flight Gets; without quota the hedge is skipped.
- If both sources fail (or return invalid/stale data), the reasons are
  aggregated with `errors.Join`.
- Sources, clock (`Clock` interface), and failures are injectable; there is
  no remote cache dependency (the cache is an in-memory map).
- `Close` rejects new Gets and waits for all in-flight goroutines to exit.

## Run

```sh
go test -race ./...
go run ./cmd/demo
```
