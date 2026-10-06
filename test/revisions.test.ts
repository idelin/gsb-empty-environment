import { RevisionEngine, SeriesBatch } from '../src/revisions';
import { StackCell, StackMode } from '../src/stack';
import { buildMatrix, stackMatrix } from '../src/matrix';
import { closeTo, eq, ok, rng, test } from './harness';

interface Tracked {
  roster: string[];
  required: Set<string>;
  hidden: Set<string>;
  /** series -> generation -> snapshot */
  snaps: Map<string, Map<number, Map<number, number>>>;
  delivered: Map<string, number>;
}

function newTracked(roster: string[]): Tracked {
  return {
    roster: roster.slice(),
    required: new Set(roster),
    hidden: new Set(),
    snaps: new Map(roster.map((id) => [id, new Map()] as [string, Map<number, Map<number, number>>])),
    delivered: new Map(roster.map((id) => [id, 0] as [string, number])),
  };
}

function trackApply(tr: Tracked, batch: SeriesBatch): void {
  const snaps = tr.snaps.get(batch.seriesId) as Map<number, Map<number, number>>;
  const snap = new Map<number, number>();
  for (const p of batch.points) snap.set(p.t, p.v);
  snaps.set(batch.generation, snap);
  // Mirror the engine's contiguous watermark: delivered = longest prefix
  // 1..g with a snapshot present.
  let g = 0;
  while (snaps.has(g + 1)) g += 1;
  tr.delivered.set(batch.seriesId, g);
}

function mergedSnapshot(tr: Tracked, id: string, g: number): Map<number, number> {
  const out = new Map<number, number>();
  const snaps = tr.snaps.get(id) as Map<number, Map<number, number>>;
  const gens = Array.from(snaps.keys()).filter((x) => x <= g).sort((a, b) => a - b);
  for (const gen of gens) {
    const snap = snaps.get(gen) as Map<number, number>;
    for (const [t, v] of snap) out.set(t, v);
  }
  return out;
}

function expectedBarrier(tr: Tracked): number {
  let min = Infinity;
  for (const id of tr.required) min = Math.min(min, tr.delivered.get(id) as number);
  return tr.required.size === 0 ? 0 : min;
}

/**
 * Full-matrix ground truth: rebuild the complete series x time matrix at
 * the current barrier from scratch and compare every published column.
 */
function checkAgainstMatrixModel(engine: RevisionEngine, tr: Tracked, mode: StackMode): void {
  const barrier = expectedBarrier(tr);
  eq(engine.getBarrier(), barrier, 'barrier matches min delivered generation');

  // Time axis: union over required (including hidden) snapshots <= barrier.
  const axisData = tr.roster.map((id) =>
    tr.required.has(id) ? mergedSnapshot(tr, id, barrier) : new Map<number, number>()
  );
  const { times } = buildMatrix(axisData);

  // Stacking rows: hidden and retracted series contribute nothing.
  const stackData = tr.roster.map((id) =>
    tr.required.has(id) && !tr.hidden.has(id) ? mergedSnapshot(tr, id, barrier) : new Map<number, number>()
  );
  const built = buildMatrix(stackData);
  const bySeries = stackMatrix(built.matrix, mode);

  const expectedAt = (t: number): StackCell[] => {
    const col = built.times.indexOf(t);
    return bySeries.map((row) => row[col]);
  };

  const cols = engine.currentColumns();
  for (const [t, pub] of cols) {
    eq(pub.generation, barrier, `column ${t} published at current barrier`);
    eq(pub.cells, expectedAt(t), `column ${t} cells match matrix model`);
  }
  // Every column with visible data must have been published.
  for (const t of times) {
    const cells = expectedAt(t);
    const anyVisible = cells.some((c) => !c.missing);
    if (anyVisible) ok(cols.has(t), `column ${t} published`);
  }
}

function batch(id: string, generation: number, pts: Array<[number, number]>): SeriesBatch {
  return { seriesId: id, generation, points: pts.map(([t, v]) => ({ t, v })) };
}

test('barrier: no publication until every required series reaches the generation', () => {
  const tr = newTracked(['a', 'b']);
  const e = new RevisionEngine(['a', 'b'], 'percent');

  let pubs = e.apply(batch('a', 1, [[1, 2], [2, 4]]));
  trackApply(tr, batch('a', 1, [[1, 2], [2, 4]]));
  eq(pubs.length, 0, 'nothing published while b is behind');
  eq(e.getBarrier(), 0, 'barrier still 0');

  pubs = e.apply(batch('b', 1, [[1, -1], [2, 4]]));
  trackApply(tr, batch('b', 1, [[1, -1], [2, 4]]));
  eq(e.getBarrier(), 1, 'barrier advances once both deliver gen 1');
  eq(pubs.map((p) => p.t), [1, 2], 'both columns published');
  ok(pubs.every((p) => p.generation === 1 && p.version === 1 && !p.alternative), 'first publications');
  checkAgainstMatrixModel(e, tr, 'percent');
});

