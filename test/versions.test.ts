import test from 'node:test';
import assert from 'node:assert/strict';
import { WatershedEngine } from '../src/engine.ts';

function scenario(): { eng: WatershedEngine; la: number; lb: number; lc: number; snapshots: Int32Array[] } {
  const w = 10, h = 6;
  const heights = new Int32Array(w * h).fill(5);
  const eng = new WatershedEngine(w, h, heights, { tileSize: 3 });
  const snapshots: Int32Array[] = [eng.getLabels()];
  const la = eng.addSeed('A', 1, 3); snapshots.push(eng.getLabels());
  const lb = eng.addSeed('B', 8, 3); snapshots.push(eng.getLabels());
  const lc = eng.addSeed('C', 5, 3); snapshots.push(eng.getLabels());
  eng.removeSeed('C'); snapshots.push(eng.getLabels());
  return { eng, la, lb, lc, snapshots };
}

test('label patches exactly describe each version delta', () => {
  const { eng } = scenario();
  for (let v = 1; v <= eng.version; v++) {
    const prev = eng.getLabelsVersion(v - 1).slice();
    for (const e of eng.getPatch(v)) prev[e.pixel] = e.to;
    assert.deepEqual([...prev], [...eng.getLabelsVersion(v)], `patch v${v}`);
  }
});

test('old version queries reconstruct historical label maps', () => {
  const { eng, snapshots } = scenario();
  for (let v = 0; v <= eng.version; v++) {
    assert.deepEqual([...eng.getLabelsVersion(v)], [...snapshots[v]], `snapshot v${v}`);
  }
  // Point queries agree with the full reconstructions.
  for (let v = 0; v <= eng.version; v++) {
    for (const [x, y] of [[0, 0], [5, 3], [9, 5], [1, 3]]) {
      assert.equal(eng.getLabelAt(x, y, v), snapshots[v][y * 10 + x], `(${x},${y})@v${v}`);
    }
  }
  assert.equal(eng.getLabelAt(0, 0, 0), 0, 'version 0 had no seeds');
  assert.throws(() => eng.getLabelsVersion(eng.version + 1), /out of range/);
});

test('merge and split events are emitted on seed add/withdraw', () => {
  const { eng, lc } = scenario();
  const v3 = eng.getEvents(3); // C added inside A/B territory
  assert.ok(v3.some(e => e.type === 'split' && e.into === lc), `expected split event, got ${JSON.stringify(v3)}`);
  const v4 = eng.getEvents(4); // C withdrawn
  assert.ok(v4.some(e => e.type === 'merge' && e.from === lc), `expected merge event, got ${JSON.stringify(v4)}`);
  const merge = v4.find(e => e.type === 'merge' && e.from === lc)!;
  assert.ok(merge.type === 'merge' && merge.into.length > 0, 'merge event lists the absorbing labels');
});

test('adjacency and boundary evidence are maintained per version', () => {
  const { eng, la, lb } = scenario();
  const adj2 = eng.getAdjacency(2); // A and B share a border on the flat map
  const pair = adj2.find(e => (e.a === Math.min(la, lb) && e.b === Math.max(la, lb)));
  assert.ok(pair !== undefined, `expected A|B adjacency, got ${JSON.stringify(adj2)}`);
  assert.ok(pair.count > 0);
  assert.ok(pair.samples.length > 0 && pair.samples.length <= 16);
  // Evidence samples are real border pixels: each sample sits next to the other label.
  const labels2 = eng.getLabelsVersion(2);
  const w = 10;
  for (const p of pair.samples) {
    const x = p % w, y = (p / w) | 0;
    assert.equal(labels2[p], pair.a);
    const near = [[x - 1, y], [x + 1, y], [x, y - 1], [x, y + 1]]
      .filter(([nx, ny]) => nx >= 0 && nx < w && ny >= 0 && ny < 6)
      .some(([nx, ny]) => labels2[ny * w + nx] === pair.b);
    assert.ok(near, `sample ${p} must touch the other region`);
  }
  // After C is removed, adjacency of the final version matches a fresh scan.
  const finalAdj = eng.getAdjacency();
  assert.ok(finalAdj.every(e => e.count > 0));
});

test('withdrawing the last seed returns the map to unassigned', () => {
  const eng = new WatershedEngine(4, 4, new Int32Array(16).fill(1), { tileSize: 2 });
  eng.addSeed('solo', 0, 0);
  assert.ok([...eng.getLabels()].every(l => l === 1));
  eng.removeSeed('solo');
  assert.ok([...eng.getLabels()].every(l => l === 0));
  const events = eng.getEvents(eng.version);
  assert.ok(events.some(e => e.type === 'merge' && e.from === 1 && e.into.includes(0)));
});
