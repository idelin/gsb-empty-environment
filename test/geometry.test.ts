import assert from "node:assert/strict";
import test from "node:test";
import {
  connectedComponentAreas,
  feasibleComponentAreas,
  pointInRegion,
  polygonRegion,
} from "../src/geometry.ts";
import type { RegionLike } from "../src/geometry.ts";
import { rationalToString } from "../src/rational.ts";

function square(x: number, y: number, width: number, height: number): number[][] {
  return [
    [x, y],
    [x + width, y],
    [x + width, y + height],
    [x, y + height],
  ];
}

function latticeComponentAreas(
  predicate: (x: number, y: number) => boolean,
  bounds: { minX: number; maxX: number; minY: number; maxY: number },
): number[] {
  const width = bounds.maxX - bounds.minX;
  const height = bounds.maxY - bounds.minY;
  const filled = new Uint8Array(width * height);
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      filled[y * width + x] = predicate(bounds.minX + x, bounds.minY + y) ? 1 : 0;
    }
  }
  const already = new Uint8Array(width * height);
  const areas: number[] = [];
  for (let start = 0; start < filled.length; start += 1) {
    if (filled[start] === 0 || already[start] === 1) {
      continue;
    }
    const queue = [start];
    already[start] = 1;
    let area = 0;
    for (let cursor = 0; cursor < queue.length; cursor += 1) {
      const index = queue[cursor]!;
      area += 1;
      const x = index % width;
      const y = Math.floor(index / width);
      for (const [dx, dy] of [
        [1, 0],
        [-1, 0],
        [0, 1],
        [0, -1],
      ] as const) {
        const nextX = x + dx;
        const nextY = y + dy;
        if (nextX < 0 || nextX >= width || nextY < 0 || nextY >= height) {
          continue;
        }
        const next = nextY * width + nextX;
        if (filled[next] === 1 && already[next] === 0) {
          already[next] = 1;
          queue.push(next);
        }
      }
    }
    areas.push(area);
  }
  return areas.sort((left, right) => left - right);
}

function exact(areas: readonly { n: bigint; d: bigint }[]): string[] {
  return areas.map(rationalToString);
}

test("integer lattice reference agrees on overlapping holes", () => {
  const first = polygonRegion(square(0, 0, 10, 10), [square(3, 3, 4, 4)]);
  const second = polygonRegion(square(2, 2, 7, 7), [square(4, 4, 4, 4)]);
  const expected = latticeComponentAreas(
    (x, y) =>
      pointInRegion(
        {
          x: `${2 * x + 1}/2`,
          y: `${2 * y + 1}/2`,
        },
        first,
      ) &&
      pointInRegion(
        {
          x: `${2 * x + 1}/2`,
          y: `${2 * y + 1}/2`,
        },
        second,
      ),
    { minX: 0, maxX: 10, minY: 0, maxY: 10 },
  );
  const actual = feasibleComponentAreas([first, second]);
  assert.deepEqual(exact(actual), expected.map(String));
});

test("lattice reference agrees on a hole containing a separate island", () => {
  const shell = polygonRegion(square(0, 0, 10, 10), [square(2, 2, 6, 6)]);
  const island = polygonRegion(square(4, 4, 2, 2));
  const region = {
    polygons: [...shell.polygons, ...island.polygons],
  } as const;
  const expected = latticeComponentAreas(
    (x, y) =>
      pointInRegion(
        {
          x: `${2 * x + 1}/2`,
          y: `${2 * y + 1}/2`,
        },
        region as RegionLike,
      ),
    {
      minX: 0,
      maxX: 10,
      minY: 0,
      maxY: 10,
    },
  );
  assert.deepEqual(exact(connectedComponentAreas(region)), expected.map(String));
});

test("point contact does not join components while edge contact does", () => {
  const pointTouch = {
    polygons: [
      ...polygonRegion(square(0, 0, 2, 2)).polygons,
      ...polygonRegion(square(2, 2, 2, 2)).polygons,
    ],
  };
  assert.deepEqual(exact(connectedComponentAreas(pointTouch)), ["4", "4"]);

  const edgeTouch = {
    polygons: [
      ...polygonRegion(square(0, 0, 2, 2)).polygons,
      ...polygonRegion(square(2, 0, 2, 2)).polygons,
    ],
  };
  assert.deepEqual(exact(connectedComponentAreas(edgeTouch)), ["8"]);

  const diagonalEdgeTouch = {
    polygons: [
      { outer: [[0, 0], [2, 0], [2, 2]] },
      { outer: [[0, 0], [2, 2], [0, 2]] },
    ],
  };
  assert.deepEqual(exact(connectedComponentAreas(diagonalEdgeTouch)), ["4"]);
});

test("a hole ending at a point contact does not join two region components", () => {
  const masked = polygonRegion(square(4, 3, 4, 6), [square(5, 4, 2, 1)]);
  const union = {
    polygons: [
      { outer: square(4, 1, 2, 7) },
      { outer: square(3, 1, 6, 4), holes: [square(4, 2, 4, 2)] },
      { outer: square(4, 2, 3, 7) },
    ],
  };
  const expected = latticeComponentAreas(
    (x, y) => {
      const point = { x: `${2 * x + 1}/2`, y: `${2 * y + 1}/2` };
      return pointInRegion(point, masked) && pointInRegion(point, union);
    },
    { minX: 0, maxX: 10, minY: 0, maxY: 10 },
  );
  assert.deepEqual(expected, [1, 16]);
  assert.deepEqual(exact(feasibleComponentAreas([masked, union])), ["1", "16"]);
});

test("a zero-area point intersection has no admissible component", () => {
  const areas = feasibleComponentAreas([
    polygonRegion(square(0, 0, 1, 1)),
    polygonRegion(square(1, 1, 1, 1)),
  ]);
  assert.deepEqual(areas, []);
});

test("extremely narrow rational intersections stay exact", () => {
  const wide = polygonRegion(square(0, 0, 4, 4));
  const narrow = polygonRegion([
    ["3999/2000", 0],
    ["4001/2000", 0],
    ["4001/2000", 4],
    ["3999/2000", 4],
  ]);
  assert.deepEqual(exact(feasibleComponentAreas([wide, narrow])), ["1/250"]);
});

test("non-integer intersection vertices are measured without floating point", () => {
  const left = polygonRegion([
    [0, 0],
    [3, 0],
    [0, 3],
  ]);
  const right = polygonRegion([
    [1, 0],
    [4, 0],
    [4, 3],
  ]);
  assert.deepEqual(exact(feasibleComponentAreas([left, right])), ["1"]);
});

test("multiple feasible components are sorted by exact area", () => {
  const mask = {
    polygons: [
      { outer: square(0, 0, 2, 2) },
      { outer: square(10, 0, 1, 2) },
    ],
  };
  const strip = polygonRegion(square(0, 0, 20, 2));
  assert.deepEqual(exact(feasibleComponentAreas([mask, strip])), ["2", "4"]);
});
