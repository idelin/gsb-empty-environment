/**
 * Revision data flow with per-column barriers.
 *
 * Each series publishes revisions as generation-stamped batches of point
 * updates; the series' state at generation g is the cumulative merge of
 * batches 1..g, later generations overriding per point. Batches may
 * arrive out of order, across and within series; gaps are buffered until
 * they fill.
 *
 * Barrier rule: the barrier G is the minimum contiguous generation
 * delivered by every required series. A time column is published at
 * generation G only when the barrier reaches G, and the published column
 * uses, for every required series, that series' latest snapshot at or
 * below G. A column therefore never mixes generations: no "half column"
 * of newer data can leak into a published (percent) baseline.
 *
 * Retracting a series drops it from the required set, recomputes every
 * affected column's layer ranges, and emits alternative versions that
 * supersede the previous publications. Hiding keeps a series required
 * (it still gates the barrier) but excludes it from stacking; unhiding
 * recomputes and emits alternatives as well.
 */

import { InputPoint, StackCell, StackMode, stackColumn } from './stack';

export interface SeriesBatch {
  seriesId: string;
  generation: number;
  points: ReadonlyArray<InputPoint>;
}

export interface ColumnPublication {
  t: number;
  generation: number;
  version: number;
  alternative: boolean;
  /** One cell per roster series, in roster order; hidden series get null bounds. */
  cells: StackCell[];
}

export class RevisionEngine {
  private readonly roster: string[];
  private readonly mode: StackMode;
  private readonly required = new Set<string>();
  private readonly hidden = new Set<string>();
  /** series -> generation -> (t -> value) */
  private readonly snapshots = new Map<string, Map<number, Map<number, number>>>();
  /** series -> contiguous delivered generation watermark */
  private readonly delivered = new Map<string, number>();
  /** series -> buffered out-of-order batches by generation */
  private readonly pending = new Map<string, Map<number, SeriesBatch>>();
  private barrier = 0;
  private readonly publishedGen = new Map<number, number>();
  private readonly colVersion = new Map<number, number>();
  private readonly lastCells = new Map<number, StackCell[]>();

  constructor(seriesIds: ReadonlyArray<string>, mode: StackMode) {
    this.roster = seriesIds.slice();
    this.mode = mode;
    for (const id of this.roster) {
      this.required.add(id);
      this.snapshots.set(id, new Map());
      this.delivered.set(id, 0);
      this.pending.set(id, new Map());
    }
  }

  getBarrier(): number {
    return this.barrier;
  }

  isRequired(id: string): boolean {
    return this.required.has(id);
  }

  /** Latest publication per column, for state comparison. */
  currentColumns(): Map<number, ColumnPublication> {
    const out = new Map<number, ColumnPublication>();
    for (const [t, gen] of this.publishedGen) {
      out.set(t, {
        t,
        generation: gen,
        version: this.colVersion.get(t) as number,
        alternative: false,
        cells: this.lastCells.get(t) as StackCell[],
      });
    }
    return out;
  }

  apply(batch: SeriesBatch): ColumnPublication[] {
    const id = batch.seriesId;
    if (!this.required.has(id)) {
      throw new Error(`unknown or retracted series "${id}"`);
    }
    if (!Number.isInteger(batch.generation) || batch.generation < 1) {
      throw new Error(`invalid generation ${batch.generation}`);
    }
    const delivered = this.delivered.get(id) as number;
    if (batch.generation <= delivered) {
      throw new Error(`generation ${batch.generation} already delivered for series "${id}"`);
    }
    const pend = this.pending.get(id) as Map<number, SeriesBatch>;
    if (pend.has(batch.generation)) {
      throw new Error(`duplicate buffered generation ${batch.generation} for series "${id}"`);
    }
    pend.set(batch.generation, batch);

    // Advance the contiguous watermark, draining buffered generations.
    let next = delivered;
    const snaps = this.snapshots.get(id) as Map<number, Map<number, number>>;
    while (pend.has(next + 1)) {
      next += 1;
      const b = pend.get(next) as SeriesBatch;
      pend.delete(next);
      const snap = new Map<number, number>();
      for (const p of b.points) {
        if (snap.has(p.t)) {
          throw new Error(`series "${id}" generation ${next}: duplicate point at t=${p.t}`);
        }
        snap.set(p.t, p.v);
      }
      snaps.set(next, snap);
    }
    this.delivered.set(id, next);

    this.recomputeBarrier();
    return this.publishBarrierColumns();
  }

  /** Removes a series from the required set and drops its data. */
  retractSeries(id: string): ColumnPublication[] {
    if (!this.required.has(id)) {
      throw new Error(`series "${id}" is not required`);
    }
    this.required.delete(id);
    this.hidden.delete(id);
    this.snapshots.set(id, new Map());
    (this.pending.get(id) as Map<number, SeriesBatch>).clear();
    this.delivered.set(id, 0);
    this.recomputeBarrier();
    const pubs = this.publishBarrierColumns();
    const all = pubs.concat(this.emitAlternatives());
    // Every publication following a roster change supersedes prior output.
    for (const p of all) p.alternative = true;
    return all;
  }

