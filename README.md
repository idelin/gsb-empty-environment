# piecewise-anim

Deterministic, explicit-time animation sampling for TypeScript. Supports
delay, finite (including fractional) repeats, yoyo (`alternate`) playback and
negative playback rates. Zero dependencies.

Requires Node >= 23.6 (runs TypeScript via type stripping). No build step.

```sh
npm test
```

## Usage

```ts
import { Animation } from "piecewise-anim";

const anim = new Animation({
  from: 0,
  to: 100,
  duration: 300,      // ms per iteration; may be 0
  delay: 100,         // ms before the active phase
  iterations: 2,      // finite, may be fractional (e.g. 2.5)
  direction: "alternate", // yoyo
  playbackRate: 1,    // negative plays in reverse
});

anim.on("finish", (e) => console.log("done at", e.globalTime));
anim.play(0);           // every call takes an explicit timestamp
anim.sample(200);       // => value at t=200
anim.pause(250);        // freeze
anim.sample(10_000);    // still the t=250 value: no jump
anim.play(10_000);      // resume exactly where it froze
anim.cancel();          // back to idle; never emits "finish"
```

The wall clock is never read. `sample(now)` is the driver: it maps the
explicit timestamp to a value and, while running, detects crossing the finish
boundary and emits `finish` exactly once.

## Semantics

### Local timeline

`delay`, `duration` and `iterations` define a local timeline. The active
phase is `[delay, delay + duration * iterations)`; `endTime` is its end.

- Before `delay`: holds the start-of-first-period value.
- After `endTime`: holds the final value (fill forward).

### Endpoint ownership

- An exact period boundary inside the active phase belongs to the **start of
  the new period** (`floor` selects the new iteration, phase 0).
- The end of the **last** period is the single exception: local times
  `>= endTime` keep the **final value** of the last iteration.
- With `direction: "alternate"`, odd iterations play in reverse, so the final
  value of an even iteration count is `from`, not `to`.

### Zero duration

`duration: 0` (or any timing with `activeDuration === 0`) is a step function
at `delay`: the start value before, the final value at and after. Finish
fires on the first sample at or past `delay`.

### Piecewise time mapping

Global time is mapped to local time through a list of segments
`{ startGlobal, startLocal, rate }`; `pause()` is simply a `rate: 0` segment.
Every operation keeps local time continuous, so pause/resume and rate changes
never jump. Multiple operations at the **same timestamp** collapse into one
segment (last one wins) — no zero-length segments. Operation timestamps must
be non-decreasing. Inspect the map via `localTimeAt(t)` and `getSegments()`.

### Negative playback rate

A fresh `play()` with a negative rate starts at `endTime` and runs backwards;
the animation finishes when local time reaches 0. Rate changes mid-run
continue from the current local time.

### Cancel

`cancel()` resets to `idle`, clears the segment map and **never emits
`finish`**. After cancel, `sample()` returns `undefined` until the next
`play()`.
