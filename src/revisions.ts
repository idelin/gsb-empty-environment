import { compareTimes, stackSeries } from './stack.ts';
import type {
  DataPoint,
  SeriesInput,
  StackMode,
  StackResult,
  TimeKey,
} from './types.ts';

export interface BatchRecord {
  seriesId: string;
  revision: number;
  points: readonly DataPoint[];
}

export interface PublishedStack {
  /** Monotonic publication version; every publish (incl. replacements) bumps it. */
  version: number;
  mode: StackMode;
  /** Column time -> barrier revision the column was published at. */
  barriers: Map<TimeKey, number>;
  /** Columns withheld because not every required series has reported them. */
  pendingTimes: TimeKey[];
  result: StackResult;
}

interface SeriesState {
  /** Batches kept sorted by ascending revision; revisions are unique per series. */
  batches: BatchRecord[];
}

/**
 * Revisioned dataflow over the stacking core.
 *
 * Each required series publishes data in revisioned batches. Batches from
 * different series may arrive in any order; revisions within one series must
 * be unique. A time column is only published once every required (and not
 * hidden) series has reported that time, and it is published at the barrier
 * revision: the minimum, across those series, of the latest revision
 * covering the column. Values are read as-of the barrier, so a column never
 * mixes one series' newer data with another's older data.
 *
 * Retracting a series removes it from the required set, recomputes every
 * layer, and emits a replacement publication with a fresh version.
 */
export class RevisionStacker {
  private readonly mode: StackMode;
  private readonly required = new Set<string>();
  private readonly hidden = new Set<string>();
  private readonly retracted = new Set<string>();
  private readonly states = new Map<string, SeriesState>();
  private version = 0;

  constructor(mode: StackMode, requiredSeries: readonly string[]) {
    this.mode = mode;
    for (const id of requiredSeries) {
      this.required.add(id);
      this.states.set(id, { batches: [] });
    }
  }

  applyBatch(seriesId: string, revision: number, points: readonly DataPoint[]): void {
    if (this.retracted.has(seriesId)) {
      throw new Error(`series "${seriesId}" has been retracted`);
    }
    const state = this.states.get(seriesId);
    if (!state) {
      throw new Error(`series "${seriesId}" is not a required series`);
    }
    if (state.batches.some((b) => b.revision === revision)) {
      throw new Error(`duplicate revision ${revision} for series "${seriesId}"`);
    }
    const record: BatchRecord = { seriesId, revision, points };
    const index = state.batches.findIndex((b) => b.revision > revision);
    if (index === -1) state.batches.push(record);
    else state.batches.splice(index, 0, record);
  }

  hideSeries(seriesId: string): void {
    this.requireActive(seriesId);
    this.hidden.add(seriesId);
  }

  unhideSeries(seriesId: string): void {
    this.requireActive(seriesId);
    this.hidden.delete(seriesId);
  }

  /**
   * Removes the series, recomputes all layers and returns the replacement
   * publication. The series can never contribute again.
   */
  retractSeries(seriesId: string): PublishedStack {
    this.requireActive(seriesId);
    this.required.delete(seriesId);
    this.hidden.delete(seriesId);
    this.retracted.add(seriesId);
    this.states.delete(seriesId);
    return this.publish();
  }

  private requireActive(seriesId: string): void {
    if (this.retracted.has(seriesId)) {
      throw new Error(`series "${seriesId}" has been retracted`);
    }
    if (!this.required.has(seriesId)) {
      throw new Error(`unknown series "${seriesId}"`);
    }
  }

  private requiredVisible(): string[] {
    return [...this.required].filter((id) => !this.hidden.has(id));
  }

  /** Latest revision of the series that covers `time`, or -1 if none does. */
  private revisionAt(seriesId: string, time: TimeKey): number {
    const state = this.states.get(seriesId);
    if (!state) return -1;
    for (let i = state.batches.length - 1; i >= 0; i -= 1) {
      const batch = state.batches[i];
      if (batch.points.some((p) => p.time === time)) return batch.revision;
    }
    return -1;
  }

  /** Value reported by the newest batch at or below `barrier` covering `time`. */
  private valueAsOf(
    seriesId: string,
    time: TimeKey,
    barrier: number,
  ): number | null | undefined {
    const state = this.states.get(seriesId);
    if (!state) return undefined;
    for (let i = state.batches.length - 1; i >= 0; i -= 1) {
      const batch = state.batches[i];
      if (batch.revision > barrier) continue;
      const point = batch.points.find((p) => p.time === time);
      if (point) return point.value;
    }
    return undefined;
  }

  publish(): PublishedStack {
    const ids = this.requiredVisible();

    const timeSet = new Set<TimeKey>();
    for (const id of ids) {
      for (const batch of this.states.get(id)?.batches ?? []) {
        for (const point of batch.points) timeSet.add(point.time);
      }
    }
    const times = [...timeSet].sort(compareTimes);

    const barriers = new Map<TimeKey, number>();
    const pendingTimes: TimeKey[] = [];
    const asOfValues = new Map<string, Map<TimeKey, number | null>>();

    for (const time of times) {
      let barrier = Number.POSITIVE_INFINITY;
      for (const id of ids) {
        const revision = this.revisionAt(id, time);
        if (revision < 0) {
          barrier = -1;
          break;
        }
        barrier = Math.min(barrier, revision);
      }
      if (barrier < 0) {
        pendingTimes.push(time);
        continue;
      }
      barriers.set(time, barrier);
      for (const id of ids) {
        const raw = this.valueAsOf(id, time, barrier);
        let byTime = asOfValues.get(id);
        if (!byTime) {
          byTime = new Map();
          asOfValues.set(id, byTime);
        }
        byTime.set(time, raw === undefined ? null : raw);
      }
    }

    const seriesInputs: SeriesInput[] = ids.map((id) => ({
      id,
      points: [...(asOfValues.get(id)?.entries() ?? [])].map(([time, value]) => ({
        time,
        value,
      })),
    }));

    this.version += 1;
    return {
      version: this.version,
      mode: this.mode,
      barriers,
      pendingTimes,
      result: stackSeries(seriesInputs, { mode: this.mode }),
    };
  }
}