  setHidden(id: string, hidden: boolean): ColumnPublication[] {
    if (!this.required.has(id)) {
      throw new Error(`series "${id}" is not required`);
    }
    const was = this.hidden.has(id);
    if (was === hidden) return [];
    if (hidden) this.hidden.add(id);
    else this.hidden.delete(id);
    return this.emitAlternatives();
  }

  private recomputeBarrier(): void {
    let min = Infinity;
    for (const id of this.required) {
      const d = this.delivered.get(id) as number;
      if (d < min) min = d;
    }
    this.barrier = this.required.size === 0 ? 0 : min;
  }

  /** Cumulative state of `id` at generation `g`: batches 1..g merged in
   * generation order, later generations overriding per point. */
  private snapshotAt(id: string, g: number): Map<number, number> | null {
    const snaps = this.snapshots.get(id) as Map<number, Map<number, number>>;
    const gens = Array.from(snaps.keys()).filter((x) => x <= g).sort((a, b) => a - b);
    if (gens.length === 0) return null;
    const out = new Map<number, number>();
    for (const gen of gens) {
      const snap = snaps.get(gen) as Map<number, number>;
      for (const [t, v] of snap) out.set(t, v);
    }
    return out;
  }

  private columnCells(t: number, g: number): { cells: StackCell[]; anyData: boolean } {
    const values: Array<number | null> = [];
    let anyData = false;
    for (const id of this.roster) {
      if (!this.required.has(id) || this.hidden.has(id)) {
        values.push(null);
        continue;
      }
      const snap = this.snapshotAt(id, g);
      const v = snap !== null && snap.has(t) ? (snap.get(t) as number) : null;
      if (v !== null) anyData = true;
      values.push(v);
    }
    const cells = stackColumn(values, this.mode);
    // Hidden (but required) series keep a roster slot with null bounds.
    for (let i = 0; i < this.roster.length; i++) {
      const id = this.roster[i];
      if (this.required.has(id) && this.hidden.has(id)) {
        cells[i] = { lower: null, upper: null, missing: true, zero: false, value: null };
      }
    }
    return { cells, anyData };
  }

  /** Publishes columns whose data is newly complete at the current barrier. */
  private publishBarrierColumns(): ColumnPublication[] {
    const g = this.barrier;
    if (g === 0) return [];
    const times = new Set<number>();
    for (const id of this.required) {
      const snaps = this.snapshots.get(id) as Map<number, Map<number, number>>;
      for (const [gen, snap] of snaps) {
        if (gen > g) continue;
        for (const t of snap.keys()) times.add(t);
      }
    }
    const pubs: ColumnPublication[] = [];
    const sorted = Array.from(times).sort((a, b) => a - b);
    for (const t of sorted) {
      const prev = this.publishedGen.get(t);
      if (prev !== undefined && prev >= g) continue;
      const { cells, anyData } = this.columnCells(t, g);
      if (!anyData && prev === undefined) continue;
      pubs.push(this.record(t, g, cells, false));
    }
    return pubs;
  }

  /** Re-emits columns whose stacked ranges changed after a roster change. */
  private emitAlternatives(): ColumnPublication[] {
    const pubs: ColumnPublication[] = [];
    const g = this.barrier;
    const allTimes = new Set<number>(this.publishedGen.keys());
    for (const id of this.required) {
      const snaps = this.snapshots.get(id) as Map<number, Map<number, number>>;
      for (const [gen, snap] of snaps) {
        if (gen > g) continue;
        for (const t of snap.keys()) allTimes.add(t);
      }
    }
    const sorted = Array.from(allTimes).sort((a, b) => a - b);
    for (const t of sorted) {
      const effectiveGen = Math.max(g, this.publishedGen.get(t) ?? 0);
      if (effectiveGen === 0) continue;
      const { cells, anyData } = this.columnCells(t, effectiveGen);
      if (!anyData && !this.publishedGen.has(t)) continue;
      const prev = this.lastCells.get(t);
      if (prev !== undefined && cellsEqual(prev, cells)) continue;
      pubs.push(this.record(t, effectiveGen, cells, true));
    }
    return pubs;
  }

  private record(t: number, generation: number, cells: StackCell[], alternative: boolean): ColumnPublication {
    const version = (this.colVersion.get(t) ?? 0) + 1;
    this.colVersion.set(t, version);
    this.publishedGen.set(t, generation);
    this.lastCells.set(t, cells);
    return { t, generation, version, alternative, cells };
  }
}

export function cellsEqual(a: ReadonlyArray<StackCell>, b: ReadonlyArray<StackCell>): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const x = a[i];
    const y = b[i];
    if (
      x.missing !== y.missing ||
      x.zero !== y.zero ||
      x.lower !== y.lower ||
      x.upper !== y.upper ||
      x.value !== y.value
    ) {
      return false;
    }
  }
  return true;
}
