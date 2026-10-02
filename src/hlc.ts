/**
 * Hybrid Logical Clock (HLC) engine.
 *
 * Combines a physical wall clock with a logical counter to produce
 * timestamps that are:
 *   - strictly monotonic (never go backwards, even if the physical
 *     clock stalls or jumps backwards);
 *   - causally consistent (a receive event always yields a timestamp
 *     greater than the timestamp of the corresponding send event);
 *   - bounded to physical reality: the wall-clock component of any
 *     emitted timestamp never exceeds the local physical clock by more
 *     than `epsilon` (the maximum tolerated physical clock drift).
 *
 * Reference: Kulkarni et al., "Logical Physical Clocks and Consistent
 * Snapshots in Globally Distributed Databases" (2014), with the
 * CockroachDB-style hard drift bound enforced via ClockDriftError.
 */

/** An HLC timestamp: physical wall-clock millis + logical counter. */
export interface HLCTimestamp {
  /** Physical component, milliseconds since Unix epoch. */
  readonly wallTime: number;
  /** Logical component, disambiguates events within the same wallTime. */
  readonly logical: number;
}

/** Thrown when the HLC would drift more than epsilon ahead of the physical clock. */
export class ClockDriftError extends Error {
  readonly offset: number;
  readonly epsilon: number;

  constructor(offset: number, epsilon: number) {
    super(
      `HLC wall clock offset ${offset}ms exceeds maximum allowed ` +
        `physical clock drift (epsilon) of ${epsilon}ms`,
    );
    this.name = 'ClockDriftError';
    this.offset = offset;
    this.epsilon = epsilon;
  }
}

export interface HLCOptions {
  /**
   * Maximum tolerated drift (ms) between the HLC wall-clock component
   * and the local physical clock. Defaults to 500ms.
   */
  epsilon?: number;
  /**
   * Injectable physical clock (millis since epoch). Defaults to Date.now.
   * Injecting a clock makes stall / backward / forward jump behaviour
   * deterministically testable.
   */
  now?: () => number;
}

export class HybridLogicalClock {
  readonly epsilon: number;
  private readonly physicalNow: () => number;
  private lastWallTime = 0;
  private lastLogical = 0;

  constructor(options: HLCOptions = {}) {
    this.epsilon = options.epsilon ?? 500;
    if (!Number.isFinite(this.epsilon) || this.epsilon < 0) {
      throw new RangeError('epsilon must be a non-negative finite number');
    }
    this.physicalNow = options.now ?? Date.now;
  }

  /** Current physical clock reading (ms). */
  physicalTime(): number {
    return this.physicalNow();
  }

  /**
   * Local or send event: returns the next strictly monotonic timestamp.
   *
   * Throws ClockDriftError if the timestamp's wall-clock component would
   * lead the physical clock by more than epsilon, guaranteeing emitted
   * timestamps never detach from physical reality beyond the bound.
   */
  now(): HLCTimestamp {
    const pt = this.physicalNow();
    const wallTime = Math.max(this.lastWallTime, pt);
    const logical = wallTime === this.lastWallTime ? this.lastLogical + 1 : 0;
    return this.commit(wallTime, logical, pt);
  }

  /**
   * Receive event: merge a remote timestamp into the local clock and
   * return a timestamp causally after both the local clock and the
   * remote timestamp.
   */
  update(remote: HLCTimestamp): HLCTimestamp {
    const pt = this.physicalNow();
    const wallTime = Math.max(this.lastWallTime, remote.wallTime, pt);

    let logical: number;
    if (wallTime === this.lastWallTime && wallTime === remote.wallTime) {
      logical = Math.max(this.lastLogical, remote.logical) + 1;
    } else if (wallTime === this.lastWallTime) {
      logical = this.lastLogical + 1;
    } else if (wallTime === remote.wallTime) {
      logical = remote.logical + 1;
    } else {
      logical = 0;
    }
    return this.commit(wallTime, logical, pt);
  }

  /** The last timestamp this clock has observed (without advancing it). */
  last(): HLCTimestamp {
    return { wallTime: this.lastWallTime, logical: this.lastLogical };
  }

  private commit(wallTime: number, logical: number, pt: number): HLCTimestamp {
    const offset = wallTime - pt;
    if (offset > this.epsilon) {
      throw new ClockDriftError(offset, this.epsilon);
    }
    this.lastWallTime = wallTime;
    this.lastLogical = logical;
    return { wallTime, logical };
  }
}

/** Total order: negative if a < b, 0 if equal, positive if a > b. */
export function compare(a: HLCTimestamp, b: HLCTimestamp): number {
  if (a.wallTime !== b.wallTime) return a.wallTime - b.wallTime;
  return a.logical - b.logical;
}

export function equals(a: HLCTimestamp, b: HLCTimestamp): boolean {
  return a.wallTime === b.wallTime && a.logical === b.logical;
}

/** Canonical string form: `<wallTime>.<logical padded to 10 digits>`. */
export function toString(ts: HLCTimestamp): string {
  return `${ts.wallTime}.${String(ts.logical).padStart(10, '0')}`;
}

/** Parse a string produced by {@link toString}. */
export function fromString(s: string): HLCTimestamp {
  const match = /^(\d+)\.(\d+)$/.exec(s);
  if (!match) throw new SyntaxError(`invalid HLC timestamp: ${JSON.stringify(s)}`);
  return { wallTime: Number(match[1]), logical: Number(match[2]) };
}
