import { Delaunay } from "../src/delaunay.ts";
import { dist2 } from "../src/geometry.ts";
import type { Point } from "../src/geometry.ts";

function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// 50 random survey points in a 1000 x 1000 m survey area
const rand = mulberry32(20261003);
const points: Point[] = Array.from({ length: 50 }, () => ({
  x: rand() * 1000,
  y: rand() * 1000,
}));

const delaunay = new Delaunay();
for (const p of points) delaunay.insert(p); // dynamic point-by-point insertion
const mesh = delaunay.mesh();

const n = mesh.points.length;
const h = mesh.hull.length;
const t = mesh.triangles.length;

console.log("=== Delaunay Triangulation Demo ===");
console.log(`points:              ${n}`);
console.log(`triangles:           ${t}  (Euler check 2n-2-h = ${2 * n - 2 - h})`);
console.log(`convex hull vertices:${h > 0 ? " " + h : " 0"}`);
console.log(`hull (CCW indices):  [${mesh.hull.join(", ")}]`);

// --- verify the empty circumcircle property -------------------------------
let violations = 0;
let worst = 0;
for (const tri of mesh.triangles) {
  const r2 = tri.circumradius * tri.circumradius;
  for (let i = 0; i < n; i++) {
    if (tri.vertices.includes(i)) continue;
    const d2 = dist2(mesh.points[i], tri.circumcenter);
    if (d2 < r2) {
      violations++;
      worst = Math.max(worst, 1 - d2 / r2);
    }
  }
}
console.log(`\nempty-circumcircle violations: ${violations} (worst relative depth: ${worst.toExponential(2)})`);

// --- mesh quality: minimum interior angle ---------------------------------
let minAngle = Infinity;
for (const tri of mesh.triangles) {
  const [a, b, c] = tri.vertices.map((i) => mesh.points[i]);
  const lens = [
    Math.hypot(b.x - c.x, b.y - c.y),
    Math.hypot(a.x - c.x, a.y - c.y),
    Math.hypot(a.x - b.x, a.y - b.y),
  ];
  for (let k = 0; k < 3; k++) {
    const cos =
      (lens[(k + 1) % 3] ** 2 + lens[(k + 2) % 3] ** 2 - lens[k] ** 2) /
      (2 * lens[(k + 1) % 3] * lens[(k + 2) % 3]);
    minAngle = Math.min(minAngle, Math.acos(Math.max(-1, Math.min(1, cos))));
  }
}
console.log(`minimum interior angle:        ${((minAngle * 180) / Math.PI).toFixed(2)} deg (Delaunay maximizes it)`);

// --- adjacency sample ------------------------------------------------------
console.log("\nadjacency graph (first 5 triangles):");
mesh.triangles.slice(0, 5).forEach((tri, i) => {
  console.log(
    `  T${i}  vertices=[${tri.vertices.join(", ")}]  neighbors=[${tri.neighbors.join(", ")}]`,
  );
});

// --- boundary edges --------------------------------------------------------
const boundary = mesh.triangles.reduce(
  (acc, tri) => acc + tri.neighbors.filter((x) => x === -1).length,
  0,
);
console.log(`\nboundary edges: ${boundary} (== hull vertex count: ${boundary === h})`);
