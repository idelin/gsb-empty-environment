/**
 * Runnable demo: multi-stage morphology graph over self-built RGBA images,
 * incremental updates, geodesic reconstruction across a 1-px bridge, and
 * budget-failure semantics. Run with: node examples/demo.ts  (Node >= 22.6)
 */
import { RgbaImage } from '../src/image.ts';
import type { GrayImage } from '../src/image.ts';
import { CROSS, makeSE } from '../src/se.ts';
import { Engine } from '../src/graph.ts';
import type { StageDef, StageReport } from '../src/graph.ts';

const W = 48;
const H = 14;
const ROWS = [6, 7];
const X0 = 2;
const X1 = 45;
const GAP = 38; // 1-px wide break in the corridor

const BLACK: [number, number, number, number] = [0, 0, 0, 255];
const WALL: [number, number, number, number] = [200, 200, 200, 255];
const NOISE: [number, number, number, number] = [160, 160, 160, 255];
const SEED: [number, number, number, number] = [255, 255, 255, 255];

function buildScene(): RgbaImage {
  const img = new RgbaImage(W, H);
  for (let y = 0; y < H; y++) for (let x = 0; x < W; x++) img.set(x, y, BLACK);
  for (const y of ROWS) {
    for (let x = X0; x <= X1; x++) {
      if (x !== GAP) img.set(x, y, WALL);
    }
  }
  // isolated noise the opening should remove
  img.set(10, 2, NOISE);
  img.set(30, 10, NOISE);
  img.set(5, 11, NOISE);
  return img;
}

function buildSeeds(): RgbaImage {
  const img = new RgbaImage(W, H);
  for (let y = 0; y < H; y++) for (let x = 0; x < W; x++) img.set(x, y, BLACK);
  img.set(X1, ROWS[0], SEED); // seed at the far right end of the corridor
  return img;
}

// Non-symmetric SE: a corner pointing right/down, origin at its tip.
const SE_CORNER = makeSE([[0, 0], [1, 0], [0, 1]], [0, 0]);

function makeStages(budget: number): StageDef[] {
  return [
    { kind: 'open', id: 'opened', input: 'src:scene', se: SE_CORNER, boundary: 'clamp' },
    {
      kind: 'reconstruct',
      id: 'rec',
      marker: 'src:seeds',
      mask: 'opened',
      connectivity: CROSS,
      budget,
    },
  ];
}

function show(title: string, img: GrayImage): void {
  console.log(`--- ${title} ---`);
  for (const row of img.toAscii()) console.log(row);
}

function fmtReports(reports: StageReport[]): string {
  return reports
    .map((r) => {
      const extra =
        r.mode === 'failed'
          ? 'BUDGET EXHAUSTED -> kept previous published output'
          : r.converged === undefined
            ? `${r.changedPixels}px changed`
            : `converged=${r.converged} sweeps=${r.iterations} changed=${r.changedPixels}px`;
      return `  [${r.id}] ${r.mode}: ${extra}`;
    })
    .join('\n');
}

const scene = buildScene();
const seeds = buildSeeds();

const engine = new Engine(W, H, { maxTiles: 6, tileSize: 8 }); // tiny cache on purpose
engine.addSource('scene', scene.toGray());
engine.addSource('seeds', seeds.toGray());
engine.setStages(makeStages(400));

console.log('== phase 1: full run (open cleans noise, reconstruction fills up to the gap) ==');
console.log(fmtReports(engine.run()));
show('opened (mask after opening)', engine.getOutput('opened'));
show('rec (geodesic reconstruction from the seed)', engine.getOutput('rec'));
console.log(`  far end (${X0},${ROWS[0]}) = ${engine.getOutput('rec').get(X0, ROWS[0])} (gap blocks it)`);

console.log('\n== phase 2: bridge the 1-px gap with two pixel edits ==');
console.log(fmtReports(engine.setPixel('scene', GAP, ROWS[0], 200)));
console.log(fmtReports(engine.setPixel('scene', GAP, ROWS[1], 200)));
show('rec after bridging', engine.getOutput('rec'));
console.log(`  far end (${X0},${ROWS[0]}) = ${engine.getOutput('rec').get(X0, ROWS[0])} (propagated ${GAP - X0}px past the kernel radius)`);

console.log('\n== phase 3: break the bridge again (stale support must be revoked) ==');
console.log(fmtReports(engine.setPixel('scene', GAP, ROWS[0], 0)));
console.log(fmtReports(engine.setPixel('scene', GAP, ROWS[1], 0)));
console.log(`  far end (${X0},${ROWS[0]}) = ${engine.getOutput('rec').get(X0, ROWS[0])} (reverted)`);

console.log('\n== phase 4: same edit with a tiny sweep budget keeps the published version ==');
const engine2 = new Engine(W, H, { maxTiles: 6, tileSize: 8 });
engine2.addSource('scene', scene.toGray());
engine2.addSource('seeds', seeds.toGray());
engine2.setStages(makeStages(10)); // enough for the initial 6 sweeps, not for the 36-sweep bridge
console.log(fmtReports(engine2.run()));
console.log(fmtReports(engine2.setPixel('scene', GAP, ROWS[0], 200)));
console.log(fmtReports(engine2.setPixel('scene', GAP, ROWS[1], 200)));
console.log(`  far end (${X0},${ROWS[0]}) = ${engine2.getOutput('rec').get(X0, ROWS[0])} (unchanged: old version kept)`);

console.log('\n== tile cache stats (bounded working set) ==');
console.log('  engine1:', engine.cacheStats());
console.log('  engine2:', engine2.cacheStats());

const rgba = engine.getOutput('rec').toRgba();
console.log('\n== RGBA readback of the result ==');
console.log(`  pixel (${X1},${ROWS[0]}) =`, rgba.get(X1, ROWS[0]));
console.log(`  pixel (${X0},${ROWS[0]}) =`, rgba.get(X0, ROWS[0]));
