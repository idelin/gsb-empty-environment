/**
 * Runnable demo: build a small RGBA scene, compile a 2-stage morphology
 * graph (asymmetric opening -> mask-constrained reconstruction), then edit
 * single pixels and watch the incremental engine maintain the result —
 * including long-range propagation across a corridor and its revocation.
 *
 * Run with: npm run demo
 */
import {
  Pipeline,
  StructuringElement,
  compileGraph,
  overlayPlane,
  planeFromChannel,
  planeFromLuminance,
  planeToAscii,
  rgbaFromAscii,
  rgbaToAscii,
  type Rgba,
} from '../src/index.ts';

const PALETTE: Record<string, Rgba> = {
  '.': [16, 16, 24, 255], // background
  W: [235, 235, 235, 255], // corridor (mask)
  S: [255, 120, 120, 255], // seed (red-ish, bright enough to stay in the mask)
  T: [0, 0, 0, 0], // transparent gap pixel blocking the corridor
  O: [64, 160, 255, 255], // overlay: reconstructed foreground
};

// 34-wide corridor, blocked by a single transparent gap pixel at x=16.
const scene = rgbaFromAscii(
  [
    '.................................',
    '.WSSSWWWWWWWWWWWTWWWWWWWWWWWWWWW.',
    '.................................',
  ],
  PALETTE,
);

const mask = planeFromLuminance(scene, 128); // W and S are in the mask; T is not
const red = planeFromChannel(scene, 0, 200);
const green = planeFromChannel(scene, 1, 200);
const marker = planeFromLuminance(scene, 128);
for (let i = 0; i < marker.data.length; i++) marker.data[i] = red.data[i] && !green.data[i] ? 1 : 0;

const graph = compileGraph(
  {
    stages: [
      // stage 1: asymmetric opening (3-wide SE anchored at its left end)
      {
        kind: 'local',
        name: 'opened',
        op: 'open',
        input: 'marker',
        se: new StructuringElement([[0, 0], [1, 0], [2, 0]], [0, 0]),
        boundary: 'zero',
      },
      // stage 2: geodesic reconstruction of the opened marker under the mask
      {
        kind: 'reconstruct',
        name: 'recon',
        marker: 'opened',
        mask: 'mask',
        se: StructuringElement.cross(1), // kernel radius 1; propagation spans the corridor
        maxIterations: 500,
      },
    ],
    output: 'recon',
  },
  ['marker', 'mask'],
);

const pipe = new Pipeline(graph, { marker, mask }, { tileSize: 8, maxCachedTiles: 6 });

const show = (title: string) => {
  const painted = overlayPlane(scene, pipe.outputPlane(), PALETTE.O);
  console.log(`\n== ${title} ==`);
  for (const row of rgbaToAscii(painted, PALETTE)) console.log(row);
  const stages = pipe.lastReport.stages
    .map((s) => `${s.name}: ${s.status}${s.iterations !== undefined ? ` (${s.iterations} waves)` : ''}`)
    .join(' | ');
  console.log(`stages: ${stages}`);
};

console.log('mask:');
console.log(planeToAscii(mask).join('\n'));
console.log('\nmarker (seed, survives the 3-wide opening):');
console.log(planeToAscii(marker).join('\n'));

show('initial: fixed point stops at the gap (x=15)');

let report = pipe.applyEdits('mask', [{ x: 16, y: 1, value: 1 }]);
console.log(`\nedit: mask[16,1] = 1  ->  ${report.stages.map((s) => `${s.name}:${s.status}`).join(', ')}`);
show('gap bridged by one pixel: propagation reached the far end');

report = pipe.applyEdits('mask', [{ x: 16, y: 1, value: 0 }]);
console.log(`\nedit: mask[16,1] = 0  ->  ${report.stages.map((s) => `${s.name}:${s.status}`).join(', ')}`);
show('gap reopened: far-end support revoked');

console.log('\ntile cache:', pipe.cacheStats());
