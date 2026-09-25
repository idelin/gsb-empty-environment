# exact-timeline

Deterministic keyframe/timeline engine in pure TypeScript with **zero
dependencies**. All core arithmetic runs on exact integer ticks
(1 tick = 1µs), so period boundaries, event deduplication and pause/resume
are exact — no floating-point drift anywhere. Layout is computed from data
fixtures (node tree + glyph metrics); no browser layout engine is involved.

## Requirements

- Node.js **≥ 23.6** runs the `.ts` files directly (type stripping).
  Developed and verified on **v23.9.0**.
- Node 22.6–23.5 also works with `node --experimental-strip-types`.

## Commands

```sh
node --test              # run the built-in test suite (node:test)
node examples/demo.ts    # run the scripted demo
```

## Semantics (the defined behavior)

- **Random access ≡ sequential advance** — sampling is a pure function of
  the exact local tick; seeking and stepping always agree.
- **Pause/resume without drift** — the player re-anchors on every state
  change and computes time as `anchor + (now - anchor) * rate`; nothing is
  accumulated incrementally.
- **Crossing-interval events** — moving `from → to` delivers events in
  `(from, to]` going forward and `[to, from)` going backward. Consecutive
  intervals tile the timeline exactly: boundary events fire once, never
  re-fire. Coincident events (same name/property/time, e.g. adjacent
  iterations at a period boundary) are delivered once.
- **Exact period boundary** — at `delay + duration * iterations` the
  animation sits at the end of the *last* iteration; it never rolls into an
  extra round.
- **Reverse = time reversal** — reversed playback evaluates the forward
  curve at `duration - t`; keyframe values are never swapped.
- **Negative delay** starts the animation partway through; **zero duration**
  is an instant transition (directed start before, directed end at/after).
- **Reduced motion** — deterministic, time-independent rest state: the
  directed value at the end of the active phase.
- **Hot-swap** — replacing a track bumps the animation version; previously
  generated events become invalid, the playback position is untouched and
  resumable.
- **Nested timelines** — per-edge `rate` (may be negative) composes with
  pause segments (parent-frame freeze intervals); child events are re-mapped
  to the parent frame by exact integer inversion of the time mapping.

## Layout

`src/layout.ts` measures text by summing glyph advances from
`fixtures/glyphs.ts`, wraps greedily (hard-breaking overlong words), and
stacks box children vertically with padding/gap. All pure arithmetic over
the fixtures in `fixtures/`.

## Sample run (`node examples/demo.ts`)

```
== 1. keyframes/easing/repeat/direction, random access ==
  t=   0ms  opacity=0
  t= 250ms  opacity=0.063
  t= 500ms  opacity=0.5
  t=1000ms  opacity=1
  t=1250ms  opacity=0.75
  t=2000ms  opacity=0
== 2. nested timeline: negative rate, pause segment ==
  t= 900ms  opacity=0.7 x=60
  t=1200ms  opacity=1 x=60        <- slide frozen by the pause segment
  t=1500ms  opacity=0.7 x=30
== 3. events delivered by crossing intervals ==
  drain at t= 300ms: begin@200ms(root/fade)
  drain at t= 700ms: half@700ms(root/fade)
  drain at t=1200ms: end@1200ms(root/fade)
== 4. pause/resume accumulates zero drift ==
  paused at 1000ms
  still   at 1000ms after 4s of wall time
  resumed at 1500ms (exactly +500ms of play)
== 5. hot-swap: old events invalid, position resumable ==
  version bumped to 2; position kept at 1500ms (was 1500ms)
  re-seeked to 1000ms; new events: swap-end@1200ms v2
== 6. reduced motion: deterministic rest state ==
  t=    0ms  opacity=1 x=100
  t=99999ms  opacity=1 x=100
== 7. layout from fixtures (no browser layout engine) ==
  root x=0 y=0 w=100 h=132
    title x=10 y=10 w=80 h=16
      "Hello world" (w=72)
    card x=10 y=32 w=80 h=90
      ...
```

## Test results (`node --test`, Node v23.9.0)

31 tests, all passing — including property-style checks with seeded random
trajectories (600 mixed seek/pause/resume/rate/scale steps verified against
direct evaluation, and 100 random interval-concatenation trials):

```
ℹ tests 31
ℹ pass 31
ℹ fail 0
```

Highlights of what is verified:

- `test/trajectory.test.ts` — random time trajectories vs direct evaluation;
  crossing-interval concatenation forward/backward; boundary no-re-fire;
  hot-swap invalidation + resumable position.
- `test/player.test.ts` — 1000 pause/resume cycles with zero drift; reduced
  motion determinism; timeScale × playbackRate composition.
- `test/timeline.test.ts` — negative child rates, pause segments, nested
  event paths, seek ≡ sequential.
- `test/timing.test.ts` — exact period boundary, negative delay, zero
  duration, reverse = time reversal.
- `test/layout.test.ts` — fixture-only measurement/wrapping/box layout plus
  generated-string properties.
