import {
  normalize,
  queryPoint,
  queryRules,
  verifyPartition,
  partitionVolume,
  unionVolume,
} from "../src/normalize";
import type { Rule, Region } from "../src/normalize";

declare const console: { log(...args: unknown[]): void; error(...args: unknown[]): void };

type Rand = () => number;

let passed = 0;
let failed = 0;

function check(cond: boolean, msg: string): void {
  if (cond) {
    passed++;
  } else {
    failed++;
    console.error("FAIL: " + msg);
  }
}

function eqJson(got: unknown, want: unknown, msg: string): void {
  const g = JSON.stringify(got);
  const w = JSON.stringify(want);
  check(g === w, msg + " | got " + g + " want " + w);
}

function rule(lo: number[], hi: number[], label: string, priority: number): Rule {
  return { box: { lo, hi }, label, priority };
}

function mulberry32(seed: number): Rand {
  let s = seed >>> 0;
  return function () {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function shuffledCopy(items: Rule[], rand: Rand): Rule[] {
  const arr = items.slice();
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    const t = arr[i];
    arr[i] = arr[j];
    arr[j] = t;
  }
  return arr;
}

function permutations(items: Rule[]): Rule[][] {
  const result: Rule[][] = [];
  const arr = items.slice();
  function heap(k: number): void {
    if (k <= 1) {
      result.push(arr.slice());
      return;
    }
    heap(k - 1);
    for (let i = 0; i < k - 1; i++) {
      const j = k % 2 === 0 ? i : 0;
      const t = arr[j];
      arr[j] = arr[k - 1];
      arr[k - 1] = t;
      heap(k - 1);
    }
  }
  heap(arr.length);
  return result;
}

function canMerge(a: Region, b: Region): boolean {
  if (a.label !== b.label) return false;
  const dims = a.box.lo.length;
  for (let dim = 0; dim < dims; dim++) {
    const adjacent = a.box.hi[dim] === b.box.lo[dim] || b.box.hi[dim] === a.box.lo[dim];
    if (!adjacent) continue;
    let rest = true;
    for (let d = 0; d < dims; d++) {
      if (d === dim) continue;
      if (a.box.lo[d] !== b.box.lo[d] || a.box.hi[d] !== b.box.hi[d]) {
        rest = false;
        break;
      }
    }
    if (rest) return true;
  }
  return false;
}

function hasMergeablePair(regions: Region[]): boolean {
  for (let i = 0; i < regions.length; i++) {
    for (let j = i + 1; j < regions.length; j++) {
      if (canMerge(regions[i], regions[j])) return true;
    }
  }
  return false;
}

// 1. basic priority cut in 1D; same-label pieces merge across rule boundaries
const rules1 = [
  rule([0], [10], "a", 1),
  rule([3], [6], "b", 2),
  rule([8], [12], "a", 1),
];
eqJson(
  normalize(rules1),
  [
    { box: { lo: [0], hi: [3] }, label: "a" },
    { box: { lo: [3], hi: [6] }, label: "b" },
    { box: { lo: [6], hi: [12] }, label: "a" },
  ],
  "1d priority cut"
);
check(unionVolume(rules1) === 12, "1d union volume");
check(verifyPartition(rules1, normalize(rules1)).ok, "1d verify");

// 2. adjacency: same label merges, different labels stay split
eqJson(
  normalize([rule([0], [5], "a", 1), rule([5], [9], "a", 1)]),
  [{ box: { lo: [0], hi: [9] }, label: "a" }],
  "adjacent same-label merge"
);
eqJson(
  normalize([rule([0], [5], "a", 1), rule([5], [9], "b", 1)]),
  [
    { box: { lo: [0], hi: [5] }, label: "a" },
    { box: { lo: [5], hi: [9] }, label: "b" },
  ],
  "adjacent different labels stay split"
);

// 3. L-shape: same-label cells merge along both axes, no mergeable pair remains
const lrules = [rule([0, 0], [2, 2], "x", 1), rule([1, 1], [2, 2], "y", 2)];
const lregions = normalize(lrules);
eqJson(
  lregions,
  [
    { box: { lo: [0, 0], hi: [2, 1] }, label: "x" },
    { box: { lo: [0, 1], hi: [1, 2] }, label: "x" },
    { box: { lo: [1, 1], hi: [2, 2] }, label: "y" },
  ],
  "L-shape merge"
);
check(!hasMergeablePair(lregions), "L-shape fully merged");

// 4. exhaustive permutation invariance on a 2D case with overlaps
const base: Rule[] = [
  rule([0, 0], [6, 6], "bg", 0),
  rule([1, 1], [4, 4], "mid", 1),
  rule([2, 2], [5, 5], "top", 2),
  rule([4, 0], [8, 3], "side", 1),
  rule([0, 4], [3, 8], "corner", 3),
  rule([6, 6], [9, 9], "far", 1),
];
const canonical = JSON.stringify(normalize(base));
let permsChecked = 0;
let permsOk = true;
for (const perm of permutations(base)) {
  if (JSON.stringify(normalize(perm)) !== canonical) {
    permsOk = false;
    console.error("FAIL: permutation " + permsChecked + " differs");
  }
  permsChecked++;
}
check(permsOk, "all permutations give identical output");
check(permsChecked === 720, "checked all 720 permutations, got " + permsChecked);
check(verifyPartition(base, normalize(base)).ok, "base case verifies");
check(!hasMergeablePair(normalize(base)), "base case fully merged");

// 5. point queries use half-open semantics
const rs = normalize([rule([0], [5], "a", 1), rule([5], [9], "b", 1)]);
check(queryPoint(rs, [0]) === "a", "query lo boundary inclusive");
check(queryPoint(rs, [4]) === "a", "query interior");
check(queryPoint(rs, [5]) === "b", "query hi boundary exclusive");
check(queryPoint(rs, [9]) === undefined, "query past end");
check(queryPoint(rs, [-1]) === undefined, "query before start");

// 6. equal-priority overlap with different labels is a conflict
let threw = false;
try {
  normalize([rule([0], [4], "a", 1), rule([2], [6], "b", 1)]);
} catch (e) {
  threw = true;
}
check(threw, "equal-priority conflict throws");
eqJson(
  normalize([rule([0], [4], "a", 1), rule([2], [6], "a", 1)]),
  [{ box: { lo: [0], hi: [6] }, label: "a" }],
  "equal-priority same label is fine"
);

// 7. dimension limits
let threw4 = false;
try {
  normalize([rule([0, 0, 0, 0], [1, 1, 1, 1], "a", 1)]);
} catch (e) {
  threw4 = true;
}
check(threw4, "dimensions above 3 rejected");

// 8. 3D smoke test
const rules3 = [rule([0, 0, 0], [4, 4, 4], "a", 1), rule([2, 2, 2], [6, 6, 6], "b", 2)];
const regions3 = normalize(rules3);
check(partitionVolume(regions3) === 120, "3d partition volume");
check(unionVolume(rules3) === 120, "3d union volume");
check(verifyPartition(rules3, regions3).ok, "3d verify");
check(queryPoint(regions3, [0, 0, 0]) === "a", "3d query a only");
check(queryPoint(regions3, [3, 3, 3]) === "b", "3d query priority wins");
check(queryPoint(regions3, [5, 5, 5]) === "b", "3d query b only");
check(queryPoint(regions3, [1, 3, 3]) === "a", "3d query a region");
check(queryPoint(regions3, [6, 6, 6]) === undefined, "3d query outside");
check(!hasMergeablePair(regions3), "3d fully merged");

// 9. randomized property tests
const rand = mulberry32(12345);
for (let trial = 0; trial < 60; trial++) {
  const dims = 1 + Math.floor(rand() * 3);
  const n = 1 + Math.floor(rand() * 8);
  const rules: Rule[] = [];
  for (let i = 0; i < n; i++) {
    const lo: number[] = [];
    const hi: number[] = [];
    for (let d = 0; d < dims; d++) {
      const a = Math.floor(rand() * 10);
      const b = a + 1 + Math.floor(rand() * 6);
      lo.push(a);
      hi.push(b);
    }
    rules.push(rule(lo, hi, "L" + Math.floor(rand() * 3), i));
  }
  const regions = normalize(rules);
  const report = verifyPartition(rules, regions);
  check(report.ok, "verify trial " + trial + " " + JSON.stringify(report));
  check(!hasMergeablePair(regions), "fully merged trial " + trial);
  eqJson(normalize(shuffledCopy(rules, rand)), regions, "order independent trial " + trial);
  for (let q = 0; q < 100; q++) {
    const pt: number[] = [];
    for (let d = 0; d < dims; d++) pt.push(Math.floor(rand() * 18) - 2);
    const got = queryPoint(regions, pt);
    const want = queryRules(rules, pt);
    check(got === want, "query trial " + trial + " pt " + pt + ": got " + got + " want " + want);
  }
}

console.log(passed + " passed, " + failed + " failed");
if (failed > 0) {
  throw new Error(failed + " test failure(s)");
}
