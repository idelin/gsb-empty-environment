import { test } from 'node:test';
import assert from 'node:assert/strict';
import { GrayImage, RgbaImage } from '../src/image.ts';
import { CROSS, LINE3_RIGHT, SQUARE3, makeSE } from '../src/se.ts';
import { Engine, compileGraph } from '../src/graph.ts';
import type { StageDef } from '../src/graph.ts';
import { runReference } from '../src/reference.ts';

const W = 40;
const H = 5;
const ROW = 2;
const GAP = 30;

function channelSources(): { mask: GrayImage; marker: GrayImage } {
  const mask = new GrayImage(W, H);
  for (let x = 0; x < W; x++) {
    if (x !== GAP) mask.set(x, ROW, 255);
  }
  const marker = new GrayImage(W, H);
  marker.set(W - 1, ROW, 255);
  return { mask, marker };
}

const channelStages: StageDef[] = [
  {
    kind: 'reconstruct',
    id: 'rec',
    marker: 'src:marker',
    mask: 'src:mask',
    connectivity: CROSS,
    budget: 100,
  },
];

function makeChannelEngine(budget: number): { engine: Engine; mask: GrayImage; marker: GrayImage } {
  const { mask, marker } = channelSources();
  const engine = new Engine(W, H, { maxTiles: 8, tileSize: 4 });
  engine.addSource('mask', mask);
  engine.addSource('marker', marker);
  const stages = [{ ...channelStages[0], budget } as StageDef];
  engine.setStages(stages);
  return { engine, mask, marker };
}

test('bridging a 1-px gap propagates to the far end; breaking revokes it', () => {
  const { engine, mask, marker } = makeChannelEngine(100);
  const sources: Record<string, GrayImage> = { mask, marker };
  engine.run();
  let ref = runReference(sources, channelStages);
  assert.ok(engine.getOutput('rec').equals(ref.get('rec')!));
  assert.equal(engine.getOutput('rec').get(0, ROW), 0, 'gap blocks the fixed point');
  assert.equal(engine.getOutput('rec').get(GAP + 1, ROW), 255);

  // Bridge the gap with a single pixel: influence must travel to the far end.
  engine.setPixel('mask', GAP, ROW, 255);
  mask.set(GAP, ROW, 255);
  ref = runReference(sources, channelStages);
  const bridged = engine.getOutput('rec');
  assert.ok(bridged.equals(ref.get('rec')!), 'incremental == full reference after bridge');
  assert.equal(bridged.get(0, ROW), 255, 'far end lit through 39 px of corridor');
  const repBridge = engine.getReport('rec')!;
  assert.equal(repBridge.converged, true);
  assert.equal(repBridge.changedPixels, GAP + 1, 'exactly the newly reached pixels changed');

  // Break it again: stale propagation support must be revoked.
  engine.setPixel('mask', GAP, ROW, 0);
  mask.set(GAP, ROW, 0);
  ref = runReference(sources, channelStages);
  const broken = engine.getOutput('rec');
  assert.ok(broken.equals(ref.get('rec')!), 'incremental == full reference after break');
  assert.equal(broken.get(0, ROW), 0, 'far end reverted');
  assert.equal(broken.get(W - 1, ROW), 255, 'marker side kept');
  assert.equal(engine.getReport('rec')!.converged, true);
});

test('budget exhaustion keeps the previously published output', () => {
  const { engine } = makeChannelEngine(10);
  engine.run();
  assert.equal(engine.getReport('rec')!.converged, true, 'initial 8 sweeps fit the budget');
  const before = engine.getOutput('rec');

  const reports = engine.setPixel('mask', GAP, ROW, 255);
  const rep = reports.find((r) => r.id === 'rec')!;
  assert.equal(rep.mode, 'failed');
  assert.equal(rep.budgetExceeded, true);
  assert.equal(rep.published, false);
  assert.ok(engine.getOutput('rec').equals(before), 'old published version retained');
  assert.equal(engine.getOutput('rec').get(0, ROW), 0);
});

