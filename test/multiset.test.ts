import assert from "node:assert/strict";
import {
  PersistentMultiset,
  EmptyMultisetError,
  IndexOutOfRangeError,
  ValueNotFoundError,
  ReleasedVersionError,
  getLiveNodeCount,
} from "../src/multiset.ts";

// --------------------------------------------------------------------------
// Deterministic unit tests
// --------------------------------------------------------------------------

function testBasics(): void {
  const ms = new PersistentMultiset<number>();
  assert.equal(ms.isEmpty(), true);
  assert.equal(ms.size(), 0);
  assert.throws(() => ms.select(0), EmptyMultisetError);
  assert.throws(() => ms.min(), EmptyMultisetError);
  assert.throws(() => ms.max(), EmptyMultisetError);
  assert.throws(() => ms.remove(1), ValueNotFoundError);
  assert.equal(ms.rank(42), 0);
  assert.equal(ms.countOf(42), 0);

  for (const v of [5, 1, 3, 3, 5, 5, 2]) ms.insert(v);
  assert.deepEqual(ms.toArray(), [1, 2, 3, 3, 5, 5, 5]);
  assert.equal(ms.size(), 7);
  assert.equal(ms.countOf(5), 3);
  assert.equal(ms.countOf(3), 2);
  assert.equal(ms.countOf(4), 0);

  // rank = number of elements strictly less than the value
  assert.equal(ms.rank(1), 0);
  assert.equal(ms.rank(3), 2);
  assert.equal(ms.rank(4), 4);
  assert.equal(ms.rank(5), 4);
  assert.equal(ms.rank(99), 7);
  assert.equal(ms.rank(-1), 0);

  // select is 0-based
  assert.equal(ms.select(0), 1);
  assert.equal(ms.select(2), 3);
  assert.equal(ms.select(3), 3);
  assert.equal(ms.select(6), 5);
  assert.equal(ms.min(), 1);
  assert.equal(ms.max(), 5);
  assert.throws(() => ms.select(7), IndexOutOfRangeError);
  assert.throws(() => ms.select(-1), IndexOutOfRangeError);
  assert.throws(() => ms.select(1.5), IndexOutOfRangeError);

  // remove deletes exactly one copy
  ms.remove(5);
  assert.deepEqual(ms.toArray(), [1, 2, 3, 3, 5, 5]);
  ms.remove(3);
  ms.remove(3);
  assert.deepEqual(ms.toArray(), [1, 2, 5, 5]);
  assert.throws(() => ms.remove(3), ValueNotFoundError);
  ms.release();
}

function testBranching(): void {
  const base = new PersistentMultiset<number>();
  for (const v of [10, 20, 20, 30]) base.insert(v);

  const branch = base.fork();
  branch.insert(15);
  branch.remove(20);
  base.insert(40);

  // Mutations on either side must not leak into the other version.
  assert.deepEqual(base.toArray(), [10, 20, 20, 30, 40]);
  assert.deepEqual(branch.toArray(), [10, 15, 20, 30]);

  // Branching from an old version again keeps working.
  const branch2 = base.fork();
  branch2.remove(10);
  assert.deepEqual(branch2.toArray(), [20, 20, 30, 40]);
  assert.deepEqual(base.toArray(), [10, 20, 20, 30, 40]);

  base.release();
  // Old versions stay usable after a sibling is released.
  assert.deepEqual(branch.toArray(), [10, 15, 20, 30]);
  assert.deepEqual(branch2.toArray(), [20, 20, 30, 40]);
  branch.release();
  branch2.release();
}

function testReleaseAndReclamation(): void {
  const before = getLiveNodeCount();
  const a = new PersistentMultiset<number>();
  for (const v of [1, 2, 3, 4, 5, 6, 7, 8]) a.insert(v);
  const b = a.fork();
  b.insert(9);
  b.insert(10);

  const bNodesBefore = b.collectReachableNodes();
  a.release();
  // Releasing a sibling must not touch any node still reachable from b,
  // and must reclaim exactly the nodes that became unreachable.
  assert.deepEqual(b.collectReachableNodes(), bNodesBefore);
  assert.deepEqual(b.toArray(), [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]);
  assert.equal(getLiveNodeCount(), bNodesBefore.size + before);

  // Double release and use-after-release are explicit errors.
  assert.throws(() => a.release(), ReleasedVersionError);
  assert.throws(() => a.insert(1), ReleasedVersionError);
  assert.throws(() => a.size(), ReleasedVersionError);
  assert.throws(() => a.fork(), ReleasedVersionError);

  b.release();
  // Everything allocated here is reclaimed once unreachable.
  assert.equal(getLiveNodeCount(), before);
}

function testCustomComparator(): void {
  const ms = new PersistentMultiset<string>((a, b) => a.length - b.length || (a < b ? -1 : a > b ? 1 : 0));
  ms.insert("pear");
  ms.insert("fig");
  ms.insert("fig");
  ms.insert("apple");
  assert.deepEqual(ms.toArray(), ["fig", "fig", "pear", "apple"]);
  assert.equal(ms.select(0), "fig");
  assert.equal(ms.rank("apple"), 3);
  ms.release();
}

// --------------------------------------------------------------------------
// Randomized model test: sorted-array oracle, branching, repeated releases
// --------------------------------------------------------------------------

