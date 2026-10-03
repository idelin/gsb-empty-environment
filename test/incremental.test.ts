import test from 'node:test';
import assert from 'node:assert/strict';
import { WatershedEngine } from '../src/engine.ts';
import { mulberry32, shuffler, assertMatchesReference } from './helpers.ts';

const SEED_IDS = ['A', 'B', 'C', 'D', 'E'];

function randomEngine(rng: () => number, w: number, h: number, heights: Int32Array, randomize: boolean): WatershedEngine {
  return new WatershedEngine(w, h, heights, {
    tileSize: 2 + ((rng() * 6) | 0),
    randomize: randomize ? shuffler(rng) : undefined,
  });
}

function fuzzOnce(trialSeed: number, randomize: boolean): void {
  const rng = mulberry32(trialSeed);
  const w = 4 + ((rng() * 25) | 0);
  const h = 4 + ((rng() * 17) | 0);
  // Small height values create many plateaus, the hardest case for tie-breaking.
  const heights = Int32Array.from({ length: w * h }, () => (rng() * 5) | 0);
  const eng = randomEngine(rng, w, h, heights, randomize);
  assertMatchesReference(eng, 'initial');
  for (let op = 0; op < 40; op++) {
    const kind = (rng() * 4) | 0;
    const seeds = eng.getSeeds();
    const x = (rng() * w) | 0, y = (rng() * h) | 0;
    if (kind === 0 && seeds.length < SEED_IDS.length) {
      const free = SEED_IDS.filter(id => !seeds.some(s => s.id === id));
      const id = free[(rng() * free.length) | 0];
      if (seeds.some(s => s.x === x && s.y === y)) continue;
      eng.addSeed(id, x, y);
    } else if (kind === 1 && seeds.length > 0) {
      const s = seeds[(rng() * seeds.length) | 0];
      if (seeds.some(o => o.id !== s.id && o.x === x && o.y === y)) continue;
      eng.moveSeed(s.id, x, y);
    } else if (kind === 2 && seeds.length > 0) {
      eng.removeSeed(seeds[(rng() * seeds.length) | 0].id);
    } else {
      const edits = Array.from({ length: 1 + ((rng() * 3) | 0) }, () => ({
        x: (rng() * w) | 0, y: (rng() * h) | 0, h: (rng() * 5) | 0,
      }));
      eng.setHeights(edits);
    }
    assertMatchesReference(eng, `op#${op} (seed=${trialSeed})`);
  }
}

test('fuzz: incremental engine equals full-flood reference after every op', () => {
  for (let trial = 0; trial < 25; trial++) fuzzOnce(1000 + trial, false);
});

test('fuzz: randomized tile order still equals the reference', () => {
  for (let trial = 0; trial < 10; trial++) fuzzOnce(5000 + trial, true);
});

test('localized edit only recomputes the affected basins', () => {
  const w = 48, h = 32;
  const rng = mulberry32(7);
  const heights = Int32Array.from({ length: w * h }, () => (rng() * 9) | 0);
  const eng = new WatershedEngine(w, h, heights, { tileSize: 8 });
  eng.addSeed('A', 4, 4);
  eng.addSeed('B', 44, 28);
  const before = eng.version;
  eng.setHeights([{ x: 2, y: 2, h: 9 }]);
  const stats = eng.getStats(before + 1);
  assert.ok(stats.regionPixels < w * h, `expected a partial recompute, got ${stats.regionPixels}/${w * h}`);
  assert.ok(stats.pops < w * h, `expected fewer pops than pixels, got ${stats.pops}`);
  assertMatchesReference(eng, 'localized edit');
});

test('plateau influence propagates across tiles when a dam is breached', () => {
  // Two basins separated by a wall at x=8; breaching the wall lets A's front
  // flood the right basin, crossing several tile borders.
  const w = 16, h = 8;
  const heights = new Int32Array(w * h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) heights[y * w + x] = x === 8 ? 50 : 10;
  }
  const eng = new WatershedEngine(w, h, heights, { tileSize: 4 });
  const la = eng.addSeed('A', 0, 0);
  assert.ok(eng.getLabel(15, 7) === 0, 'right basin unassigned while the dam holds');
  eng.setHeights([{ x: 8, y: 4, h: 10 }]);
  const stats = eng.getStats(eng.version);
  assert.ok(stats.expansions >= 1, 'dirty region must have grown across tiles');
  assert.equal(eng.getLabel(15, 7), la, 'A flooded the right basin through the breach');
  assertMatchesReference(eng, 'dam breach');
});

test('raising a seeded pixel to a peak strands it: its old region is re-competed', () => {
  const w = 12, h = 12;
  const heights = new Int32Array(w * h).fill(4);
  const eng = new WatershedEngine(w, h, heights, { tileSize: 4 });
  const la = eng.addSeed('A', 3, 3);
  const lb = eng.addSeed('B', 9, 9);
  eng.setHeights([{ x: 3, y: 3, h: 40 }]); // seed A now sits on a peak
  // A's front starts at level 40 and can only keep its own pixel; B's level-4
  // front wins everything else. The engine must agree with the reference.
  assert.equal(eng.getLabel(3, 3), la);
  for (let p = 0; p < w * h; p++) {
    if (p !== 3 + 3 * w) assert.equal(eng.getLabels()[p], lb, `pixel ${p}`);
  }
  assertMatchesReference(eng, 'peak');
});
