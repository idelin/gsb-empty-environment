/**
 * Topological K-ring neighbourhood traversal.
 *
 * The traversal is a pure integer computation: cells are decoded to
 * (face, IJ) coordinates, expanded with the 6 unit directions, and
 * mapped across icosahedron edges with precomputed integer affine
 * transforms.  No floating point and no trigonometry is involved.
 */

import { DIRECTIONS, ijAdd } from './hexmath.ts';
import type { IJ } from './hexmath.ts';
import {
  cellFromFaceIJ,
  cellToFaceIJ,
  resolveFaceIJ,
} from './cell.ts';

export interface DiskEntry {
  cell: string;
  distance: number;
}

/**
 * All cells within topological distance `k` of `cell` (including the
 * centre), in BFS order.  Interior cells yield exactly
 * 1 + 3*k*(k+1) cells; near icosahedron vertices the pentagon defect
 * yields fewer, but the set stays contiguous and duplicate-free.
 */
export function gridDisk(cell: string, k: number): string[] {
  return gridDiskDistances(cell, k).map((e) => e.cell);
}

/** Like {@link gridDisk} but also reports the ring distance per cell. */
export function gridDiskDistances(cell: string, k: number): DiskEntry[] {
  if (!Number.isInteger(k) || k < 0) {
    throw new Error(`invalid k: ${k}`);
  }
  const center = cellToFaceIJ(cell);
  const seen = new Set<string>();
  const out: DiskEntry[] = [];
  const key = (face: number, ij: IJ) => `${face}:${ij.i}:${ij.j}`;
  let frontier: { face: number; ij: IJ }[] = [{ face: center.face, ij: center.ij }];
  seen.add(key(center.face, center.ij));
  out.push({ cell, distance: 0 });
  for (let step = 1; step <= k; step++) {
    const next: { face: number; ij: IJ }[] = [];
    for (const cur of frontier) {
      for (const dir of DIRECTIONS) {
        const candidate = ijAdd(cur.ij, dir);
        const resolved = resolveFaceIJ(cur.face, candidate, center.res);
        if (!resolved) continue; // vertex defect gap: no neighbour here
        const id = key(resolved.face, resolved.ij);
        if (seen.has(id)) continue;
        seen.add(id);
        next.push(resolved);
        out.push({
          cell: cellFromFaceIJ(resolved.face, resolved.ij, center.res),
          distance: step,
        });
      }
    }
    frontier = next;
  }
  return out;
}

/** Cells at exact topological distance `k` (the k-th ring). */
export function gridRing(cell: string, k: number): string[] {
  if (k === 0) return [cell];
  return gridDiskDistances(cell, k)
    .filter((e) => e.distance === k)
    .map((e) => e.cell);
}

/** Expected disk size for a defect-free interior cell: 1 + 3k(k+1). */
export function expectedDiskSize(k: number): number {
  return 1 + 3 * k * (k + 1);
}
