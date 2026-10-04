import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Pipeline,
  StructuringElement,
  compileGraph,
  localOpPlane,
  makePlane,
  planeToAscii,
  reconstructReference,
  type Plane,
} from '../src/index.ts';

function randomPlane(width: number, height: number, density: number, seed: number): Plane {
  let s = seed >>> 0;
  const rnd = () => ((s = (s * 1664525 + 1013904223) >>> 0) / 2 ** 32);
  const plane = makePlane(width, height);
  for (let i = 0; i < plane.data.length; i++) plane.data[i] = rnd() < density ? 1 : 0;
  return plane;
}

/** Corridor mask: 60-px horizontal channel blocked by a single gap pixel. */
function corridor(): { mask: Plane; marker: Plane } {
  const mask = makePlane(64, 3);
  for (let x = 0; x < 60; x++) mask.data[1 * 64 + x] = 1;
  mask.data[1 * 64 + 30] = 0; // the gap
  const marker = makePlane(64, 3);
  marker.data[1 * 64 + 0] = 1;
  return { mask, marker };
}

function corridorPipeline(maxIterations?: number): Pipeline {
  const { mask, marker } = corridor();
  const graph = compileGraph(
    {
      stages: [
        {
          kind: 'reconstruct',
          name: 'recon',
          marker: 'marker',
          mask: 'mask',
          se: StructuringElement.cross(1), // kernel radius 1, propagation 60 px
          maxIterations,
        },
      ],
      output: 'recon',
    },
    ['marker', 'mask'],
  );
  return new Pipeline(graph, { marker, mask }, { tileSize: 8, maxCachedTiles: 4 });
}

test('one pixel opens a long channel: propagation reaches the far end', () => {
  const pipe = corridorPipeline();
  const before = pipe.outputPlane();
  assert.equal(before.data[1 * 64 + 29], 1);
  assert.equal(before.data[1 * 64 + 30], 0, 'gap blocks the fixed point');
  assert.equal(before.data[1 * 64 + 59], 0);

  const report = pipe.applyEdits('mask', [{ x: 30, y: 1, value: 1 }]);
  assert.equal(report.stages[0].status, 'fixed-point');
  const after = pipe.outputPlane();
  assert.equal(after.data[1 * 64 + 59], 1, 'effect propagated far beyond the kernel radius');
  assert.equal(after.data[1 * 64 + 30], 1);
  // cross-check against the naive iterative reference on the edited inputs
  const { mask, marker } = corridor();
  mask.data[1 * 64 + 30] = 1;
  const ref = reconstructReference(marker, mask, StructuringElement.cross(1));
  assert.ok(ref.converged);
  assert.deepEqual([...after.data], [...ref.result.data]);
});

test('breaking the channel revokes the old propagation support', () => {
  const pipe = corridorPipeline();
  pipe.applyEdits('mask', [{ x: 30, y: 1, value: 1 }]);
  assert.equal(pipe.outputPlane().data[1 * 64 + 59], 1);

  const report = pipe.applyEdits('mask', [{ x: 30, y: 1, value: 0 }]);
  assert.equal(report.stages[0].status, 'fixed-point');
  const after = pipe.outputPlane();
  assert.equal(after.data[1 * 64 + 59], 0, 'far-end support revoked');
  assert.equal(after.data[1 * 64 + 31], 0);
  assert.equal(after.data[1 * 64 + 29], 1, 'near side keeps its support');
  const { mask, marker } = corridor();
  const ref = reconstructReference(marker, mask, StructuringElement.cross(1));
  assert.deepEqual([...after.data], [...ref.result.data]);
});

test('budget exhaustion keeps the previously published output', () => {
  const pipe = corridorPipeline(3); // 60-px channel needs ~60 waves
  const publishedBefore = planeToAscii(pipe.outputPlane());
  const report = pipe.applyEdits('mask', [{ x: 30, y: 1, value: 1 }]);
  assert.equal(report.stages[0].status, 'budget-exhausted');
  assert.equal(report.stages[0].changedPixels, 0);
  assert.deepEqual(planeToAscii(pipe.outputPlane()), publishedBefore, 'published output untouched');
  // raising the budget lets the same edit publish
  const pipe2 = corridorPipeline(100);
  const report2 = pipe2.applyEdits('mask', [{ x: 30, y: 1, value: 1 }]);
  assert.equal(report2.stages[0].status, 'fixed-point');
  assert.equal(pipe2.outputPlane().data[1 * 64 + 59], 1);
});

