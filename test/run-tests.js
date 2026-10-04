'use strict';
const assert = require('assert/strict');
const { rasterize, Rat } = require('../dist/index.js');

let passed = 0;
function test(name, fn) {
  try {
    fn();
    passed++;
    console.log(`ok - ${name}`);
  } catch (err) {
    console.error(`FAIL - ${name}`);
    console.error(err);
    process.exitCode = 1;
  }
}

function maskFrom(rows) {
  return rows.map((r) => r.split('').map((c) => (c === '#' ? 1 : 0)));
}

function assertMask(mask, rows, label) {
  const expected = maskFrom(rows);
  assert.equal(mask.height, expected.length, `${label}: height`);
  assert.equal(mask.width, expected[0].length, `${label}: width`);
  for (let y = 0; y < expected.length; y++) {
    for (let x = 0; x < expected[0].length; x++) {
      assert.equal(
        mask.at(x, y),
        expected[y][x],
        `${label}: pixel (${x},${y})\nactual:\n${mask}\n`,
      );
    }
  }
}

// ---------- rational arithmetic ----------

test('Rat: exact conversion and normalization', () => {
  assert.equal(Rat.fromNumber(0.5).toString(), '1/2');
  assert.equal(Rat.fromNumber(0.1).toString(), '3602879701896397/36028797018963968');
  assert.equal(Rat.fromNumber(-3).toString(), '-3');
  assert.equal(Rat.of(6n, -8n).toString(), '-3/4');
  assert.equal(Rat.of(0n, 5n).toString(), '0');
});

test('Rat: exact arithmetic', () => {
  // 0.1 + 0.2 !== 0.3 in doubles, but exact rationals compare correctly.
  const a = Rat.fromNumber(0.1).add(Rat.fromNumber(0.2));
  const b = Rat.fromNumber(0.3);
  assert.notEqual(a.toString(), b.toString()); // inputs are dyadic approximations
  const third = Rat.of(1n, 3n);
  assert.equal(third.add(third).add(third).toString(), '1');
  assert.equal(Rat.of(7n, 3n).cmp(Rat.of(2n, 1n)), 1);
  assert.equal(Rat.of(3n, 2n).isInteger(), false);
  assert.equal(Rat.of(4n, 2n).isInteger(), true);
});

// ---------- simple polygons ----------

test('axis-aligned square, integer vertices', () => {
  const r = rasterize(
    [
      [1, 1],
      [4, 1],
      [4, 4],
      [1, 4],
    ],
    { width: 6, height: 6 },
  );
  const expected = [
    '......',
    '.###..',
    '.###..',
    '.###..',
    '......',
    '......',
  ];
  assertMask(r.evenOdd, expected, 'evenOdd');
  assertMask(r.nonZero, expected, 'nonZero');
  assertMask(r.diff, [
    '......',
    '......',
    '......',
    '......',
    '......',
    '......',
  ], 'diff');
});

test('boundary-inclusive: edges through pixel centers', () => {
  // Square whose edges all pass exactly through pixel centers.
  const r = rasterize(
    [
      [0.5, 0.5],
      [2.5, 0.5],
      [2.5, 2.5],
      [0.5, 2.5],
    ],
    { width: 4, height: 4 },
  );
  const expected = [
    '###.',
    '###.',
    '###.',
    '....',
  ];
  assertMask(r.evenOdd, expected, 'evenOdd');
  assertMask(r.nonZero, expected, 'nonZero');
});

test('horizontal edges on scanlines + vertex exactly on pixel center', () => {
  // Apex (1.5, 2.5) is a pixel center and a local maximum in y, so it is
  // excluded by the half-open crossing rule and must be caught by the
  // exact boundary rule instead.
  const r = rasterize(
    [
      [0.5, 0.5],
      [2.5, 0.5],
      [1.5, 2.5],
    ],
    { width: 4, height: 4 },
  );
  const expected = [
    '###.',
    '.#..',
    '.#..',
    '....',
  ];
  assertMask(r.evenOdd, expected, 'evenOdd');
  assertMask(r.nonZero, expected, 'nonZero');
});