test('barrier: a column never mixes generations (no half-column new data)', () => {
  const tr = newTracked(['a', 'b']);
  const e = new RevisionEngine(['a', 'b'], 'percent');

  e.apply(batch('a', 1, [[1, 1]]));
  trackApply(tr, batch('a', 1, [[1, 1]]));
  e.apply(batch('b', 1, [[1, 3]]));
  trackApply(tr, batch('b', 1, [[1, 3]]));
  let col = e.currentColumns().get(1);
  closeTo((col as NonNullable<typeof col>).cells[0].upper as number, 0.25, 'gen1 percent baseline for a');

  // Series a revises column 1 at generation 2; b has not. The published
  // percent baseline must keep using generation-1 data for BOTH series.
  const pubs = e.apply(batch('a', 2, [[1, 9]]));
  trackApply(tr, batch('a', 2, [[1, 9]]));
  eq(pubs.length, 0, 'no republication while b lags');
  col = e.currentColumns().get(1);
  eq((col as NonNullable<typeof col>).generation, 1, 'column still at generation 1');
  closeTo((col as NonNullable<typeof col>).cells[0].upper as number, 0.25, 'no half-column leak');

  const pubs2 = e.apply(batch('b', 2, [[1, 1]]));
  trackApply(tr, batch('b', 2, [[1, 1]]));
  eq(pubs2.length, 1, 'column republished at the new barrier');
  closeTo(pubs2[0].cells[0].upper as number, 0.9, 'gen2 baseline uses both gen2 snapshots');
  closeTo(pubs2[0].cells[1].upper as number, 1.0, 'b share at gen2');
  checkAgainstMatrixModel(e, tr, 'percent');
});

test('out-of-order batches within a series are buffered', () => {
  const tr = newTracked(['a', 'b']);
  const e = new RevisionEngine(['a', 'b'], 'absolute');

  e.apply(batch('a', 2, [[1, 10]]));
  eq(e.getBarrier(), 0, 'gen 2 alone cannot advance anything');
  e.apply(batch('a', 1, [[1, 1]]));
  trackApply(tr, batch('a', 1, [[1, 1]]));
  trackApply(tr, batch('a', 2, [[1, 10]]));
  eq(e.getBarrier(), 0, 'buffered gen 2 drains but b still gates');
  const pubs = e.apply(batch('b', 1, [[1, 2]]));
  trackApply(tr, batch('b', 1, [[1, 2]]));
  eq(e.getBarrier(), 1, 'barrier at min delivered');
  eq(pubs.length, 1, 'column published once');
  checkAgainstMatrixModel(e, tr, 'absolute');
});

test('retraction recomputes ranges and emits alternative versions', () => {
  const tr = newTracked(['a', 'b', 'c']);
  const e = new RevisionEngine(['a', 'b', 'c'], 'absolute');
  const g1: Array<[string, Array<[number, number]>]> = [
    ['a', [[1, 2], [2, 1]]],
    ['b', [[1, -3], [2, 5]]],
    ['c', [[1, 4], [3, 7]]],
  ];
  for (const [id, pts] of g1) {
    e.apply(batch(id, 1, pts));
    trackApply(tr, batch(id, 1, pts));
  }
  const before = e.currentColumns();
  eq(before.size, 3, 'three columns published');

  const pubs = e.retractSeries('b');
  tr.required.delete('b');
  ok(pubs.length > 0, 'alternatives emitted');
  ok(pubs.every((p) => p.alternative), 'all flagged alternative');
  ok(pubs.every((p) => p.version === 2), 'versions bumped');

  const after = e.currentColumns();
  const a1 = (after.get(1) as NonNullable<ReturnType<typeof after.get>>).cells[0];
  eq([a1.lower, a1.upper], [0, 2], 'a keeps its slice without b');
  const c1 = (after.get(1) as NonNullable<ReturnType<typeof after.get>>).cells[2];
  eq([c1.lower, c1.upper], [2, 6], 'c re-stacked directly above a');
  const b1 = (after.get(1) as NonNullable<ReturnType<typeof after.get>>).cells[1];
  ok(b1.missing, 'retracted series slot is inert');
  checkAgainstMatrixModel(e, tr, 'absolute');
});

