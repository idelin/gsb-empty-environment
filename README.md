# explicit-time-anim

A TypeScript animation **sampling** library. There is no clock and no
scheduler: every control and every sample takes an explicit external
timestamp, which makes animations fully deterministic and trivially
testable.

## Features

- **Delay** before the active phase (`delay`)
- **Finite repeats**, including fractional counts (`iterations: 2.5`)
- **Yoyo** and friends (`direction: 'normal' | 'reverse' | 'alternate' | 'alternate-reverse'`)
- **Negative playback rates** (`playbackRate`, changeable mid-flight)
- **Pause/resume without jumps** — local time is continuous by construction
- **Cancel without a completion event** — `cancel()` never fires `onfinish`
- **Zero-duration animations** with well-defined before/after values

## Model

Local animation time is derived from external time through a
**piecewise time map** (`PiecewiseTimeMap`): a list of segments, each
anchoring a local time at an external start time and advancing at a
constant rate (`local = startLocal + (t - startTime) * rate`). Pausing is
a segment with rate 0.

Every operation (`pause`, `resume`, `seek`, `setPlaybackRate`) rewrites
the map at its timestamp: segments at or after that time are discarded
and a new segment is appended. Consequences:

- **Same-timestamp operations compose in call order.** Each op observes
  the local time produced by the previous op at that instant, and the
  last op wins for the mapping going forward. `pause(t); resume(t)` is a
  no-op; `pause(t); seek(x, t); resume(t)` applies the seek.
- **Zero-length segments never corrupt the mapping** — they are replaced
  by the next op at the same timestamp.
- **Resume cannot jump**, because the new segment anchors exactly the
  frozen local time.

## Endpoint ownership

Inside the active phase (`active = local - delay`,
`total = duration * iterations`):

| Where | Value |
| --- | --- |
| `active < 0` (before phase) | start of iteration 0 |
| interior boundary `active == k * duration`, `k < iterations` | **start of the new iteration** (progress 0 of iteration `k`) |
| `active == total` (end of the last iteration) | **final value of the last iteration** |
| `active > total` (after phase) | final value, held |

So an animation with 3 iterations of 100ms shows `~end` at `t=99.9`,
`start` at `t=100` (the boundary belongs to iteration 1), but keeps the
final value at `t=300`. A zero-duration animation flips from the start
value to the final value exactly at `delay` and finishes there.

The directed progress (after applying `direction` to the iteration
parity) is passed through `easing`, then interpolated between `from` and
`to`.

## Events

`onfinish(finishTime)` fires **exactly once** per natural completion,
detected lazily during `sample(t)` (positive rate: local time reaches
`delay + duration * iterations`; negative rate: local time reaches 0).
After finishing, the boundary value is held. `cancel()` returns to
`idle`, stops sampling (`undefined`), and never emits a completion
event. `seek()` on a finished animation re-arms it as paused.

## API

```ts
const anim = new Animation({
  from: 0, to: 100,
  duration: 1000,        // per iteration; may be 0
  delay: 200,
  iterations: 2,         // finite, may be fractional
  direction: 'alternate',
  playbackRate: 1,       // may be negative
  easing: (p) => p * p,  // applied to the directed progress
  onfinish: (t) => console.log('finished at', t),
});

anim.play(0);            // all times are explicit external timestamps
anim.sample(700);        // => 50
anim.pause(400);
anim.resume(1000);       // no jump
anim.seek(500, 1000);    // local ms, clamped to [0, end]
anim.setPlaybackRate(-1, 1000);
anim.cancel();           // idle again, no finish event
anim.state;              // 'idle' | 'running' | 'paused' | 'finished'
anim.localTime(t);       // local time via the piecewise map
```

## Development

```sh
npm test        # node test/run-tests.mjs (no dependencies)
npm run build   # tsc -p tsconfig.json (regenerates dist/)
```

`src/` is the TypeScript source; `dist/` is the committed build output
that the tests run against.
