# Retractable Min-Max Gap Network (exact integer/rational time)

A temporal constraint solver in pure Python 3.11 (standard library only).
Each constraint has the exact form

```
lower <= time(target) - time(source) <= upper        (lower/upper: Fraction)
```

i.e. a min-max gap between two events; absolute time ranges are constraints
against a distinguished anchor variable. All arithmetic is exact rational
arithmetic (`fractions.Fraction`) — no floats, no epsilon tolerances, so a
conflict of exactly `1/3` is detected as-is.

## Layout

- `temporal/exact_time.py` — three distinct, non-interchangeable types:
  - `Duration`: a length of time (exact Fraction of a second);
  - `TimePoint`: an absolute instant (epoch + `Duration`);
  - `WallClock`: a civil clock reading in a named offset zone;
  - `OffsetTable`: synthetic offset conversion table (`define` base zones,
    `derive` composed offsets such as a shift schedule on top of a zone),
    converting `WallClock -> TimePoint` exactly.
- `temporal/network.py` — `TemporalNetwork`, the retractable min-max gap
  network (difference-constraints system):
  - `add_constraint` / `set_range` propagate **incrementally** (SPFA seeded
    at the new edges) and report the **earliest/latest feasible bounds** of
    every event;
  - `push()` / `pop()` retract assumption layers; after a pop the bounds are
    recomputed from scratch, so bounds that lost their support are relaxed;
  - every mutation records a **history version** with a bound snapshot
    (`history`, `bounds_at(version)`);
  - contradictions are reported as **negative cycles over the original
    constraint IDs** (`contradiction()` / `contradiction_detail()`);
  - a relaxation **budget**: when exhausted the status becomes `UNKNOWN`
    and `schedule()`/`bounds()` refuse to answer — a budget interruption
    never returns a pseudo-feasible schedule;
  - the uniform-translation degree of freedom is explicit: unanchored
    events report unbounded bounds, `set_range(event, v, v)` anchors them;
  - unconstrained events are returned separately by `bounds_all()`.
- `temporal/validation.py` — `reference_solve`, an **independent**
  whole-graph Bellman-Ford used to cross-validate the incremental network.
- `tests/` — unittest suite (see below).
- `examples/demo.py` — runnable end-to-end sample.

## Run

```
python3.11 examples/demo.py
python3.11 -m unittest discover -s tests -v
```

## Sample run (`examples/demo.py`)

```
=== 2. B-A in [3,5] and A-B >= 0  ->  infeasible, cycle evidence ===
status          : INFEASIBLE
negative cycle  : nodes=['B', 'A'] constraints=['ordBA', 'gapAB'] weight=-3
schedule()      : refused -> network is infeasible; see contradiction()

=== 3. Push/pop assumptions + history + earliest/latest bounds ===
bounds B (base) : [3, 15]   (version 2)
bounds B (push) : [3, 14]   (version 4)
bounds B (pop)  : [3, 15]   (unsupported bound relaxed)
  v1  add rA                 -> FEASIBLE
  v2  add gAB                -> FEASIBLE
  v3  push                   -> FEASIBLE
  v4  add assume-tight       -> FEASIBLE
  v5  pop [assume-tight]     -> FEASIBLE

=== 4. Uniform translation freedom, then anchor ===
unanchored A,B  : A=[-inf, +inf] B=[-inf, +inf] (free shift)
witness schedule: {'A': Fraction(-3, 1), 'B': Fraction(0, 1)}
anchored A=0    : A=[0, 0] B=[3, 5]

=== 7. Budget interruption ===
status          : UNKNOWN  (budget 40, chain of 299)
schedule()      : refused -> budget exhausted; result unknown, not feasible

=== 8. Exact 1/3-cycle conflict ===
status          : INFEASIBLE, cycle=['t2', 't3', 't1'], weight=-1 (exactly -1)
```

## Test results

`python3.11 -m unittest discover -s tests -v` — **19 tests, OK**, covering:

- exact `Duration`/`TimePoint`/`WallClock` arithmetic and type distinctions
  (e.g. `TimePoint + TimePoint` is a `TypeError`);
- synthetic offset table conversion (`UTC -> CST -> SHIFT`, 09:00 CST ==
  09:30 SHIFT exactly);
- the required example `B-A in [3,5]` + `A-B >= 0` → `INFEASIBLE` with the
  negative cycle reported as constraint IDs `{gapAB, ordBA}`, weight `-3`;
- an exact `1/3`-per-edge 3-cycle conflict (weight exactly `-1`) that float
  tolerance could mask;
- push/pop: tightened bounds relax again after retraction; pop restores
  feasibility; history versions are monotonic and queryable;
- anchoring: relative-only networks keep the uniform-shift freedom
  (unbounded bounds) yet still yield a witness schedule; pinning fixes it;
- unconstrained events returned separately with range `(-inf, +inf)`;
- budget interruption: status `UNKNOWN`, `schedule()`/`bounds()` raise
  `UnknownError` — never a pseudo-feasible answer;
- **randomized cross-validation**: 3 seeds x 200 random
  add/set_range/push/pop operations; after every mutation the incremental
  network's feasibility, per-event bounds, witness schedule and negative
  cycle evidence are checked against the independent full
  difference-constraints solve in `temporal/validation.py`.

```
Ran 19 tests in 6.657s

OK
```