function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface ModelVersion {
  ms: PersistentMultiset<number>;
  model: number[]; // sorted, duplicates repeated
}

function sortedInsert(model: number[], value: number): void {
  let lo = 0;
  let hi = model.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (model[mid] < value) lo = mid + 1;
    else hi = mid;
  }
  model.splice(lo, 0, value);
}

function sortedRemoveOne(model: number[], value: number): boolean {
  const idx = model.indexOf(value);
  if (idx < 0) return false;
  model.splice(idx, 1);
  return true;
}

function checkVersion(v: ModelVersion, label: string): void {
  assert.deepEqual(v.ms.toArray(), v.model, `${label}: toArray mismatch`);
  assert.equal(v.ms.size(), v.model.length, `${label}: size mismatch`);
}

function checkGcInvariant(versions: ModelVersion[]): void {
  const reachable = new Set<object>();
  for (const v of versions) {
    for (const node of v.ms.collectReachableNodes()) reachable.add(node);
  }
  assert.equal(
    getLiveNodeCount(),
    reachable.size,
    `live node count ${getLiveNodeCount()} != reachable ${reachable.size}`,
  );
}

function randomTest(seed: number, ops: number): void {
  const rand = mulberry32(seed);
  const pick = (n: number) => Math.floor(rand() * n);
  const VALUE_RANGE = 40; // small range forces many duplicates

  const versions: ModelVersion[] = [{ ms: new PersistentMultiset<number>(), model: [] }];

  for (let step = 0; step < ops; step++) {
    const op = rand();
    const vi = pick(versions.length);
    const v = versions[vi];
    const label = `seed=${seed} step=${step}`;

    if (op < 0.28) {
      // insert
      const value = pick(VALUE_RANGE);
      v.ms.insert(value);
      sortedInsert(v.model, value);
      checkVersion(v, label + " insert");
    } else if (op < 0.48) {
      // remove one copy; sometimes a value that is absent
      const value = rand() < 0.75 && v.model.length > 0
        ? v.model[pick(v.model.length)]
        : pick(VALUE_RANGE);
      const present = v.model.includes(value);
      if (present) {
        v.ms.remove(value);
        sortedRemoveOne(v.model, value);
      } else {
        assert.throws(() => v.ms.remove(value), ValueNotFoundError, label + " remove absent");
      }
      checkVersion(v, label + " remove");
    } else if (op < 0.60) {
      // rank of a random (possibly absent) value
      const value = pick(VALUE_RANGE + 4) - 2;
      const expected = v.model.filter((x) => x < value).length;
      assert.equal(v.ms.rank(value), expected, `${label} rank(${value})`);
    } else if (op < 0.72) {
      // select: valid, boundary-invalid, and empty cases
      const n = v.model.length;
      if (n === 0) {
        assert.throws(() => v.ms.select(0), EmptyMultisetError, label + " select empty");
        assert.throws(() => v.ms.select(pick(3)), EmptyMultisetError, label + " select empty");
      } else {
        const k = pick(n);
        assert.equal(v.ms.select(k), v.model[k], `${label} select(${k})`);
        assert.throws(() => v.ms.select(n), IndexOutOfRangeError, label + " select size");
        assert.throws(() => v.ms.select(-1 - pick(3)), IndexOutOfRangeError, label + " select negative");
      }
    } else if (op < 0.80) {
      // countOf / has
      const value = pick(VALUE_RANGE);
      const expected = v.model.filter((x) => x === value).length;
      assert.equal(v.ms.countOf(value), expected, `${label} countOf(${value})`);
      assert.equal(v.ms.has(value), expected > 0, `${label} has(${value})`);
    } else if (op < 0.90) {
      // fork a new branch from a random existing version
      const src = versions[pick(versions.length)];
      versions.push({ ms: src.ms.fork(), model: [...src.model] });
    } else {
      // release a random version (keep at least one alive)
      if (versions.length > 1) {
        const [victim] = versions.splice(vi, 1);
        victim.ms.release();
        // repeated release must throw, not corrupt anything
        assert.throws(() => victim.ms.release(), ReleasedVersionError, label + " double release");
        assert.throws(() => victim.ms.select(0), ReleasedVersionError, label + " use after release");
      }
    }

    if (step % 50 === 49) {
      for (const ver of versions) checkVersion(ver, label + " periodic");
      checkGcInvariant(versions);
    }
  }

  // Final cross-check of every surviving branch, then release all.
  for (const v of versions) checkVersion(v, "final");
  checkGcInvariant(versions);
  for (const v of versions) v.ms.release();
}

// --------------------------------------------------------------------------

testBasics();
console.log("ok - basics");
testBranching();
console.log("ok - branching");
testReleaseAndReclamation();
console.log("ok - release & reclamation");
testCustomComparator();
console.log("ok - custom comparator");

const gcBaseline = getLiveNodeCount();
for (const seed of [1, 7, 42, 1337, 20261005]) {
  randomTest(seed, 3000);
  console.log(`ok - randomized model test (seed=${seed})`);
}
randomTest(987654321, 20000);
console.log("ok - randomized stress test (seed=987654321, 20000 ops)");
assert.equal(getLiveNodeCount(), gcBaseline, "all nodes reclaimed after releasing every version");
console.log("ok - all nodes reclaimed after full release");

console.log("all tests passed");
