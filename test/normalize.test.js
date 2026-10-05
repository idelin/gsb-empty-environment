const assert = require('node:assert/strict');
const { normalize, queryPoint, verify, boxVolume } = require('../dist/normalize.js');

let failures = 0;
function test(name, fn) {
  try {
    fn();
    console.log(`ok   - ${name}`);
  } catch (e) {
    failures += 1;
    console.error(`FAIL - ${name}\n${e.stack}`);
  }
}

function canonical(regions) {
  return regions
    .map((r) => JSON.stringify([r.label, r.box.lo, r.box.hi]))
    .sort()
    .join('\n');
}

function permutations(arr) {
  if (arr.length <= 1) return [arr.slice()];
  const out = [];
  for (let i = 0; i < arr.length; i++) {
    const rest = arr.slice(0, i).concat(arr.slice(i + 1));
    for (const p of permutations(rest)) out.push([arr[i], ...p]);
  }
  return out;
}

function assertOrderIndependent(rules, msg) {
  const expected = canonical(normalize(rules));
  for (const perm of permutations(rules)) {
    assert.equal(canonical(normalize(perm)), expected, msg);
  }
}

const rules1d = [
  { box: { lo: [0], hi: [10] }, label: 'a', priority: 1 },
  { box: { lo: [3], hi: [7] }, label: 'b', priority: 5 },
  { box: { lo: [5], hi: [12] }, label: 'c', priority: 3 },
];

test('1d: partition, merge and exact regions', () => {
  const regions = normalize(rules1d);
  assert.equal(canonical(regions), [
    JSON.stringify(['a', [0], [3]]),
    JSON.stringify(['b', [3], [7]]),
    JSON.stringify(['c', [7], [12]]),
  ].join('\n'));
});

test('1d: point queries', () => {
  const regions = normalize(rules1d);
  assert.equal(queryPoint(regions, [0]), 'a');
  assert.equal(queryPoint(regions, [4]), 'b');
  assert.equal(queryPoint(regions, [6]), 'b');
  assert.equal(queryPoint(regions, [7]), 'c');
  assert.equal(queryPoint(regions, [11]), 'c');
  assert.equal(queryPoint(regions, [12]), null);
  assert.equal(queryPoint(regions, [-1]), null);
});

test('1d: verify ok and volumes', () => {
  const regions = normalize(rules1d);
  const report = verify(rules1d, regions);
  assert.deepEqual(report.errors, []);
  assert.equal(report.ok, true);
  assert.equal(report.regionVolume, 12);
  assert.equal(report.coveredVolume, 12);
});

test('1d: order independent over all permutations', () => {
  assertOrderIndependent(rules1d, '1d permutation mismatch');
});

test('1d: same-label adjacent rules merge into one box', () => {
  const regions = normalize([
    { box: { lo: [0], hi: [5] }, label: 'x', priority: 1 },
    { box: { lo: [5], hi: [9] }, label: 'x', priority: 1 },
  ]);
  assert.equal(regions.length, 1);
  assert.deepEqual(regions[0], { box: { lo: [0], hi: [9] }, label: 'x' });
});

test('1d: same-label merge split by higher priority island', () => {
  const regions = normalize([
    { box: { lo: [0], hi: [5] }, label: 'x', priority: 1 },
    { box: { lo: [5], hi: [9] }, label: 'x', priority: 1 },
    { box: { lo: [3], hi: [6] }, label: 'y', priority: 9 },
  ]);
  assert.equal(canonical(regions), [
    JSON.stringify(['x', [0], [3]]),
    JSON.stringify(['x', [6], [9]]),
    JSON.stringify(['y', [3], [6]]),
  ].join('\n'));
});

test('tie on priority: smaller label wins regardless of order', () => {
  const a = { box: { lo: [0], hi: [4] }, label: 'z', priority: 1 };
  const b = { box: { lo: [2], hi: [6] }, label: 'a', priority: 1 };
  const expected = [
    JSON.stringify(['a', [2], [6]]),
    JSON.stringify(['z', [0], [2]]),
  ].join('\n');
  assert.equal(canonical(normalize([a, b])), expected);
  assert.equal(canonical(normalize([b, a])), expected);
});

const rules2d = [
  { box: { lo: [0, 0], hi: [10, 10] }, label: 'bg', priority: 0 },
  { box: { lo: [2, 2], hi: [5, 5] }, label: 'fg', priority: 10 },
  { box: { lo: [4, 4], hi: [7, 7] }, label: 'fg2', priority: 5 },
];

test('2d: queries follow priority (fg beats fg2 in overlap)', () => {
  const regions = normalize(rules2d);
  assert.equal(queryPoint(regions, [0, 0]), 'bg');
  assert.equal(queryPoint(regions, [3, 3]), 'fg');
  assert.equal(queryPoint(regions, [4, 4]), 'fg'); // fg(10) > fg2(5)
  assert.equal(queryPoint(regions, [6, 6]), 'fg2');
  assert.equal(queryPoint(regions, [9, 9]), 'bg');
  assert.equal(queryPoint(regions, [10, 10]), null);
});

test('2d: verify ok, union volume stays 100', () => {
  const regions = normalize(rules2d);
  const report = verify(rules2d, regions);
  assert.deepEqual(report.errors, []);
  assert.equal(report.ok, true);
  assert.equal(report.regionVolume, 100);
  assert.equal(report.coveredVolume, 100);
});