test('exact rational intersection lands on pixel center', () => {
  // Edge (0,0)-(7,3) meets scanline y=1.5 at x=3.5 exactly: pixel (3,1)
  // center is on the boundary and must be classified inside.
  const r = rasterize(
    [
      [0, 0],
      [7, 3],
      [0, 3],
    ],
    { width: 8, height: 4 },
  );
  assert.equal(r.evenOdd.at(3, 1), 1);
  assert.equal(r.nonZero.at(3, 1), 1);
  // Interior and exterior neighbours of the boundary pixel.
  assert.equal(r.evenOdd.at(2, 1), 1);
  assert.equal(r.nonZero.at(2, 1), 1);
  assert.equal(r.evenOdd.at(4, 1), 0);
  assert.equal(r.nonZero.at(4, 1), 0);
});

// ---------- self-intersecting polygons ----------

test('double-wound triangle: rules differ on the whole interior', () => {
  // Same triangle traced twice: winding number 2 inside, crossing count 2.
  const tri = [
    [1, 1],
    [6, 1],
    [1, 6],
  ];
  const r = rasterize([...tri, ...tri], { width: 8, height: 8 });
  // Interior point (1.5, 1.5): even-odd out, non-zero in.
  assert.equal(r.evenOdd.at(1, 1), 0);
  assert.equal(r.nonZero.at(1, 1), 1);
  assert.equal(r.diff.at(1, 1), 1);
  // Interior point (2.5, 3.5): also wound twice.
  assert.equal(r.evenOdd.at(2, 3), 0);
  assert.equal(r.nonZero.at(2, 3), 1);
  assert.equal(r.diff.at(2, 3), 1);
  // Hypotenuse x+y=7 passes through pixel center (3.5, 3.5): boundary ->
  // inside under both rules.
  assert.equal(r.evenOdd.at(3, 3), 1);
  assert.equal(r.nonZero.at(3, 3), 1);
  assert.equal(r.diff.at(3, 3), 0);
  // Clearly outside.
  assert.equal(r.evenOdd.at(0, 0), 0);
  assert.equal(r.nonZero.at(0, 0), 0);
  // diffPixels agrees with the diff mask.
  const set = new Set(r.diffPixels().map(([x, y]) => `${x},${y}`));
  assert(set.has('1,1'));
  assert(set.has('2,3'));
  assert(!set.has('3,3'));
  for (let y = 0; y < 8; y++) {
    for (let x = 0; x < 8; x++) {
      assert.equal(set.has(`${x},${y}`), r.diff.at(x, y) === 1);
    }
  }
});

test('pentagram: center filled only by non-zero rule', () => {
  const star = [];
  for (let k = 0; k < 5; k++) {
    const a = (Math.PI / 180) * (90 + k * 144);
    star.push([6 + 5 * Math.cos(a), 6 - 5 * Math.sin(a)]);
  }
  const r = rasterize(star, { width: 12, height: 12 });
  // Center pentagon: crossed twice -> even-odd out; winding 2 -> non-zero in.
  for (const [x, y] of [
    [5, 5],
    [6, 6],
    [5, 6],
    [6, 5],
  ]) {
    assert.equal(r.evenOdd.at(x, y), 0, `evenOdd at ${x},${y}`);
    assert.equal(r.nonZero.at(x, y), 1, `nonZero at ${x},${y}`);
    assert.equal(r.diff.at(x, y), 1, `diff at ${x},${y}`);
  }
  // Tip of the top arm: inside under both rules.
  assert.equal(r.evenOdd.at(5, 3), 1);
  assert.equal(r.nonZero.at(5, 3), 1);
  assert.equal(r.diff.at(5, 3), 0);
  // Far corner: outside under both rules.
  assert.equal(r.evenOdd.at(0, 0), 0);
  assert.equal(r.nonZero.at(0, 0), 0);
});

test('bowtie (figure-eight): both rules agree everywhere', () => {
  // For a figure-eight the two lobes carry winding +1 and -1, so even-odd
  // and non-zero produce identical masks; the diff must be empty.
  const r = rasterize(
    [
      [1, 1],
      [5, 5],
      [5, 1],
      [1, 5],
    ],
    { width: 7, height: 7 },
  );
  const expected = [
    '.......',
    '.#..#..',
    '.####..',
    '.####..',
    '.#..#..',
    '.......',
    '.......',
  ];
  assertMask(r.evenOdd, expected, 'evenOdd');
  assertMask(r.nonZero, expected, 'nonZero');
  assertMask(r.diff, [
    '.......',
    '.......',
    '.......',
    '.......',
    '.......',
    '.......',
    '.......',
  ], 'diff');
  // Diagonals pass through pixel centers (1.5,1.5), (3.5,3.5) etc.: those
  // pixels are boundary and therefore inside.
  assert.equal(r.evenOdd.at(3, 3), 1);
  assert.equal(r.nonZero.at(3, 3), 1);
});