/** Reference: recompute the whole 3-stage pipeline naively from sources. */
function referencePipeline(src: Plane, mask: Plane, labels: Plane): Plane {
  const seOpen = new StructuringElement([[0, 0], [1, 0], [0, 1]], [0, 0]);
  const opened = localOpPlane('open', src, seOpen, 'zero', { plane: labels, mask: new Set([1]) });
  const recon = reconstructReference(opened, mask, StructuringElement.cross(1));
  const gated = makePlane(src.width, src.height);
  for (let i = 0; i < gated.data.length; i++)
    gated.data[i] = labels.data[i] === 1 ? recon.result.data[i] : opened.data[i];
  const seErode = new StructuringElement([[0, 0], [0, 1]], [0, 0]);
  return localOpPlane('erode', gated, seErode, 'clamp');
}

test('random edits on a 3-stage graph match full naive recomputation', () => {
  const W = 40;
  const H = 30;
  const src = randomPlane(W, H, 0.3, 11);
  const mask = randomPlane(W, H, 0.55, 22);
  const labels = makePlane(W, H);
  let s = 33;
  const rnd = () => ((s = (s * 1664525 + 1013904223) >>> 0) / 2 ** 32);
  for (let i = 0; i < labels.data.length; i++) labels.data[i] = rnd() < 0.5 ? 1 : 9;

  const seOpen = new StructuringElement([[0, 0], [1, 0], [0, 1]], [0, 0]);
  const seErode = new StructuringElement([[0, 0], [0, 1]], [0, 0]);
  const graph = compileGraph(
    {
      stages: [
        { kind: 'local', name: 'opened', op: 'open', input: 'src', se: seOpen, labels: new Set([1]) },
        { kind: 'reconstruct', name: 'recon', marker: 'opened', mask: 'mask', se: StructuringElement.cross(1), labels: new Set([1]) },
        { kind: 'local', name: 'final', op: 'erode', input: 'recon', se: seErode, boundary: 'clamp' },
      ],
      output: 'final',
    },
    ['src', 'mask'],
  );
  // deliberately tiny cache: 8 hot tiles of 8x8 for 6 planes of 40x30
  const pipe = new Pipeline(graph, { src, mask }, { labels, tileSize: 8, maxCachedTiles: 8 });

  const checkAgainst = (what: string) => {
    const expected = referencePipeline(pipe.outputPlane('src'), pipe.outputPlane('mask'), labels);
    const actual = pipe.outputPlane();
    assert.deepEqual([...actual.data], [...expected.data], `mismatch after ${what}`);
    assert.ok(pipe.cacheStats().hotTiles <= 8, 'tile cache budget exceeded');
  };

  checkAgainst('initial full run');
  for (let round = 0; round < 12; round++) {
    const edits = [];
    const n = 1 + Math.floor(rnd() * 5);
    for (let k = 0; k < n; k++) {
      edits.push({
        x: Math.floor(rnd() * W),
        y: Math.floor(rnd() * H),
        value: rnd() < 0.5 ? 1 : 0,
      });
    }
    pipe.applyEdits(rnd() < 0.7 ? 'src' : 'mask', edits);
    checkAgainst(`edit round ${round} (${n} px)`);
  }
});

test('per-stage label masks pass non-matching labels through unchanged', () => {
  const src = planeFromBits(['#..', '...', '..#']);
  const labels = makePlane(3, 3);
  labels.data[0] = 5; // top-left is label 5: stage must not touch it
  const graph = compileGraph(
    {
      stages: [
        { kind: 'local', name: 'd', op: 'dilate', input: 'src', se: StructuringElement.square(1), labels: new Set([0]) },
      ],
    },
    ['src'],
  );
  const pipe = new Pipeline(graph, { src }, { labels });
  const out0 = pipe.outputPlane();
  assert.equal(out0.data[0], 1, 'label-5 pixel keeps input value');
  const report = pipe.applyEdits('src', [{ x: 0, y: 0, value: 0 }]);
  assert.ok(report.stages[0].changedPixels >= 1);
  const out1 = pipe.outputPlane();
  assert.equal(out1.data[0], 0, 'label-5 pixel follows the input (pass-through)');
  assert.equal(out1.data[1], 0, 'label-0 pixel recomputed by the op');
});

function planeFromBits(rows: string[]): Plane {
  const plane = makePlane(rows[0].length, rows.length);
  for (let y = 0; y < rows.length; y++)
    for (let x = 0; x < rows[y].length; x++) plane.data[y * plane.width + x] = rows[y][x] === '#' ? 1 : 0;
  return plane;
}
