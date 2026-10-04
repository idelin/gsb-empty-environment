import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ContourExtractor,
  extractContours,
  extractSegments,
} from '../src/index.ts';
import type { RationalPoint, Segment } from '../src/index.ts';

const pt = (p: RationalPoint): string => `${p.x.toString()},${p.y.toString()}`;
const pathStr = (path: ReadonlyArray<RationalPoint>): string[] =>
  path.map(pt);
const hasPair = (segments: ReadonlyArray<Segment>, p: string, q: string) =>
  segments.some(
    (s) =>
      (pt(s.a) === p && pt(s.b) === q) || (pt(s.a) === q && pt(s.b) === p),
  );

test('exact rational interpolation on a single cell', () => {
  // values: (0,0)=0 (1,0)=3 / (0,1)=0 (1,1)=0, threshold 1
  const { chains, rings } = extractContours([[0, 3], [0, 0]], 1);
  assert.equal(rings.length, 0);
  assert.equal(chains.length, 1);
  assert.deepEqual(pathStr(chains[0]), ['1/3,0', '1,2/3']);
});

test('closed ring around a single hot vertex', () => {
  const grid = [
    [0, 0, 0],
    [0, 10, 0],
    [0, 0, 0],
  ];
  const { chains, rings } = extractContours(grid, 5);
  assert.equal(chains.length, 0);
  assert.equal(rings.length, 1);
  assert.deepEqual(
    [...pathStr(rings[0])].sort(),
    ['1,1/2', '1,3/2', '1/2,1', '3/2,1'],
  );
});

test('open chain endpoints lie on the grid boundary', () => {
  const { chains, rings } = extractContours([[10, 0], [0, 0]], 5);
  assert.equal(rings.length, 0);
  assert.equal(chains.length, 1);
  assert.deepEqual(pathStr(chains[0]), ['0,1/2', '1/2,0']);
});

test('shared edge crossings are computed exactly once', () => {
  const grid = [
    [0, 0, 0, 0],
    [0, 9, 9, 0],
    [0, 9, 9, 0],
    [0, 0, 0, 0],
  ];
  const extractor = new ContourExtractor(grid, 5);
  const segments = [...extractor.generateSegments()];
  assert.ok(segments.length > 0);

  // Independently count distinct crossed edges.
  let expected = 0;
  for (let y = 0; y < 4; y++) {
    for (let x = 0; x < 4; x++) {
      if (x + 1 < 4 && grid[y][x] > 5 !== grid[y][x + 1] > 5) expected++;
      if (y + 1 < 4 && grid[y][x] > 5 !== grid[y + 1][x] > 5) expected++;
    }
  }
  assert.equal(extractor.interpolationCount, expected);
});

test('no duplicate segments on shared edges', () => {
  const grid = [
    [0, 5, 0],
    [5, 10, 5],
    [0, 5, 0],
  ];
  const segments = extractSegments(grid, 3);
  const keys = segments.map((s) =>
    s.aKey < s.bKey ? `${s.aKey}|${s.bKey}` : `${s.bKey}|${s.aKey}`,
  );
  assert.equal(new Set(keys).size, keys.length);
});

test('saddle resolved by bilinear center above threshold', () => {
  // a=c=10, b=d=1, T=5: center sum 22 > 20 -> center inside -> wrap b and d.
  const segments = extractSegments([[10, 1], [1, 10]], 5);
  assert.equal(segments.length, 2);
  assert.ok(hasPair(segments, '5/9,0', '1,4/9')); // wraps corner b
  assert.ok(hasPair(segments, '0,5/9', '4/9,1')); // wraps corner d
});

test('saddle resolved by bilinear center below threshold', () => {
  // a=c=10, b=d=-1, T=5: center sum 18 < 20 -> center outside -> wrap a and c.
  const segments = extractSegments([[10, -1], [-1, 10]], 5);
  assert.equal(segments.length, 2);
  assert.ok(hasPair(segments, '0,5/11', '5/11,0')); // wraps corner a
  assert.ok(hasPair(segments, '1,6/11', '6/11,1')); // wraps corner c
});

test('saddle tie uses the fixed diagonal rule (case 5)', () => {
  // a=c=10, b=d=0, T=5: center sum 20 == 4*T -> fixed rule wraps b and d.
  const segments = extractSegments([[10, 0], [0, 10]], 5);
  assert.equal(segments.length, 2);
  assert.ok(hasPair(segments, '1/2,0', '1,1/2')); // wraps corner b
  assert.ok(hasPair(segments, '0,1/2', '1/2,1')); // wraps corner d
});

test('saddle tie uses the fixed diagonal rule (case 10)', () => {
  // b=d=10, a=c=0, T=5: tie -> fixed rule wraps b and d (same diagonal).
  const segments = extractSegments([[0, 10], [10, 0]], 5);
  assert.equal(segments.length, 2);
  assert.ok(hasPair(segments, '1/2,0', '1,1/2')); // wraps corner b
  assert.ok(hasPair(segments, '0,1/2', '1/2,1')); // wraps corner d
});