test('2d: order independent over all permutations', () => {
  assertOrderIndependent(rules2d, '2d permutation mismatch');
});

test('2d: L-shaped same-label union decomposes deterministically', () => {
  const rules = [
    { box: { lo: [0, 0], hi: [2, 1] }, label: 'x', priority: 1 },
    { box: { lo: [0, 1], hi: [1, 3] }, label: 'x', priority: 1 },
  ];
  const regions = normalize(rules);
  assert.equal(regions.length, 2);
  assert.equal(regions.reduce((s, r) => s + boxVolume(r.box), 0), 4);
  assert.equal(verify(rules, regions).ok, true);
  assertOrderIndependent(rules, 'L-shape permutation mismatch');
});

const rules3d = [
  { box: { lo: [0, 0, 0], hi: [4, 4, 4] }, label: 'a', priority: 1 },
  { box: { lo: [1, 1, 1], hi: [3, 3, 3] }, label: 'b', priority: 2 },
  { box: { lo: [2, 2, 2], hi: [5, 5, 5] }, label: 'c', priority: 3 },
];

test('3d: queries and verify', () => {
  const regions = normalize(rules3d);
  assert.equal(queryPoint(regions, [0, 0, 0]), 'a');
  assert.equal(queryPoint(regions, [1, 1, 1]), 'b');
  assert.equal(queryPoint(regions, [2, 2, 2]), 'c');
  assert.equal(queryPoint(regions, [3, 3, 3]), 'c');
  assert.equal(queryPoint(regions, [4, 4, 4]), 'c');
  assert.equal(queryPoint(regions, [5, 5, 5]), null);
  const report = verify(rules3d, regions);
  assert.deepEqual(report.errors, []);
  assert.equal(report.ok, true);
  assert.equal(report.regionVolume, 83); // 64 + 27 - 8
  assert.equal(report.coveredVolume, 83);
});

test('3d: order independent over all permutations', () => {
  assertOrderIndependent(rules3d, '3d permutation mismatch');
});

test('verify detects overlapping regions', () => {
  const regions = normalize(rules1d);
  const tampered = regions.concat([{ box: { lo: [1], hi: [2] }, label: 'q' }]);
  const report = verify(rules1d, tampered);
  assert.equal(report.ok, false);
  assert.equal(report.disjoint, false);
});

test('verify detects shrunk region (volume mismatch)', () => {
  const regions = normalize(rules1d);
  const tampered = regions.map((r) => ({ box: { lo: r.box.lo.slice(), hi: r.box.hi.slice() }, label: r.label }));
  tampered[tampered.length - 1].box.hi[0] -= 1;
  const report = verify(rules1d, tampered);
  assert.equal(report.ok, false);
  assert.equal(report.disjoint, true);
  assert.equal(report.regionVolume, 11);
  assert.equal(report.coveredVolume, 12);
});

test('verify detects mislabeled region', () => {
  const regions = normalize(rules1d);
  const tampered = regions.map((r) => ({ box: { lo: r.box.lo.slice(), hi: r.box.hi.slice() }, label: r.label }));
  tampered[0].label = 'impostor';
  const report = verify(rules1d, tampered);
  assert.equal(report.ok, false);
  assert.equal(report.semanticsMatch, false);
});

test('empty input', () => {
  assert.deepEqual(normalize([]), []);
  assert.equal(verify([], []).ok, true);
});

test('invalid input rejected', () => {
  assert.throws(() => normalize([{ box: { lo: [5], hi: [5] }, label: 'x', priority: 1 }]));
  assert.throws(() => normalize([{ box: { lo: [0.5], hi: [5] }, label: 'x', priority: 1 }]));
  assert.throws(() => normalize([{ box: { lo: [0, 0, 0, 0], hi: [1, 1, 1, 1] }, label: 'x', priority: 1 }]));
  assert.throws(() => normalize([
    { box: { lo: [0], hi: [1] }, label: 'x', priority: 1 },
    { box: { lo: [0, 0], hi: [1, 1] }, label: 'y', priority: 1 },
  ]));
});

test('fuzz: random 2d rule sets are order independent and verifiable', () => {
  let seed = 12345;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x80000000;
  };
  const randInt = (n) => Math.floor(rand() * n);
  for (let trial = 0; trial < 200; trial++) {
    const rules = [];
    const n = 1 + randInt(6);
    for (let i = 0; i < n; i++) {
      const x0 = randInt(20);
      const y0 = randInt(20);
      rules.push({
        box: { lo: [x0, y0], hi: [x0 + 1 + randInt(8), y0 + 1 + randInt(8)] },
        label: `L${randInt(4)}`,
        priority: randInt(5),
      });
    }
    const regions = normalize(rules);
    const report = verify(rules, regions);
    assert.equal(report.ok, true, `fuzz trial ${trial}: ${report.errors.join('; ')}`);
    const shuffled = rules.slice();
    for (let i = shuffled.length - 1; i > 0; i--) {
      const j = randInt(i + 1);
      [shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]];
    }
    assert.equal(canonical(normalize(shuffled)), canonical(regions), `fuzz trial ${trial} order dependent`);
  }
});

if (failures > 0) {
  console.error(`\n${failures} test(s) failed`);
  process.exit(1);
}
console.log('\nall tests passed');
