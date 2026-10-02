/**
 * Clock abstractions.
 *
 * A lease kernel must never trust the wall clock alone: wall clocks can jump
 * (NTP corrections, manual changes, VM snapshots). We therefore track two
 * time sources:
 *  - wallNow():      wall-clock time in ms (may jump forwards/backwards)
 *  - monotonicNow(): monotonic time in ms (never jumps, may drift slightly)
 *
 * Divergence between the two beyond the configured max drift is treated as a
 * clock anomaly and triggers an emergency demotion.
 */
export interface Clock {
  wallNow(): number;
  monotonicNow(): number;
}

/** Real clock backed by Date.now() and process.hrtime. */
export class SystemClock implements Clock {
  wallNow(): number {
    return Date.now();
  }
  monotonicNow(): number {
    const [sec, nano] = process.hrtime();
    return sec * 1000 + nano / 1e6;
  }
}

/** Fully controllable clock for deterministic tests and simulations. */
export class ManualClock implements Clock {
  private wall: number;
  private mono: number;

  constructor(startWallMs = 0) {
    this.wall = startWallMs;
    this.mono = 0;
  }

  wallNow(): number {
    return this.wall;
  }

  monotonicNow(): number {
    return this.mono;
  }

  /** Advance both clocks together: the normal passage of time. */
  advance(ms: number): void {
    this.wall += ms;
    this.mono += ms;
  }

  /** Jump only the wall clock: simulates an NTP step / admin time change. */
  jumpWall(ms: number): void {
    this.wall += ms;
  }
}
