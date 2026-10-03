import { performance } from 'node:perf_hooks';
import { Broadphase } from '../src/broadphase.ts';
import { makeAABB } from '../src/aabb.ts';

// Demo: 500 rigid bodies moving every frame; exactly 12 pairs overlap.
// The broadphase incrementally updates a dynamic AABB tree and emits the
// candidate pairs for the narrowphase — no O(N^2) pairwise checks.

const N = 500;
const FRAMES = 120;
const WARMUP = 20;
const bp = new Broadphase(0.1);

const base = new Float64Array(N * 3);  // grid anchor per body
const dir = new Float64Array(N * 3);   // oscillation direction per body
const HALF = 1;                        // half-extent of every body
const AMP = 2;                         // oscillation amplitude (< grid gap)

const spacing = 10;
for (let id = 0; id < N; id++) {
  base[id * 3 + 0] = (id % 8) * spacing;
  base[id * 3 + 1] = (Math.floor(id / 8) % 8) * spacing;
  base[id * 3 + 2] = Math.floor(id / 64) * spacing;
  dir[id * 3 + 0] = Math.sin(id * 12.9898);
  dir[id * 3 + 1] = Math.cos(id * 78.233);
  dir[id * 3 + 2] = Math.sin(id * 3.1415);
  bp.addBody(id, boxOf(id, 0));
}

function boxOf(id: number, t: number) {
  const s = AMP * Math.sin(t);
  const x = base[id * 3] + dir[id * 3] * s;
  const y = base[id * 3 + 1] + dir[id * 3 + 1] * s;
  const z = base[id * 3 + 2] + dir[id * 3 + 2] * s;
  return makeAABB(x - HALF, y - HALF, z - HALF, x + HALF, y + HALF, z + HALF);
}

// Plant 12 colliding pairs: bodies (0,1), (2,3), ..., (22,23) share motion.
const PLANTED = 12;
for (let k = 0; k < PLANTED; k++) {
  const a = 2 * k;
  const b = 2 * k + 1;
  base[b * 3] = base[a * 3] + 0.5; // overlapping boxes (offset < 2 * HALF)
  base[b * 3 + 1] = base[a * 3 + 1];
  base[b * 3 + 2] = base[a * 3 + 2];
  dir[b * 3] = dir[a * 3];
  dir[b * 3 + 1] = dir[a * 3 + 1];
  dir[b * 3 + 2] = dir[a * 3 + 2];
  bp.moveBody(b, boxOf(b, 0));
}

console.log(`scene: ${N} bodies, ${PLANTED} overlapping pairs, ${FRAMES} frames\n`);

const updateUs: number[] = [];
const queryUs: number[] = [];
let pairCount = 0;

for (let frame = 0; frame < FRAMES + WARMUP; frame++) {
  const t = frame * 0.15;
  const u0 = performance.now();
  for (let id = 0; id < N; id++) bp.moveBody(id, boxOf(id, t));
  const u1 = performance.now();
  const pairs = bp.computePairs();
  const u2 = performance.now();

  if (frame >= WARMUP) {
    updateUs.push((u1 - u0) * 1000);
    queryUs.push((u2 - u1) * 1000);
  }
  pairCount = pairs.length;

  if (frame === 0) {
    console.log('frame 0 candidate pairs -> narrowphase:');
    console.log('  ' + pairs.map((p) => `(${p.bodyA}, ${p.bodyB})`).join(' '));
    console.log();
  }
}

function median(xs: number[]): number {
  const s = [...xs].sort((a, b) => a - b);
  return s[Math.floor(s.length / 2)];
}

// O(N^2) brute-force reference for contrast.
const boxes = [];
for (let id = 0; id < N; id++) boxes.push(boxOf(id, 0));
const b0 = performance.now();
let bruteCount = 0;
for (let i = 0; i < N; i++) {
  for (let j = i + 1; j < N; j++) {
    const a = boxes[i];
    const b = boxes[j];
    if (
      a.minX <= b.maxX && a.maxX >= b.minX &&
      a.minY <= b.maxY && a.maxY >= b.minY &&
      a.minZ <= b.maxZ && a.maxZ >= b.minZ
    ) bruteCount++;
  }
}
const bruteUs = (performance.now() - b0) * 1000;

console.log(`candidate pairs per frame : ${pairCount} (expected ${PLANTED})`);
console.log(`brute-force O(N^2) check  : ${bruteUs.toFixed(1)} us for ${N} bodies (${bruteCount} pairs)`);
console.log(`tree update, 500 moves    : median ${median(updateUs).toFixed(1)} us/frame`);
console.log(`pair filtering            : median ${median(queryUs).toFixed(1)} us/frame`);
console.log(`tree height               : ${bp.treeHeight} (log2(500) ~ 9)`);
