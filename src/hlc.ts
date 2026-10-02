/**
 * Hybrid Logical Clock (HLC) engine.
 *
 * Combines a physical wall clock with a logical counter to produce
 * timestamps that are:
 *  - strictly monotonic (never go backwards, even across clock stalls
 *    or short backward jumps of the physical clock),
 *  - causally consistent (a received timestamp can be merged so that
 *    every subsequent local timestamp is greater),
 *  - bounded to physical reality: |hlc.wallTime - physicalNow| <= epsilon,
 *    where epsilon is the maximum tolerated physical clock drift.
 *
 * Reference: "Logical Physical Clocks and Consistent Snapshots in
 * Globally Distributed Databases" (Kulkarni et al., 2014).
 */

/** Number of bits reserved for the logical counter in the packed 64-bit form. */
export const LOGICAL_BITS = 16n;
/** Maximum value the logical counter may take before it is considered overflow. */
export const MAX_LOGICAL = (1 << 16) - 1;

/** An HLC timestamp: physical wall-time component (ms) + logical counter. */
export interface Timestamp {
  /** Physical component: milliseconds since Unix epoch. */
  readonly wallTime: number;
  /** Logical component: disambiguates events within the same millisecond. */
  readonly logical: number;
}

/** Error thrown when the HLC drifts further than epsilon from physical time. */
export class ClockDriftError extends Error {
  readonly drift: number;
  readonly epsilon: number;
  constructor(drift: number, epsilon: number) {
    super(
      `HLC drift ${drift}ms exceeds maximum allowed physical clock drift ` +
        `(epsilon=${epsilon}ms). The physical clock appears stalled or ` +
        `jumped backwards beyond the tolerated bound.`,
    );
    this.name = 'ClockDriftError';
    this.drift = drift;
    this.epsilon = epsilon;
  }
}

/** Error thrown when the logical counter overflows its 16-bit budget. */
export class LogicalOverflowError extends Error {
  constructor() {
    super(
      'HLC logical counter overflow: more than 65535 events within a single ' +
        'physical millisecond.',
    );
    this.name = 'LogicalOverflowError';
  }
}

export interface HLCClockOptions {
  /**
   * Maximum tolerated drift between the HLC physical component and the real
   * physical clock, in milliseconds. Defaults to 500ms.
   */
  epsilon?: number;
  /** Physical clock source. Defaults to Date.now. Injectable for testing. */
  physicalClock?: () => number;
}

/** Compare two timestamps lexicographically: (wallTime, logical). */
export function compare(a: Timestamp, b: Timestamp): number {
  if (a.wallTime !== b.wallTime) return a.wallTime - b.wallTime;
  return a.logical - b.logical;
}

/** Pack a timestamp into a 64-bit bigint: 48 bits wallTime | 16 bits logical. */
export function pack(ts: Timestamp): bigint {
  return (BigInt(ts.wallTime) << LOGICAL_BITS) | BigInt(ts.logical);
}

/** Unpack a 64-bit bigint produced by {@link pack}. */
export function unpack(packed: bigint): Timestamp {
  return {
    wallTime: Number(packed >> LOGICAL_BITS),
    logical: Number(packed & ((1n << LOGICAL_BITS) - 1n)),
  };
}

export class HLCClock {
  private readonly epsilon: number;
  private readonly physicalClock: () => number;
  private lastWallTime = 0;
  private lastLogical = 0;

  constructor(options: HLCClockOptions = {}) {
    this.epsilon = options.epsilon ?? 500;
    this.physicalClock = options.physicalClock ?? Date.now;
  }

  /** The most recently issued (or merged) timestamp. */
  get latest(): Timestamp {
    return { wallTime: this.lastWallTime, logical: this.lastLogical };
  }

  /**
   * Generate the next timestamp for a local or send event.
   *
   * The physical component never exceeds max(physicalNow, previous) and the
   * logical counter keeps the sequence strictly increasing while the
   * physical clock is stalled or moves backwards. If the physical clock
   * jumps forward, the HLC follows it immediately (smooth catch-up: the
   * counter resets to 0), so timestamps never detach from physical reality.
   */
  now(): Timestamp {
    const physicalNow = this.physicalClock();
    const wallTime = Math.max(physicalNow, this.lastWallTime);
    const logical = wallTime === this.lastWallTime ? this.lastLogical + 1 : 0;
    return this.commit(wallTime, logical, physicalNow);
  }

  /**
   * Merge a timestamp received from a remote node (receive event), preserving
   * causal ordering: the returned timestamp is strictly greater than both the
   * local latest and the received one.
   */
  update(received: Timestamp): Timestamp {
    const physicalNow = this.physicalClock();
    const wallTime = Math.max(physicalNow, this.lastWallTime, received.wallTime);

    let logical: number;
    if (wallTime === this.lastWallTime && wallTime === received.wallTime) {
      logical = Math.max(this.lastLogical, received.logical) + 1;
    } else if (wallTime === this.lastWallTime) {
      logical = this.lastLogical + 1;
    } else if (wallTime === received.wallTime) {
      logical = received.logical + 1;
    } else {
      logical = 0;
    }
    return this.commit(wallTime, logical, physicalNow);
  }

  private commit(wallTime: number, logical: number, physicalNow: number): Timestamp {
    const drift = wallTime - physicalNow;
    if (drift > this.epsilon) {
      throw new ClockDriftError(drift, this.epsilon);
    }
    if (logical > MAX_LOGICAL) {
      throw new LogicalOverflowError();
    }
    this.lastWallTime = wallTime;
    this.lastLogical = logical;
    return { wallTime, logical };
  }
}
