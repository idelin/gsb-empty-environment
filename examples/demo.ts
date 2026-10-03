/**
 * End-to-end demo:
 *  1. Build a 2D scene from SDF primitives + CSG.
 *  2. Rasterize it into a 64x64 SDF grid and print an ASCII visualisation.
 *  3. Extract the zero isocontour (marching squares).
 *  4. Sphere-trace rays from arbitrary origins against the analytic field
 *     and against the sampled grid.
 *  5. Same pipeline in 3D: 64^3 grid, marching-tetrahedra isosurface, rays.
 */
import { circle, box2, subtract2, smoothUnion2, sphere, box3, smoothUnion3 } from '../src/sdf.ts';
import { SdfGrid2D, SdfGrid3D } from '../src/grid.ts';
import { extractContour2D } from '../src/contour2d.ts';
import { extractSurface3D } from '../src/surface3d.ts';
import { rayMarch2, rayMarch3 } from '../src/raymarch.ts';
import type { Vec2, Vec3 } from '../src/vec.ts';

const line = (s = '') => console.log(s);

/* --------------------------------- 2D ------------------------------------ */

line('=== 2D: scene = smoothUnion(circle, box) - small circle ===');
const scene2 = subtract2(
  smoothUnion2(0.15, circle([-0.25, 0], 0.45), box2([0.35, 0.1], [0.3, 0.25])),
  circle([0.1, 0.35], 0.15),
);

const grid2 = SdfGrid2D.fromSdf(scene2, { min: [-1, -1], max: [1, 1] }, 64, 64);
line(`rasterized 64x64 SDF grid, cell = ${grid2.cellX.toFixed(4)} x ${grid2.cellY.toFixed(4)}`);
line();

// ASCII visualisation: '#' inside (d<0), '+' near surface, '.' outside.
const ramp = (d: number) => (d < -0.15 ? '#' : d < 0 ? '*' : d < 0.08 ? '+' : '.');
for (let j = grid2.ny - 1; j >= 0; j -= 2) {
  let row = '';
  for (let i = 0; i < grid2.nx; i++) row += ramp(grid2.at(i, j));
  line(row);
}
line();

const contour = extractContour2D(grid2);
line(`marching squares: ${contour.length} contour segments extracted`);
line();

line('--- 2D ray marching (sphere tracing) ---');
const rays2: Array<[Vec2, Vec2]> = [
  [[-2, 0], [1, 0]],
  [[1.8, 1.2], [-0.9, -0.44]],
  [[0.1, 2], [0, -1]],
  [[-2, -2], [0, -1]], // miss
];
for (const [origin, dir] of rays2) {
  const analytic = rayMarch2(scene2, origin, dir, { maxDistance: 20 });
  const sampled = rayMarch2((p) => grid2.sample(p), origin, dir, { maxDistance: 20, stepScale: 0.9 });
  const fmt = (r: typeof analytic) =>
    r.hit
      ? `hit @ (${r.point[0].toFixed(3)}, ${r.point[1].toFixed(3)}) dist=${r.distance.toFixed(3)} steps=${r.steps}`
      : `miss after ${r.steps} steps`;
  line(`ray (${origin}) -> (${dir})`);
  line(`  analytic field : ${fmt(analytic)}`);
  line(`  sampled 64x64  : ${fmt(sampled)}`);
}
line();

/* --------------------------------- 3D ------------------------------------ */

line('=== 3D: scene = smoothUnion(sphere, box) ===');
const scene3 = smoothUnion3(0.2, sphere([-0.25, 0, 0], 0.5), box3([0.35, 0, 0], [0.3, 0.3, 0.3]));

console.time('rasterize 64^3 grid');
const grid3 = SdfGrid3D.fromSdf(scene3, { min: [-1, -1, -1], max: [1, 1, 1] }, 64, 64, 64);
console.timeEnd('rasterize 64^3 grid');

console.time('marching tetrahedra');
const mesh = extractSurface3D(grid3);
console.timeEnd('marching tetrahedra');
line(`isosurface: ${mesh.positions.length} vertices, ${mesh.triangles.length} triangles`);

line();
line('--- 3D ray marching ---');
const rays3: Array<[Vec3, Vec3]> = [
  [[0, 0, 3], [0, 0, -1]],
  [[2, 0.5, 0.5], [-1, -0.25, -0.25]],
  [[0, 5, 0], [0, 1, 0]], // miss
];
for (const [origin, dir] of rays3) {
  const analytic = rayMarch3(scene3, origin, dir, { maxDistance: 20 });
  const sampled = rayMarch3((p) => grid3.sample(p), origin, dir, { maxDistance: 20, stepScale: 0.9 });
  const fmt = (r: typeof analytic) =>
    r.hit
      ? `hit @ (${r.point[0].toFixed(3)}, ${r.point[1].toFixed(3)}, ${r.point[2].toFixed(3)}) dist=${r.distance.toFixed(3)} steps=${r.steps}`
      : `miss after ${r.steps} steps`;
  line(`ray (${origin}) -> (${dir})`);
  line(`  analytic field : ${fmt(analytic)}`);
  line(`  sampled 64^3   : ${fmt(sampled)}`);
}
