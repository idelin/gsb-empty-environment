# spandep

Builds service dependency edges from paired client/server telemetry spans.
Telemetry is synthesized locally into replayable fixtures; nothing talks to a
monitoring platform and there is no dashboard — the output is a dependency
edge report with uncertain evidence kept explicit.

## Semantics

- **Pairing**: a server span pairs with its parent span iff the parent is a
  client span in the same trace. One pair = exactly one edge call; the two
  spans of a request are never counted twice.
- **Retries**: attempts of one logical call are distinct client spans (same
  trace, same parent, same operation) with their own server children. Each
  attempt is one edge call with its own outcome, so a failed attempt plus a
  successful retry counts 1 error + 1 success — never double-counted as one
  call, never collapsed into one.
- **Success vs error**: counted separately per edge. A call is an error if
  either side reports `error`, otherwise a success.
- **Unpaired spans**: kept as uncertain evidence with a reason (missing
  client/server counterpart), never dropped and never forced into an edge.
- **Missing service name**: a pair with an empty service on either side is
  kept as uncertain evidence; the real service is never guessed.
- **Duplicate exports** (same `traceId`+`spanId`) are dropped, first wins.

## Usage

```sh
# synthesize telemetry locally into a replayable fixture
go run ./cmd/spandep synth -seed 1 -out fixtures/sample.json

# replay a fixture into a dependency edge report
go run ./cmd/spandep replay fixtures/sample.json

# tests
go test ./...
```

## Sample run

```
$ go run ./cmd/spandep synth -seed 1 -out fixtures/sample.json
$ go run ./cmd/spandep replay fixtures/sample.json
dependency edges (caller -> callee: calls ok err):
  web -> api: calls=2 ok=1 err=1
  web -> billing: calls=2 ok=1 err=1
duplicate spans dropped: 1
uncertain evidence (3):
  [GET /health] span=lonely-srv trace=trace-5 kind=server service="api" status=ok: server span without matching client span; caller unknown
  [GET /legacy] span=cli-srv trace=trace-6 kind=server service="" status=ok: paired call "GET /legacy" with missing service name (caller="api" callee=""); not guessed
  [GET /search] span=lonely-cli trace=trace-4 kind=client service="web" status=error: client span without matching server span; callee unknown
```

## Test results

```
$ go test ./...
ok  	spandep	0.388s
```
