/**
 * Acceptance demo:
 *  - build a BVH over a scene of 1000 triangles (SAH)
 *  - cast 100 rays
 *  - verify every result against brute force
 *  - report leaf-pruning ratio (target: > 95%) and runtime (target: ms)
 *  - exercise dynamic insert/remove
 */
import { BVH } from '../src/bvh.ts';
import type { Triangle } from '../src/triangle.ts';
import {
  generateScene,
  generateRays,
  bruteForceNearest,
  makeRng,
} from './scene.ts';

const triBounds = (t: Triangle) => t.bounds;
const triIntersect = (t: Triangle, ray: Parameters<Triangle['intersectRay']>[0]) =>
  t.intersectRay(ray);

const N_TRIANGLES = 1000;
const N_RAYS = 100;

console.log('=== Dynamic BVH demo ===\n');

// 1. Build to build
const triangles = generateScene(N_TRIANGLES);
let t0 = performance.now();
const bvh = BVH.build(triangles, triBounds);
const buildMs = performance.now() - t0;
console.log(`[build]  ${N_TRIANGLES} triangles -> SAH BVH in ${buildMs.toFixed(2)} ms`);

// 2. Cast rays
const rays = generateRays(N_RAYS, 7, triangles);
t0 = performance.now();
let visitedLeaves = 0;
let visitedInternal = 0;
let hits = 0;
let mismatches = 0;
for (const ray of rays) {
  const hit = bvh.raycast(ray, triIntersect);
  visitedLeaves += bvh.stats.leafNodesVisited;
  visitedInternal += bvh.stats.internalNodesVisited;
  if (hit) hits++;
  const truth = bruteForceNearest(triangles, ray);
  const ok =
    (hit === null && truth === null) ||
    (hit !== null && truth !== null &&
      hit.object === truth.object && Math.abs(hit.t - truth.t) < 1e-6);
  if (!ok) mismatches++;
}
const queryMs = performance.now() - t0;

const totalLeafVisitsCapacity = N_RAYS * N_TRIANGLES;
const skipRatio = 1 - visitedLeaves / totalLeafVisitsCapacity;

console.log(`[query]  ${N_RAYS} rays in ${queryMs.toFixed(2)} ms (${(queryMs / N_RAYS).toFixed(3)} ms/ray)`);
console.log(`[prune]  avg leaves tested/ray: ${(visitedLeaves / N_RAYS).toFixed(1)} / ${N_TRIANGLES}`);
console.log(`[prune]  avg internal nodes/ray: ${(visitedInternal / N_RAYS).toFixed(1)}`);
console.log(`[prune]  leaf skip ratio: ${(skipRatio * 100).toFixed(2)}% (target > 95%)`);
console.log(`[result] hits: ${hits}/${N_RAYS}, mismatches vs brute force: ${mismatches}`);

// 3. Dynamic updates
const rng = makeRng(2024);
const dynamic = new BVH<Triangle>(triBounds);
for (const t of triangles) dynamic.insert(t);
console.log(`\n[dynamic] inserted ${dynamic.size} objects one-by-one, valid: ${dynamic.validate().length === 0}`);
let removed = 0;
for (let i = 0; i < 200; i++) {
  const victim = triangles[Math.floor(rng() * triangles.length)];
  if (dynamic.remove(victim)) removed++;
}
console.log(`[dynamic] removed ${removed} objects, size=${dynamic.size}, valid: ${dynamic.validate().length === 0}`);
const dynHit = dynamic.raycast(rays[0], triIntersect);
console.log(`[dynamic] raycast after updates: ${dynHit ? `hit at t=${dynHit.t.toFixed(3)}` : 'miss'}`);

// 4. Verdict
console.log('\n=== verdict ===');
const pass = skipRatio > 0.95 && mismatches === 0 && queryMs < 100;
console.log(`pruning > 95% ........ ${skipRatio > 0.95 ? 'PASS' : 'FAIL'}`);
console.log(`exact results ........ ${mismatches === 0 ? 'PASS' : 'FAIL'}`);
console.log(`millisecond queries .. ${queryMs < 100 ? 'PASS' : 'FAIL'} (${queryMs.toFixed(2)} ms for 100 rays)`);
process.exit(pass ? 0 : 1);
