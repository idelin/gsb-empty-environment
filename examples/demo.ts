/**
 * Runnable demo: rasterize geometry into a 64x64 SDF grid, render it as
 * ASCII art, extract the isosurface, and sphere-trace a ray onto it.
 *
 *   node examples/demo.ts
 */

import { circle, box2, sphere, union, smoothUnion } from "../src/geometry.ts";
import { SDFGrid2D, SDFGrid3D } from "../src/grid.ts";
import { rayMarch2D, rayMarch3D } from "../src/raymarch.ts";

// --- 2D: 64x64 grid of a compound shape ------------------------------------

const shape = smoothUnion(circle([-0.55, 0.35], 0.7), box2([0.6, -0.4], [0.45, 0.45]), 0.3);
const grid = new SDFGrid2D(64, 64, { min: [-2, -2], max: [2, 2] }).rasterize(shape);

console.log("64x64 SDF grid (downsampled to 48x24, '#' = inside):\n");
const cols = 48;
const rows = 24;
for (let r = 0; r < rows; r++) {
  let line = "";
  for (let c = 0; c < cols; c++) {
    const x = -2 + (4 * c) / (cols - 1);
    const y = 2 - (4 * r) / (rows - 1);
    const d = grid.sample([x, y]);
    line += d < 0 ? "#" : d < 0.15 ? "+" : d < 0.5 ? "." : " ";
  }
  console.log(line);
}

// --- Isosurface extraction --------------------------------------------------

const contour = grid.extractIsosurface();
console.log(`\nExtracted zero contour: ${contour.length} segments`);

// --- 2D ray marching ---------------------------------------------------------

const sdf2 = (p: [number, number]) => grid.sample(p);
const origin: [number, number] = [-1.9, 1.6];
const dir: [number, number] = [1, -0.75];
const hit = rayMarch2D(sdf2, origin, dir);
console.log(
  `\n2D ray march from (${origin}) along (${dir}):` +
    `\n  hit=${hit.hit} steps=${hit.steps} distance=${hit.distance.toFixed(4)}` +
    `\n  point=(${hit.point[0].toFixed(3)}, ${hit.point[1].toFixed(3)}) residual SDF=${hit.finalSDF.toExponential(2)}`,
);

// --- 3D: 64^3 grid + ray march ----------------------------------------------

const blob = union(sphere([-0.5, 0, 0], 0.7), sphere([0.6, 0.2, 0], 0.5));
const grid3 = new SDFGrid3D(64, 64, 64, {
  min: [-2, -2, -2],
  max: [2, 2, 2],
}).rasterize(blob);

const sdf3 = (p: [number, number, number]) => grid3.sample(p);
const hit3 = rayMarch3D(sdf3, [0, 0.15, 1.9], [0, 0, -1]);
console.log(
  `\n3D ray march (64^3 grid):` +
    `\n  hit=${hit3.hit} steps=${hit3.steps} distance=${hit3.distance.toFixed(4)}` +
    `\n  point=(${hit3.point.map((v) => v.toFixed(3)).join(", ")})`,
);

const surface = grid3.extractIsosurface();
console.log(`\nExtracted 3D isosurface: ${surface.length} triangles`);
