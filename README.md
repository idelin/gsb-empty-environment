# breaker

Per-group circuit breaker in pure Go standard library. Each group name gets
an independent circuit with `closed` / `open` / `half-open` states.

## Design

- **Per-group state**: `Manager` keeps an independent `group` per name; a
  failure in group A never changes group B.
- **Fast fail**: while `open`, `Execute` returns the identifiable sentinel
  `ErrOpen` without running the task. While `half-open`, at most
  `HalfOpenMaxProbes` concurrent probes are admitted; the rest get
  `ErrProbeLimit`.
- **Generation checks**: every state transition bumps `generation`. Probe
  results and open-state expiry callbacks carry the generation they were
  created in and are discarded when it no longer matches, so a stale failure
  can never reopen a newer generation that already recovered.
- **Injected dependencies**: the guarded work is a `func() error` supplied by
  the caller; time comes from the `Clock` interface and expiry scheduling
  from the `Scheduler` interface, so tests are fully deterministic. Nil
  values default to real time (`time.Now` / `time.AfterFunc`).
- **Synchronisation**: `sync.Mutex` only (state machine per group, map guard
  in the manager). Validated with `go test -race`.

## Usage

```go
m := breaker.NewManager(breaker.Options{
    FailureThreshold:  2,                   // consecutive failures to open
    OpenDuration:      300 * time.Millisecond,
    HalfOpenMaxProbes: 1,
})
err := m.Execute("payments", func() error { return callBackend() })
switch {
case errors.Is(err, breaker.ErrOpen):       // circuit open, fast fail
case errors.Is(err, breaker.ErrProbeLimit): // half-open quota exhausted
}
```

## Run

```
go test -race -v ./...
go run ./cmd/demo
```

## Test results

```
=== RUN   TestOpenAfterThresholdAndFastFail      --- PASS
=== RUN   TestOpenExpiresToHalfOpen              --- PASS
=== RUN   TestHalfOpenProbeQuota                 --- PASS
=== RUN   TestHalfOpenProbeFailureReopens        --- PASS
=== RUN   TestStaleProbeResultIgnored            --- PASS
=== RUN   TestStaleExpiryCallbackIgnored         --- PASS
=== RUN   TestGroupIsolation                     --- PASS
=== RUN   TestClosedSuccessResetsFailures        --- PASS
=== RUN   TestConcurrentRace                     --- PASS
=== RUN   ExampleManager                         --- PASS
ok      breaker
```

## Demo output (real clock, backend recovers on the 5th actual call)

```
t=   0ms  attempt 01  state=closed    err=backend unavailable
t= 100ms  attempt 02  state=open      err=backend unavailable
t= 200ms  attempt 03  state=open      err=breaker: circuit is open
t= 300ms  attempt 04  state=open      err=breaker: circuit is open
t= 400ms  attempt 05  state=open      err=backend unavailable   <- half-open probe, fails, reopens
t= 500ms  attempt 06  state=open      err=breaker: circuit is open
t= 600ms  attempt 07  state=open      err=breaker: circuit is open
t= 700ms  attempt 08  state=open      err=backend unavailable   <- half-open probe, fails, reopens
t= 800ms  attempt 09  state=open      err=breaker: circuit is open
t= 900ms  attempt 10  state=open      err=breaker: circuit is open
t=1000ms  attempt 11  state=closed    err=<nil>                 <- probe succeeds, closes
t=1100ms  attempt 12  state=closed    err=<nil>
```
