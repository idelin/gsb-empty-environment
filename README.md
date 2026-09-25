# Deterministic Motion Engine

A dependency-free TypeScript animation engine: keyframes, easing, repetition
and direction compose on nested timelines, with exact-time semantics that make
arbitrary seeking and sequential advancement indistinguishable. Verified with
Node's built-in test runner (`node:test`); no browser layout engine is used
anywhere — layout is computed from fixtures.

## Requirements

- Node.js >= 23.6 (runs TypeScript directly via type stripping), or
  Node.js >= 22.6 with `node --experimental-strip-types`.

## Run

```sh
npm test          # node --test  (39 tests)
npm run sample    # node samples/demo.ts
```

## Design: time is always a pure function

Every value is computed as a pure function of root clock time. Nothing is
accumulated incrementally, which is what makes the guarantees below hold:

- **Random access**: `valueAt(t)` is total; `seek(t)` and sequential `tick()`
  share one advance path (`Player.advanceTo`), so both observe identical
  property values (`test/trajectory.test.ts` proves it on auto-generated
  trajectories against direct evaluation).
- **Pause/resume without drift**: `Clock` accumulates only whole active
  segments; wall time during a pause is never folded in.
- **Exact time scaling**: player `timeScale` x child `rate` compose in a
  single multiplication from the root clock reading — no `t += dt * scale`
  accumulation.

## Semantics

- **Keyframes/easing**: per-segment easing (linear, named easings,
  `cubic-bezier`, `steps`). Offsets outside [first, last] hold.
- **Repetition & direction**: `iterations` (may be `Infinity`), `direction`
  normal/reverse/alternate/alternate-reverse.
- **Reversed curves are time reversals**: `reverse` evaluates the forward
  track at mirrored time (`sample(1 - p)`), it does not approximate reversal
  by swapping keyframe values.
- **Exact period boundary**: active time == `iterations * duration` resolves
  to the end state of the final iteration — never an extra round.
- **Negative delay** starts the animation partway; **zero duration** is
  defined (start state before the delay, end state at/after it).
- **Pause segments** (`Timeline.addPause(at, duration)`): the playhead holds
  while input time crosses the segment; the mapping stays a pure function.
- **Negative playback rate**: a child plays from its end to its start; outside
  its window the first/last frame (in play order) holds.
- **Events (cross-interval semantics)**: advancing from `t0` to `t1` delivers
  events in `(t0, t1]` forward and `[t1, t0)` backward, in travel order, with
  a `direction` flag. An event exactly on a shared boundary belongs to exactly
  one of two adjacent intervals, so boundary events never fire twice.
- **Hot-swap**: `setKeyframes` bumps a keyframe generation; events registered
  against older generations are invalidated, and the play position resumes
  against the new track.
- **Reduced motion**: `reducedMotion` yields a deterministic constant output
  (the end state) for any time and suppresses event delivery.
- **Layout**: node tree + glyph metrics come from `fixtures/`; measurement,
  word wrap and box layout are computed in `src/layout.ts`.

## Layout

```
src/
  easing.ts     easings, cubic-bezier solver, steps, time-reversed easing
  keyframes.ts  sorted keyframe track sampling
  animation.ts  KeyframeAnimation: delay/duration/iterations/direction/events
  clock.ts      pausable drift-free clock (injectable time source)
  timeline.ts   nested timelines, pause segments, negative rates, events
  player.ts     seek/tick share one advance path; timeScale composition
  layout.ts     fixture-driven text measurement and box layout
fixtures/       glyph-metrics.json, node-tree.json
test/           node:test suites (39 tests)
samples/demo.ts runnable feature tour
```

## Sample output (`npm run sample`)