test('retraction can release the barrier and unlock pending generations', () => {
  const tr = newTracked(['a', 'b']);
  const e = new RevisionEngine(['a', 'b'], 'percent');
  e.apply(batch('a', 1, [[1, 1]]));
  e.apply(batch('a', 2, [[1, 5]]));
  trackApply(tr, batch('a', 1, [[1, 1]]));
  trackApply(tr, batch('a', 2, [[1, 5]]));
  e.apply(batch('b', 1, [[1, 1]]));
  trackApply(tr, batch('b', 1, [[1, 1]]));
  eq(e.getBarrier(), 1, 'b lags at gen 1');

  const pubs = e.retractSeries('b');
  tr.required.delete('b');
  eq(e.getBarrier(), 2, 'barrier released to a gen 2');
  ok(pubs.some((p) => p.alternative && p.generation === 2), 'alternative at gen 2');
  checkAgainstMatrixModel(e, tr, 'percent');
});

test('hide and recover: alternatives match the matrix model', () => {
  const tr = newTracked(['a', 'b']);
  const e = new RevisionEngine(['a', 'b'], 'percent');
  e.apply(batch('a', 1, [[1, 2], [2, 0]]));
  trackApply(tr, batch('a', 1, [[1, 2], [2, 0]]));
  e.apply(batch('b', 1, [[1, -2], [2, 4]]));
  trackApply(tr, batch('b', 1, [[1, -2], [2, 4]]));
  const original = e.currentColumns().get(1);
  const originalCells = JSON.stringify((original as NonNullable<typeof original>).cells);

  const hidePubs = e.setHidden('b', true);
  tr.hidden.add('b');
  ok(hidePubs.length > 0 && hidePubs.every((p) => p.alternative), 'hide emits alternatives');
  const hidden = e.currentColumns().get(1);
  const aHidden = (hidden as NonNullable<typeof hidden>).cells[0];
  closeTo(aHidden.upper as number, 1, 'a alone spans the full positive range');
  checkAgainstMatrixModel(e, tr, 'percent');

  const showPubs = e.setHidden('b', false);
  tr.hidden.delete('b');
  ok(showPubs.length > 0 && showPubs.every((p) => p.alternative), 'unhide emits alternatives');
  const restored = e.currentColumns().get(1);
  eq(JSON.stringify((restored as NonNullable<typeof restored>).cells), originalCells, 'recovery restores original layers');
  checkAgainstMatrixModel(e, tr, 'percent');
});

test('randomized: out-of-order revisions, retractions, hide/recover vs matrix model', () => {
  for (const mode of ['absolute', 'percent'] as const) {
    for (let seed = 1; seed <= 12; seed++) {
      const rand = rng(seed * 7919);
      const roster = ['s0', 's1', 's2', 's3'];
      const tr = newTracked(roster);
      const e = new RevisionEngine(roster, mode);

      // Pre-generate 3 generations of random snapshots per series.
      const gens: SeriesBatch[] = [];
      for (const id of roster) {
        for (let g = 1; g <= 3; g++) {
          const pts: Array<[number, number]> = [];
          for (let t = 0; t < 6; t++) {
            if (rand() < 0.25) continue; // missing
            const v = rand() < 0.2 ? 0 : Math.round((rand() - 0.5) * 200) / 10;
            pts.push([t, v]);
          }
          gens.push(batch(id, g, pts));
        }
      }
      // Shuffle delivery order (within-series gaps exercise the buffer).
      for (let i = gens.length - 1; i > 0; i--) {
        const j = Math.floor(rand() * (i + 1));
        const tmp = gens[i];
        gens[i] = gens[j];
        gens[j] = tmp;
      }

      const live = new Set(roster);
      for (const b of gens) {
        if (!live.has(b.seriesId)) continue;
        // Random roster events between batches.
        if (rand() < 0.15 && live.size > 2) {
          const victims = Array.from(live);
          const victim = victims[Math.floor(rand() * victims.length)];
          e.retractSeries(victim);
          tr.required.delete(victim);
          live.delete(victim);
        } else if (rand() < 0.2) {
          const id = roster[Math.floor(rand() * roster.length)];
          if (tr.required.has(id)) {
            const hide = rand() < 0.5;
            e.setHidden(id, hide);
          if (hide) tr.hidden.add(id);
            else tr.hidden.delete(id);
          }
        }
        if (!live.has(b.seriesId)) continue; // retraction may hit this series
        e.apply(b);
        trackApply(tr, b);
        checkAgainstMatrixModel(e, tr, mode);
      }
    }
  }
});
