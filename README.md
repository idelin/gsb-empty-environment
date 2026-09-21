# Streaming A→B-without-C pattern matcher

Python-standard-library event-time CEP engine.

## Pattern

Per key `k`: an `A` followed by a `B` with

```
0 <= t(B) - t(A) <= 5 ms        # closed interval, inclusive endpoints
```

and **no `C` strictly between them**. A single `B` matches every live,
non-expired `A`, so overlapping matches are allowed.

## Ordering semantics

Records are first ordered by event time. Records with the **same** timestamp
are ordered by a fixed total tie-break:

```
A  <  C  <  B,   then (source, seq)
```

"C lies between A and B" means `A < C < B` in this total order (strict on
both sides). Consequences at equal times:

| records at same time | result |
|---|---|
| `A@t, B@t` | match (`d = 0`, and no C between) |
| `A@t, C@t, B@t` | blocked — C is strictly between |
| `C@t, B@t` without an earlier `A` | no match anyway |

Window endpoints are inclusive: `A@0,B@5` matches; `A@0,B@6` does not.
That is why with `A@0 A@1 B@6`, only `A@1 → B@6` matches.

Watermark closure is half-open: watermark `W` promises no later record with
time `< W`. A match ending at `B@b` is finalized when `W > b` (so at `W == b`
it is still open and revisable).

## Result lifecycle

1. `on_match_proposed(match)` — tentative, retractable result (`+`).
2. A later `C` between the pair, or a withdrawal of either endpoint, emits
   `on_match_revoked(match, cause)` (`-`). Withdrawing the blocking `C`
   re-proposes.
3. When the watermark passes the `B`, `on_match_final(match)` is emitted
   (`#`); the result is immutable thereafter.

## Late events and watermarks

* `t < W` events are **late**.
* Late `A`/`C` with `t + 5 >= W` may still touch **open** matches; they are
  incorporated and also surfaced via a `late_incorporated` side report.
* Late `B` is never incorporated: every A it could pair with is already
  sealed, so accepting it could only change finalized results.
* Anything out of scope (e.g. late `C` with `t + 5 < W`) is emitted via
  `on_side_report` (`out_of_scope_late`) and never alters finalized results.
* Withdrawals of unknown/pruned/finalized identities produce
  `withdraw_unknown_or_closed` side reports; finalized results stay put.
* Identity is `(source, seq)`; duplicates are ignored and reported
  (`duplicate`). Events and their withdrawals share the identity space but
  are deduplicated in separate namespaces; an event arriving after its own
  withdrawal is rejected (`event_after_withdraw`).

## State bounds

* `A`/`C` entries pruned once `t + 5 < W`; `B` entries once `t < W`.
* Open match candidates live only until their `B` is sealed.
* Pending matches are therefore released boundedly as `W` advances
  (see `consistency_demo` output).

## Layout

```
src/streamcep/
  matcher.py          streaming engine, Collector interface, watermark driver
  reference.py        total-order enumeration reference (ground truth)
  consumer.py         end-to-end worked example printing +/-/#/~ notifications
  consistency_demo.py order/chunking convergence vs reference; state release
tests/test_pattern.py unittest suite (19 tests)
```

## Run

```
PYTHONPATH=src python3 -m streamcep.consumer
PYTHONPATH=src python3 -m streamcep.consistency_demo
python3 tests/test_pattern.py -v
```

## Minimal API

```python
from streamcep import Event, Withdraw, Watermark, StreamMatcher, Collector

class Sink(Collector):
    def on_match_proposed(self, match, late=False): ...
    def on_match_revoked(self, match, cause, late=False, blocker=None): ...
    def on_match_final(self, match): ...
    def on_side_report(self, report): ...

m = StreamMatcher(Sink())
m.push(Event("A", 0, "key", "source-1", 1))
m.push(Event("B", 3, "key", "source-1", 2))
m.advance(10)          # or m.push(Watermark(10))
```