function multiStageGraph(): {
  stages: StageDef[];
  sources: Record<string, GrayImage>;
  ids: string[];
} {
  const w = 16;
  const h = 12;
  const a = new GrayImage(w, h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      a.set(x, y, (x * 37 + y * 91) % 101 > 60 ? 255 : 0);
    }
  }
  const b = new GrayImage(w, h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      b.set(x, y, (x * 13 + y * 7) % 5 === 0 ? 0 : 180);
    }
  }
  const label = new Uint8Array(w * h).fill(1);
  for (let y = 0; y < h; y++) label[y * w + 3] = 0; // passthrough column
  const stages: StageDef[] = [
    { kind: 'dilate', id: 'd1', input: 'src:a', se: makeSE([[0, 0], [1, 0], [2, 0]], [1, 0]), boundary: 'clamp' },
    { kind: 'close', id: 'c1', input: 'd1', se: LINE3_RIGHT, boundary: 'skip' },
    { kind: 'open', id: 'o1', input: 'c1', se: SQUARE3, boundary: 'zero', label },
    { kind: 'reconstruct', id: 'r1', marker: 'o1', mask: 'src:b', connectivity: CROSS, budget: 500 },
  ];
  return { stages, sources: { a, b }, ids: ['d1', 'c1', 'o1', 'r1'] };
}

function assertEngineMatchesRef(engine: Engine, sources: Record<string, GrayImage>, stages: StageDef[], ids: string[]): void {
  const ref = runReference(sources, stages);
  for (const id of ids) {
    const got = engine.getOutput(id);
    const want = ref.get(id)!;
    assert.ok(got.equals(want), `stage '${id}' diverged from reference`);
  }
}

test('multi-stage graph stays exact across a sequence of local edits', () => {
  const { stages, sources, ids } = multiStageGraph();
  const engine = new Engine(16, 12, { maxTiles: 4, tileSize: 4 });
  engine.addSource('a', sources.a);
  engine.addSource('b', sources.b);
  engine.setStages(stages);
  engine.run();
  assertEngineMatchesRef(engine, sources, stages, ids);

  const edits: Array<[string, number, number, number]> = [
    ['a', 5, 5, 255], // grow foreground
    ['a', 6, 5, 255],
    ['a', 5, 5, 0], // shrink it back (decrease path)
    ['b', 8, 8, 0], // mask cut -> revoke support
    ['b', 8, 8, 180], // restore
    ['a', 12, 2, 255],
    ['b', 12, 2, 0],
  ];
  for (const [src, x, y, v] of edits) {
    engine.setPixel(src, x, y, v);
    sources[src].set(x, y, v);
    assertEngineMatchesRef(engine, sources, stages, ids);
  }
});

test('tile cache bound of a single tile stays correct (evictions, no truncation)', () => {
  const { stages, sources, ids } = multiStageGraph();
  const engine = new Engine(16, 12, { maxTiles: 1, tileSize: 4 });
  engine.addSource('a', sources.a);
  engine.addSource('b', sources.b);
  engine.setStages(stages);
  engine.run();
  engine.setPixel('a', 5, 5, 255);
  sources.a.set(5, 5, 255);
  engine.setPixel('b', 8, 8, 0);
  sources.b.set(8, 8, 0);
  assertEngineMatchesRef(engine, sources, stages, ids);
  const stats = engine.cacheStats();
  assert.ok(stats.resident <= stats.maxTiles, 'resident tiles respect the bound');
  assert.ok(stats.evictions > 0, 'cache actually evicted under pressure');
});

test('RGBA source end-to-end through the engine', () => {
  const rgba = RgbaImage.fromAscii(['....', '.##.', '.#..', '....'], {
    '.': [0, 0, 0, 255],
    '#': [255, 255, 255, 255],
  });
  const engine = new Engine(4, 4);
  engine.addSource('img', rgba.toGray());
  engine.setStages([{ kind: 'dilate', id: 'd', input: 'src:img', se: CROSS }]);
  engine.run();
  const out = engine.getOutput('d').toRgba();
  assert.deepEqual(out.get(2, 2), [255, 255, 255, 255]);
  assert.deepEqual(out.get(0, 0), [0, 0, 0, 255]);
});

test('compileGraph rejects cycles and unknown references', () => {
  assert.throws(() =>
    compileGraph([
      { kind: 'dilate', id: 'a', input: 'b', se: CROSS },
      { kind: 'dilate', id: 'b', input: 'a', se: CROSS },
    ]),
  );
  assert.throws(() => compileGraph([{ kind: 'dilate', id: 'a', input: 'nope', se: CROSS }]));
});
