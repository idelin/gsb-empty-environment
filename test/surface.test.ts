import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sphere, box3, smoothUnion3 } from '../src/sdf.ts';
import { SdfGrid3D } from '../src/grid.ts';
import { extractSurface3D } from '../src/surface3d.ts';
import { distance3, sub3, cross3, dot3, length3 } from '../src/vec.ts';

test('marching tetrahedra extracts a sphere isosurface', () => {
  const sdf = sphere([0, 0, 0], 0.6);
  const grid = SdfGrid3D.fromSdf(sdf, { min: [-1, -1, -1], max: [1, 1, 1] }, 64, 64, 64);
  const mesh = extractSurface3D(grid);

  assert.ok(mesh.positions.length > 500, `vertices: ${mesh.positions.length}`);
  assert.ok(mesh.triangles.length > 1000, `triangles: ${mesh.triangles.length}`);

  // All vertices lie on the sphere within interpolation tolerance.
  const tol = 2 * grid.cellX;
  for (const p of mesh.positions) {
    assert.ok(Math.abs(distance3(p, [0, 0, 0]) - 0.6) < tol, `vertex ${p} off surface`);
  }

  // Every triangle index is valid.
  for (const [a, b, c] of mesh.triangles) {
    assert.ok(a < mesh.positions.length && b < mesh.positions.length && c < mesh.positions.length);
  }

  // Welded mesh is edge-manifold: each undirected edge appears exactly twice.
  const edges = new Map<string, number>();
  for (const [a, b, c] of mesh.triangles) {
    for (const [u, v] of [[a, b], [b, c], [c, a]]) {
      const key = u < v ? `${u}_${v}` : `${v}_${u}`;
      edges.set(key, (edges.get(key) ?? 0) + 1);
    }
  }
  for (const [key, n] of edges) {
    assert.equal(n, 2, `edge ${key} shared by ${n} triangles (expected 2)`);
  }
});

test('isosurface of a smooth-union composite stays on the field zero set', () => {
  const sdf = smoothUnion3(0.2, sphere([-0.3, 0, 0], 0.4), box3([0.3, 0, 0], [0.3, 0.3, 0.3]));
  const grid = SdfGrid3D.fromSdf(sdf, { min: [-1, -1, -1], max: [1, 1, 1] }, 64, 64, 64);
  const mesh = extractSurface3D(grid);
  assert.ok(mesh.triangles.length > 0);
  // Vertices sit on the analytic zero set within interpolation tolerance
  // (tet edges along cube diagonals cut the trilinear field, not the
  // analytic one, so the bound is O(cell^2), not exact).
  const tol = 2 * grid.cellX;
  for (const p of mesh.positions) {
    assert.ok(Math.abs(sdf(p)) < tol, `vertex ${p} has sdf=${sdf(p)}`);
  }
});

test('triangle normals point outward (away from the negative interior)', () => {
  const sdf = sphere([0, 0, 0], 0.6);
  const grid = SdfGrid3D.fromSdf(sdf, { min: [-1, -1, -1], max: [1, 1, 1] }, 32, 32, 32);
  const mesh = extractSurface3D(grid);
  let outward = 0;
  for (const [a, b, c] of mesh.triangles) {
    const n = cross3(sub3(mesh.positions[b], mesh.positions[a]), sub3(mesh.positions[c], mesh.positions[a]));
    if (length3(n) === 0) continue;
    // Sphere gradient at the centroid is the centroid itself (outward).
    const centroid = mesh.positions[a];
    if (dot3(n, centroid) > 0) outward++;
  }
  assert.ok(outward / mesh.triangles.length > 0.95, `${outward}/${mesh.triangles.length} outward`);
});
