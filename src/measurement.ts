import type { Rational } from "./rational.ts";
import { interval, unboundedInterval } from "./interval.ts";
import type { Interval } from "./interval.ts";

/**
 * A measurement: a central value plus an absolute error bound.
 *
 * `error === null` means the error is UNKNOWN (unbounded). Unknown error is
 * never treated as 0: any classification that would need the bound becomes
 * "needs-review" instead of "pass".
 */
export interface Measurement {
  readonly id: string;
  readonly value: Rational;
  readonly error: Rational | null;
}

export interface MeasurementChange {
  readonly id: string;
  readonly version: number;
  readonly epoch: number;
}

type Listener = (change: MeasurementChange) => void;

interface Entry {
  measurement: Measurement;
  version: number;
}

/**
 * Versioned measurement registry. Every `set` bumps the measurement version
 * and the global epoch, and notifies listeners (used by the evidence store
 * to incrementally revoke stale judgments).
 */
export class MeasurementStore {
  private entries = new Map<string, Entry>();
  private epochCounter = 0;
  private listeners: Listener[] = [];

  set(id: string, value: Rational, error: Rational | null): Measurement {
    if (error !== null && error.sign() < 0) {
      throw new Error(`measurement ${id}: negative error bound`);
    }
    const prev = this.entries.get(id);
    const version = (prev?.version ?? 0) + 1;
    const measurement: Measurement = { id, value, error };
    this.entries.set(id, { measurement, version });
    this.epochCounter += 1;
    const change: MeasurementChange = { id, version, epoch: this.epochCounter };
    for (const fn of this.listeners) fn(change);
    return measurement;
  }

  get(id: string): Measurement {
    const e = this.entries.get(id);
    if (!e) throw new Error(`unknown measurement: ${id}`);
    return e.measurement;
  }

  has(id: string): boolean {
    return this.entries.has(id);
  }

  version(id: string): number {
    const e = this.entries.get(id);
    if (!e) throw new Error(`unknown measurement: ${id}`);
    return e.version;
  }

  /** Snapshot of all measurement versions (used as evidence dependencies). */
  versions(): Map<string, number> {
    const out = new Map<string, number>();
    for (const [id, e] of this.entries) out.set(id, e.version);
    return out;
  }

  ids(): string[] {
    return [...this.entries.keys()];
  }

  epoch(): number {
    return this.epochCounter;
  }

  onChange(fn: Listener): void {
    this.listeners.push(fn);
  }

  /** Value interval of a measurement; unknown error -> unbounded. */
  intervalOf(id: string): Interval {
    const m = this.get(id);
    if (m.error === null) return unboundedInterval();
    return interval(m.value.sub(m.error), m.value.add(m.error));
  }
}