test('closing vertex may be repeated; zero-length edges ignored', () => {
  const open = rasterize(
    [
      [1, 1],
      [4, 1],
      [4, 4],
      [1, 4],
    ],
    { width: 6, height: 6 },
  );
  const closed = rasterize(
    [
      [1, 1],
      [4, 1],
      [4, 4],
      [1, 4],
      [1, 1],
    ],
    { width: 6, height: 6 },
  );
  assert.deepEqual([...closed.evenOdd.data], [...open.evenOdd.data]);
  assert.deepEqual([...closed.nonZero.data], [...open.nonZero.data]);
});

test('non-zero origin shifts pixel centers', () => {
  const r = rasterize(
    [
      [10, 10],
      [13, 10],
      [13, 13],
      [10, 13],
    ],
    { width: 5, height: 5, originX: 9, originY: 9 },
  );
  const expected = [
    '.....',
    '.###.',
    '.###.',
    '.###.',
    '.....',
  ];
  assertMask(r.evenOdd, expected, 'evenOdd');
  assertMask(r.nonZero, expected, 'nonZero');
});

test('input validation', () => {
  assert.throws(() => rasterize([[0, 0], [1, 1]], { width: 4, height: 4 }));
  assert.throws(() => rasterize([[0, 0], [1, 0], [1, 1]], { width: 0, height: 4 }));
  assert.throws(() =>
    rasterize([[0, 0], [1, 0], [NaN, 1]], { width: 4, height: 4 }),
  );
});

// ---------- cross-check against a straightforward float reference ----------

function refClassify(poly, px, py) {
  // Returns { eo, nz } or null when the sample point is (nearly) on the
  // boundary, where float arithmetic cannot be trusted as a reference.
  let crossings = 0;
  let wn = 0;
  const n = poly.length;
  for (let i = 0; i < n; i++) {
    const [x1, y1] = poly[i];
    const [x2, y2] = poly[(i + 1) % n];
    if (y1 === y2) {
      if (y1 === py && Math.min(x1, x2) <= px && px <= Math.max(x1, x2)) {
        return null; // on a horizontal edge
      }
      continue;
    }
    if (py === y1 || py === y2) return null; // conservative: vertex row
    const lo = Math.min(y1, y2);
    const hi = Math.max(y1, y2);
    if (lo <= py && py < hi) {
      const x = x1 + ((py - y1) * (x2 - x1)) / (y2 - y1);
      if (Math.abs(x - px) < 1e-9) return null; // too close to call with floats
      if (x > px) {
        crossings++;
        wn += y2 > y1 ? 1 : -1;
      }
    }
  }
  return { eo: crossings % 2 === 1, nz: wn !== 0 };
}

function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('random polygons match float reference off the boundary', () => {
  const W = 12;
  const H = 12;
  let compared = 0;
  for (let seed = 1; seed <= 300; seed++) {
    const rnd = mulberry32(seed);
    const n = 3 + Math.floor(rnd() * 10);
    const poly = [];
    for (let i = 0; i < n; i++) poly.push([rnd() * W, rnd() * H]);
    const r = rasterize(poly, { width: W, height: H });
    for (let y = 0; y < H; y++) {
      for (let x = 0; x < W; x++) {
        const ref = refClassify(poly, x + 0.5, y + 0.5);
        if (!ref) continue;
        compared++;
        assert.equal(
          r.evenOdd.at(x, y),
          ref.eo ? 1 : 0,
          `seed ${seed} evenOdd at (${x},${y}) poly=${JSON.stringify(poly)}`,
        );
        assert.equal(
          r.nonZero.at(x, y),
          ref.nz ? 1 : 0,
          `seed ${seed} nonZero at (${x},${y}) poly=${JSON.stringify(poly)}`,
        );
        assert.equal(r.diff.at(x, y), r.evenOdd.at(x, y) ^ r.nonZero.at(x, y));
      }
    }
  }
  assert(compared > 10000, `too few pixels compared: ${compared}`);
  console.log(`   (${compared} pixel classifications cross-checked)`);
});

console.log(`\n${passed} tests passed${process.exitCode ? ' (with failures)' : ''}`);
