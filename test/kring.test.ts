import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  gridDisk,
  gridDiskDistances,
  gridRing,
  expectedDiskSize,
} from '../src/kring.ts';
import {
  latLngToCell,
  cellToFaceIJ,
  cellToChildren,
  faceCenterCell,
  getResolution,
  isValidCell,
  resolveFaceIJ,
} from '../src/cell.ts';
import { DIRECTIONS, ijAdd } from '../src/hexmath.ts';

const BEIJING = { lat: 39.9042, lng: 116.4074 };

function neighborsOf(cell: string): string[] {
  return gridRing(cell, 1);
}

test('acceptance: k=3 ring around a res-8 cell has exactly 37 cells', () => {
  const center = latLngToCell(BEIJING.lat, BEIJING.lng, 8);
  const disk = gridDisk(center, 3);
  assert.equal(disk.length, 37);
  assert.equal(disk.length, expectedDiskSize(3));
  assert.equal(new Set(disk).size, disk.length, 'no duplicate cells');
  assert.ok(disk.includes(center));
  for (const cell of disk) {
    assert.ok(isValidCell(cell));
    assert.equal(getResolution(cell), 8, 'all neighbours at same resolution');
  }
});

test('ring sizes follow 6k for interior cells', () => {
  const center = latLngToCell(BEIJING.lat, BEIJING.lng, 8);
  assert.equal(gridRing(center, 0).length, 1);
  assert.equal(gridRing(center, 1).length, 6);
  assert.equal(gridRing(center, 2).length, 12);
  assert.equal(gridRing(center, 3).length, 18);
});

test('disk is contiguous and closed: every cell reachable from centre', () => {
  const center = latLngToCell(BEIJING.lat, BEIJING.lng, 8);
  const disk = new Set(gridDisk(center, 3));
  // BFS within the set from the centre must cover every cell (no holes)
  const visited = new Set([center]);
  const queue = [center];
  while (queue.length > 0) {
    const cur = queue.pop()!;
    for (const n of neighborsOf(cur)) {
      if (disk.has(n) && !visited.has(n)) {
        visited.add(n);
        queue.push(n);
      }
    }
  }
  assert.equal(visited.size, disk.size, 'disk has holes');
  // closure: every ring-1 neighbour of a disk cell lies within disk(k+1)
  const bigger = new Set(gridDisk(center, 4));
  for (const cell of disk) {
    for (const n of neighborsOf(cell)) {
      assert.ok(bigger.has(n), `neighbour ${n} escaped the k+1 disk`);
    }
  }
});

test('adjacency is symmetric and distances are consistent', () => {
  const center = latLngToCell(BEIJING.lat, BEIJING.lng, 8);
  const entries = gridDiskDistances(center, 3);
  for (const { cell, distance } of entries) {
    if (distance === 0) continue;
    // every cell at distance d touches some cell at distance d-1
    const ns = gridDiskDistances(cell, 1);
    const minBack = Math.min(
      ...ns.map((e) => {
        const found = entries.find((x) => x.cell === e.cell);
        return found ? found.distance : 99;
      }),
    );
    assert.equal(minBack, distance - 1);
  }
});

test('k-ring traversal uses no floating-point trigonometry', () => {
  const center = latLngToCell(BEIJING.lat, BEIJING.lng, 8);
  const names = [
    'sin', 'cos', 'tan', 'asin', 'acos', 'atan', 'atan2',
    'sinh', 'cosh', 'tanh', 'exp', 'log', 'log2', 'log10', 'pow', 'sqrt',
  ] as const;
  const originals = new Map<string, unknown>();
  for (const name of names) {
    originals.set(name, (Math as Record<string, unknown>)[name]);
    (Math as Record<string, unknown>)[name] = () => {
      throw new Error(`Math.${name} called during K-ring traversal`);
    };
  }
  try {
    const disk = gridDisk(center, 3);
    assert.equal(disk.length, 37);
    assert.equal(gridRing(center, 3).length, 18);
  } finally {
    for (const name of names) {
      (Math as Record<string, unknown>)[name] = originals.get(name);
    }
  }
});

test('face-crossing disks stay exact: 37 cells across icosa edges', () => {
  // enumerate res-4 cells of face 0; cells whose neighbourhood crosses
  // an icosa edge (but avoids the vertex defect gap) must yield full disks
  const root = faceCenterCell(0, 0);
  const all = cellToChildren(root, 4);
  let checked = 0;
  for (const cell of all) {
    const fj = cellToFaceIJ(cell);
    const faces = new Set<number>();
    let hasNull = false;
    for (const d of DIRECTIONS) {
      const r = resolveFaceIJ(fj.face, ijAdd(fj.ij, d), fj.res);
      if (!r) hasNull = true;
      else faces.add(r.face);
    }
    if (hasNull || faces.size < 2) continue; // not an edge-crossing cell
    // skip cells within 3 rings of the vertex defect gap
    const nearDefect = gridDisk(cell, 3).some((dc) => {
      const df = cellToFaceIJ(dc);
      return DIRECTIONS.some(
        (d) => resolveFaceIJ(df.face, ijAdd(df.ij, d), df.res) === null,
      );
    });
    if (nearDefect) continue;
    const disk = gridDisk(cell, 3);
    assert.equal(disk.length, 37, `edge-crossing disk of ${cell}`);
    assert.equal(new Set(disk).size, 37);
    checked++;
    if (checked >= 25) break;
  }
  assert.ok(checked >= 10, `only ${checked} edge-crossing cells exercised`);
});

test('vertex defect: disk stays contiguous, duplicate-free and bounded', () => {
  // deterministically locate cells adjacent to the defect gap
  const root = faceCenterCell(0, 0);
  const all = cellToChildren(root, 4);
  const defectCells = all.filter((cell) => {
    const fj = cellToFaceIJ(cell);
    return DIRECTIONS.some(
      (d) => resolveFaceIJ(fj.face, ijAdd(fj.ij, d), fj.res) === null,
    );
  });
  assert.ok(defectCells.length > 0, 'no defect cells found');
  for (const defectCell of defectCells.slice(0, 20)) {
    const disk = gridDisk(defectCell, 3);
    assert.ok(disk.length > 0 && disk.length <= 37, `size ${disk.length}`);
    assert.equal(new Set(disk).size, disk.length, 'no duplicates');
    // contiguity even across the defect
    const set = new Set(disk);
    const visited = new Set([defectCell]);
    const queue = [defectCell];
    while (queue.length > 0) {
      const cur = queue.pop()!;
      for (const n of gridRing(cur, 1)) {
        if (set.has(n) && !visited.has(n)) {
          visited.add(n);
          queue.push(n);
        }
      }
    }
    assert.equal(visited.size, disk.length, 'defect disk is not contiguous');
  }
});

test('k=0 disk is just the centre; invalid k rejected', () => {
  const center = latLngToCell(BEIJING.lat, BEIJING.lng, 8);
  assert.deepEqual(gridDisk(center, 0), [center]);
  assert.throws(() => gridDisk(center, -1));
  assert.throws(() => gridDisk(center, 1.5));
});
