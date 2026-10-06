# gwsim — gateway rate-limit simulation

A gateway simulation built on an exactly-computed token bucket limiter.
Time is expressed in **integer nanoseconds** and token rates are
**rational numbers** (`math/big.Rat`), so all accounting is exact — no
floating-point drift.

## Semantics

- **Lazy refill.** Nothing is computed between events. At each event the
  bucket is settled to the event timestamp: `tokens += rate * elapsed`.
- **Capacity truncation discards the remainder.** When a refill pushes the
  balance above the capacity, the balance is set to exactly the capacity.
  The truncated excess is never stored or carried into later events.
- **Config changes settle under the old config first.** `SetConfig(now, cfg)`
  settles the bucket to `now` using the *old* rate and capacity, and only
  then installs the new config. A balance above the new capacity is kept
  until the next event, where lazy truncation applies.
- **Backwards time is rejected.** Any event timestamp earlier than the last
  settled timestamp fails with `ratelimit.ErrTimeBackwards` and leaves the
  entire state (balance, clock, config) unchanged. Equal timestamps are
  allowed.
- **Failed withdrawals don't withdraw.** A request that costs more than the
  settled balance is rejected; the balance is untouched (the clock has still
  been settled to the event time).

## Layout

- `ratelimit/` — the token bucket limiter (`Limiter`, `Config`).
- `gateway/` — a request/deterministic-decision simulator driving a limiter.

## Verification

`ratelimit/limiter_test.go` defines an independent **rational per-event
model** and requires the limiter to agree with it after every event:

- long idle periods (truncation to exactly the capacity, remainder discarded),
- boundary consumption (exact-balance success, hair-above rejection),
- rate and capacity switching (old-config settlement at the switch instant),
- backwards-time rejection with provably unchanged state,
- a 2000-event randomized stream cross-checked event by event.

Run with:

```sh
go test ./...
```