```
=== 1. keyframes + easing + iterations + direction (alternate) ===
  t=0  x=0
  t=0.5  x=25
  t=1  x=100
  t=1.5  x=25
  t=2  x=0
  t=2.5  x=0
  exact boundary t=2 lands on the end state, no extra round

=== 2. clock pause/resume accumulates no drift ===
  active time after 10 + (1e9 paused) + 5 = 15 (expected 15)

=== 3. nested timeline: negative rate + pause segment + keyframe events ===
  t     sequential        direct valueAt(t)
  0     x=0 y=10               x=0 y=10  OK
  0.5   x=25 y=10              x=25 y=10  OK
  1     x=50 y=10              x=50 y=10  OK
  1.25  x=50 y=10              x=50 y=10  OK
  2     x=75 y=10              x=75 y=10  OK
  2.5   x=100 y=10             x=100 y=10  OK
  3     x=100 y=5              x=100 y=5  OK
  3.5   x=100 y=0              x=100 y=0  OK
  4     x=100 y=0              x=100 y=0  OK

=== 4. events delivered for the crossed interval (t0, t1] ===
  t=1  mid  direction=1  at timeline
  t=2.5  keyframe  direction=1  at timeline/child[0]
  t=3.5  keyframe  direction=-1  at timeline/child[1]/child[0]
  boundary events not re-fired: OK

=== 5. time scale composition uses exact time ===
  position=4.5  exact=4.5  equal=OK

=== 6. reduced motion: deterministic constant output ===
  outputs: x=100 | x=100 | x=100 | x=100 | x=100
  deterministic: OK

=== 7. hot-swap keyframes: old events invalidated, position resumes ===
  before swap: position=1.5 value=x=75
  after swap:  position=2 value=x=0
  events after swap: keyframe
  old event invalidated: OK

=== 8. fixture-driven layout (no browser layout engine) ===
  t=0  root.width=120  root.height=58  title lines=1 ["alpha beta gamma"]
  t=0.5  root.width=90  root.height=70  title lines=2 ["alpha beta", "gamma"]
  t=1  root.width=60  root.height=82  title lines=3 ["alpha", "beta", "gamma"]
```

## Test results (`npm test`)

```
✔ keyframes + easing combine: eased interpolation between offsets
✔ held segments before the first and after the last offset
✔ repetition and direction: alternate reverses odd iterations by time
✔ reverse direction is exact time reversal of the forward curve
✔ exact period boundary does not skip an extra round
✔ negative delay starts the animation partway
✔ zero duration is defined: start state before delay, end state at/after
✔ infinite iterations keep cycling
✔ keyframe events fire per iteration at exact local times
✔ pause freezes time; wall time during pause never leaks in
✔ repeated pause/resume cycles accumulate no drift
✔ pause/resume are idempotent
✔ linear is the identity
✔ cubic-bezier hits endpoints and midpoint of the symmetric curve
✔ cubic-bezier rejects out-of-range x values
✔ steps end/start hold then jump
✔ reverseEasing mirrors time: reverse(e)(t) === 1 - e(1 - t)
✔ resolveEasing accepts names, functions and undefined
✔ cross-interval semantics: forward (t0, t1], backward [t1, t0)
✔ boundary events are delivered exactly once across adjacent advances
✔ nested events map to root input time across pauses and negative rates
✔ sequential small steps and one big seek deliver the same event stream
✔ boundary events do not re-fire when advancing past a period edge
✔ events carry the path of the node that produced them
✔ hot-swapping keyframes invalidates old events and resumes position
✔ events registered against an older keyframe generation are dropped
✔ text measurement uses fixture glyph advances, no layout engine
✔ fixture node tree lays out deterministically
✔ animated props reflow text wrapping without a browser
✔ reduced motion yields a deterministic constant output for any time
✔ reduced motion suppresses event delivery
✔ pause segments hold the playhead; mapping is a pure function of input time
✔ overlapping pause segments are rejected
✔ nested timeline with negative rate plays the child end -> start
✔ values are identical for arbitrary seeking and sequential advancement
✔ time scaling composes exactly: player scale x child rate, no accumulation
✔ nested pause segments compose through child time mapping
✔ auto-generated trajectory: sequential, random-access and direct evaluation agree
✔ event stream is identical for stepped advancement and a single seek

ℹ tests 39
ℹ pass 39
ℹ fail 0
```
