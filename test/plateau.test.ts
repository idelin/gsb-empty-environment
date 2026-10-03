import test from 'node:test';
import assert from 'node:assert/strict';
import { WatershedEngine } from '../src/engine.ts';
import { segmentReference } from '../src/reference.ts';
import { mulberry32, shuffler } from './helpers.ts';

test('flat plateau: tie at equal distance is won by smaller seed id, not scan order', () => {
  const w = 7, h = 7;
  const heights = new Int32Array(w * h).fill(5);
  const eng = new WatershedEngine(w, h, heights, { tileSize: 2 });
  eng.addSeed('A', 0, 3);
  eng.addSeed('B', 6, 3);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const label = eng.getLabel(x, y);
      if (x <= 3) assert.equal(label, 1, `x=${x} should belong to A`); // x=3 is equidistant: A wins by id
      else assert.equal(label, 2, `x=${x} should belong to B`);
    }
  }
});

test('seed insertion order does not change the result', () => {
  const w = 7, h = 7;
  const mk = () => new WatershedEngine(w, h, new Int32Array(w * h).fill(5), { tileSize: 3 });
  const e1 = mk(); e1.addSeed('A', 0, 3); e1.addSeed('B', 6, 3);
  const e2 = mk(); e2.addSeed('B', 6, 3); e2.addSeed('A', 0, 3);
  // Label numbers follow insertion order; the partition by seed id must not.
  const byId = (eng: WatershedEngine): string[] => {
    const idOf = new Map(eng.getSeeds().map(s => [s.label, s.id] as const));
    return [...eng.getLabels()].map(l => idOf.get(l) ?? '-');
  };
  assert.deepEqual(byId(e1), byId(e2));
});

test('random tile processing order yields identical labels', () => {
  const w = 7, h = 7;
  const heights = new Int32Array(w * h).fill(5);
  const plain = new WatershedEngine(w, h, heights, { tileSize: 2 });
  plain.addSeed('A', 0, 3); plain.addSeed('B', 6, 3);
  const shuffled = new WatershedEngine(w, h, heights, { tileSize: 2, randomize: shuffler(mulberry32(99)) });
  shuffled.addSeed('A', 0, 3); shuffled.addSeed('B', 6, 3);
  assert.deepEqual([...shuffled.getLabels()], [...plain.getLabels()]);
});

test('withdrawing a bridging seed forces re-competition of its region', () => {
  const w = 9, h = 1;
  const heights = new Int32Array(w * h).fill(0);
  const eng = new WatershedEngine(w, h, heights, { tileSize: 2 });
  eng.addSeed('A', 0, 0);
  eng.addSeed('B', 4, 0);
  eng.addSeed('C', 8, 0);
  const la = eng.getSeeds().find(s => s.id === 'A')!.label;
  const lb = eng.getSeeds().find(s => s.id === 'B')!.label;
  const lc = eng.getSeeds().find(s => s.id === 'C')!.label;
  assert.deepEqual([...eng.getLabels()], [la, la, la, lb, lb, lb, lb, lc, lc]);
  eng.removeSeed('B');
  // A and C split the freed region; the midpoint tie goes to the smaller id.
  assert.deepEqual([...eng.getLabels()], [la, la, la, la, la, lc, lc, lc, lc]);
  const ref = segmentReference(w, h, heights, eng.getSeeds());
  assert.deepEqual([...eng.getLabels()], [...ref.labels]);
});

test('engine equals reference on plateaus regardless of tile size', () => {
  const w = 13, h = 9;
  const heights = new Int32Array(w * h).fill(7);
  for (const tileSize of [1, 2, 3, 4, 13]) {
    const eng = new WatershedEngine(w, h, heights, { tileSize });
    eng.addSeed('X', 2, 2);
    eng.addSeed('Y', 10, 6);
    eng.addSeed('Z', 6, 4);
    const ref = segmentReference(w, h, heights, eng.getSeeds());
    assert.deepEqual([...eng.getLabels()], [...ref.labels], `tileSize=${tileSize}`);
  }
});
