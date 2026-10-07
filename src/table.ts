/**
 * A complete offset transition table. The table alone defines the zone:
 * no system timezone or IANA database is consulted.
 */

export interface Transition {
  /** UTC instant (ms since Unix epoch) from which `offsetMinutes` takes effect. */
  readonly utcMs: number;
  /** Offset from UTC in minutes, in effect from `utcMs` until the next transition. */
  readonly offsetMinutes: number;
}

export interface TransitionTableInput {
  /** Offset (minutes east of UTC) in effect before the first transition. */
  readonly initialOffsetMinutes: number;
  /** Transitions, must be sorted strictly ascending by `utcMs`. */
  readonly transitions: readonly Transition[];
}

/** Maximum accepted absolute offset: 24 hours in minutes. */
export const MAX_OFFSET_MINUTES = 24 * 60;

export class InvalidTransitionTableError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'InvalidTransitionTableError';
  }
}

function assertValidOffset(offsetMinutes: number, where: string): void {
  if (!Number.isInteger(offsetMinutes)) {
    throw new InvalidTransitionTableError(`${where}: offset must be an integer number of minutes, got ${offsetMinutes}`);
  }
  if (Math.abs(offsetMinutes) > MAX_OFFSET_MINUTES) {
    throw new InvalidTransitionTableError(`${where}: |offset| must be <= ${MAX_OFFSET_MINUTES} minutes, got ${offsetMinutes}`);
  }
}

export class TransitionTable {
  readonly initialOffsetMinutes: number;
  readonly transitions: readonly Transition[];

  constructor(input: TransitionTableInput) {
    assertValidOffset(input.initialOffsetMinutes, 'initialOffsetMinutes');
    for (let i = 0; i < input.transitions.length; i += 1) {
      const t = input.transitions[i];
      if (!Number.isFinite(t.utcMs)) {
        throw new InvalidTransitionTableError(`transitions[${i}]: utcMs must be finite, got ${t.utcMs}`);
      }
      assertValidOffset(t.offsetMinutes, `transitions[${i}]`);
      if (i > 0 && t.utcMs <= input.transitions[i - 1].utcMs) {
        throw new InvalidTransitionTableError(
          `transitions[${i}]: utcMs must be strictly ascending (${t.utcMs} <= ${input.transitions[i - 1].utcMs})`,
        );
      }
    }
    this.initialOffsetMinutes = input.initialOffsetMinutes;
    this.transitions = input.transitions.slice();
  }

  /** Number of constant-offset periods (transitions + 1). */
  get periodCount(): number {
    return this.transitions.length + 1;
  }

  /** Inclusive UTC start of period `i` (`-Infinity` for the first). */
  periodStart(i: number): number {
    return i === 0 ? -Infinity : this.transitions[i - 1].utcMs;
  }

  /** Exclusive UTC end of period `i` (`+Infinity` for the last). */
  periodEnd(i: number): number {
    return i < this.transitions.length ? this.transitions[i].utcMs : Infinity;
  }

  /** Offset (minutes) of period `i`. */
  periodOffset(i: number): number {
    return i === 0 ? this.initialOffsetMinutes : this.transitions[i - 1].offsetMinutes;
  }

  /** Index of the period containing UTC instant `utcMs`. */
  periodIndexAtUtc(utcMs: number): number {
    let lo = 0;
    let hi = this.transitions.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.transitions[mid].utcMs <= utcMs) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }

  /** Offset (minutes east of UTC) in effect at UTC instant `utcMs`. */
  offsetAtUtc(utcMs: number): number {
    return this.periodOffset(this.periodIndexAtUtc(utcMs));
  }
}