test('iso-valued vertex produces no dangling duplicate segments', () => {
  // Center vertex equals the threshold; north and south are inside.
  const grid = [
    [0, 10, 0],
    [0, 5, 0],
    [0, 10, 0],
  ];
  const segments = extractSegments(grid, 5);
  // No zero-length segments.
  for (const s of segments) assert.notEqual(s.aKey, s.bKey);
  // No duplicate segments.
  const keys = segments.map((s) =>
    s.aKey < s.bKey ? `${s.aKey}|${s.bKey}` : `${s.bKey}|${s.aKey}`,
  );
  assert.equal(new Set(keys).size, keys.length);

  const { chains, rings } = extractContours(grid, 5);
  assert.equal(rings.length, 0);
  assert.equal(chains.length, 2);
  for (const chain of chains) {
    assert.equal(chain.length, 3);
    assert.equal(pt(chain[1]), '1,1'); // passes through the iso vertex
  }
});

test('pinch at an iso-valued vertex leaves no degenerate segments', () => {
  // Center == threshold, all four neighbors inside: the contour pinches
  // through the center vertex; the degenerate zero-length segments there
  // must be dropped, leaving four clean boundary chains.
  const grid = [
    [0, 10, 0],
    [10, 5, 10],
    [0, 10, 0],
  ];
  const segments = extractSegments(grid, 5);
  assert.equal(segments.length, 4);
  for (const s of segments) assert.notEqual(s.aKey, s.bKey);
  const { chains, rings } = extractContours(grid, 5);
  assert.equal(rings.length, 0);
  assert.equal(chains.length, 4);
  for (const chain of chains) assert.equal(chain.length, 2);
});

test('flat edge at threshold between two inside regions is emitted once', () => {
  // Row 0 inside, row 1 exactly at threshold, row 2 inside: the contour
  // along the plateau would be generated from both sides; it must appear
  // only once.
  const grid = [
    [10, 10, 10],
    [5, 5, 5],
    [10, 10, 10],
  ];
  const segments = extractSegments(grid, 5);
  assert.equal(segments.length, 2);
  const { chains, rings } = extractContours(grid, 5);
  assert.equal(rings.length, 0);
  assert.equal(chains.length, 1);
  assert.deepEqual(pathStr(chains[0]), ['0,1', '1,1', '2,1']);
});

test('flat boundary edge yields a single clean chain', () => {
  const grid = [
    [5, 5, 5, 5],
    [10, 10, 10, 10],
  ];
  const { chains, rings } = extractContours(grid, 5);
  assert.equal(rings.length, 0);
  assert.equal(chains.length, 1);
  assert.deepEqual(pathStr(chains[0]), ['0,0', '1,0', '2,0', '3,0']);
});

test('fuzz: invariants hold on random integer grids', () => {
  let seed = 0x2f6e2b1;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed;
  };

  for (let iter = 0; iter < 200; iter++) {
    const w = 2 + (rand() % 7);
    const h = 2 + (rand() % 7);
    const threshold = rand() % 4;
    const grid: number[][] = [];
    for (let y = 0; y < h; y++) {
      const row: number[] = [];
      for (let x = 0; x < w; x++) row.push(rand() % 5);
      grid.push(row);
    }

    const extractor = new ContourExtractor(grid, threshold);
    const segments = [...extractor.generateSegments()];

    // 1. Shared edge crossings are computed exactly once.
    let crossedEdges = 0;
    for (let y = 0; y < h; y++) {
      for (let x = 0; x < w; x++) {
        if (x + 1 < w && grid[y][x] > threshold !== grid[y][x + 1] > threshold)
          crossedEdges++;
        if (y + 1 < h && grid[y][x] > threshold !== grid[y + 1][x] > threshold)
          crossedEdges++;
      }
    }
    assert.equal(extractor.interpolationCount, crossedEdges);

    // 2. No zero-length or duplicate segments.
    const keys = new Set<string>();
    for (const s of segments) {
      assert.notEqual(s.aKey, s.bKey);
      const key =
        s.aKey < s.bKey ? `${s.aKey}|${s.bKey}` : `${s.bKey}|${s.aKey}`;
      assert.ok(!keys.has(key), `duplicate segment ${key}`);
      keys.add(key);
    }

    // 3. Joining consumes every segment exactly once.
    const { chains, rings } = extractContours(grid, threshold);
    const total =
      chains.reduce((n, c) => n + c.length - 1, 0) +
      rings.reduce((n, r) => n + r.length, 0);
    assert.equal(total, segments.length);

    // 4. Well-formed paths.
    for (const chain of chains) assert.ok(chain.length >= 2);
    for (const ring of rings) {
      assert.ok(ring.length >= 3);
      const first = ring[0];
      const last = ring[ring.length - 1];
      assert.ok(!(first.x.equals(last.x) && first.y.equals(last.y)));
    }

    // 5. All points stay within the grid bounds.
    for (const path of [...chains, ...rings]) {
      for (const p of path) {
        assert.ok(p.x.toNumber() >= 0 && p.x.toNumber() <= w - 1);
        assert.ok(p.y.toNumber() >= 0 && p.y.toNumber() <= h - 1);
      }
    }
  }
});

test('flat buffer grid input is supported', () => {
  const grid = { data: [0, 0, 0, 0, 10, 0, 0, 0, 0], width: 3, height: 3 };
  const { rings } = extractContours(grid, 5n);
  assert.equal(rings.length, 1);
  assert.equal(rings[0].length, 4);
});
