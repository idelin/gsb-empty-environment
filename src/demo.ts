import { BVH } from "./bvh.ts";
import { rayTriangle } from "./triangle.ts";
import {
  bruteForceRaycast,
  generateRays,
  generateTriangles,
  triangleItems,
} from "./scene.ts";

const TRIANGLE_COUNT = 1000;
const RAY_COUNT = 100;

// 1. Build a scene of 1000 triangles clustered as distinct objects.
const triangles = generateTriangles(TRIANGLE_COUNT, 40);
const items = triangleItems(triangles);

const buildStart = performance.now();
const tree = BVH.build(items);
const buildMs = performance.now() - buildStart;

// 2. Cast 100 rays through the BVH.
const rays = generateRays(RAY_COUNT);
tree.resetStats();
const castStart = performance.now();
let hits = 0;
for (const ray of rays) {
  const hit = tree.raycast(ray.origin, ray.dir, (tri) =>
    rayTriangle(ray.origin, ray.dir, tri),
  );
  if (hit) hits++;
}
const castMs = performance.now() - castStart;

// 3. Cross-check against brute force and compute pruning ratio.
let mismatches = 0;
for (const ray of rays) {
  const bvhHit = tree.raycast(ray.origin, ray.dir, (tri) =>
    rayTriangle(ray.origin, ray.dir, tri),
  );
  const brute = bruteForceRaycast(triangles, ray.origin, ray.dir, rayTriangle);
  const bvhT = bvhHit ? bvhHit.t : null;
  const bruteT = brute ? brute.t : null;
  if (bvhT === null !== (bruteT === null)) mismatches++;
  else if (bvhT !== null && bruteT !== null && Math.abs(bvhT - bruteT) > 1e-6) mismatches++;
}

const totalLeafTests = tree.stats.leafTests;
const bruteForceTests = RAY_COUNT * TRIANGLE_COUNT;
const skippedRatio = 1 - totalLeafTests / bruteForceTests;

console.log("=== Dynamic BVH demo ===");
console.log(`triangles:            ${TRIANGLE_COUNT}`);
console.log(`rays:                 ${RAY_COUNT}`);
console.log(`build time:           ${buildMs.toFixed(2)} ms`);
console.log(`raycast total time:   ${castMs.toFixed(2)} ms (${(castMs / RAY_COUNT).toFixed(3)} ms/ray)`);
console.log(`rays that hit:        ${hits}/${RAY_COUNT}`);
console.log(`leaf tests (BVH):     ${totalLeafTests}`);
console.log(`leaf tests (brute):   ${bruteForceTests}`);
console.log(`leaves skipped:       ${(skippedRatio * 100).toFixed(2)}%`);
console.log(`nearest-hit matches:  ${mismatches === 0 ? "yes (100/100)" : `NO — ${mismatches} mismatches`}`);

if (mismatches > 0 || skippedRatio < 0.95) {
  console.error("ACCEPTANCE FAILED");
  process.exit(1);
}
console.log("ACCEPTANCE PASSED");
