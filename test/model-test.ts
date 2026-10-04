import {
  PersistentMultiset,
  VersionId,
  UnknownVersionError,
  KeyNotFoundError,
  EmptyMultisetError,
} from "../src/persistent-multiset";

let checks = 0;

function assert(cond: boolean, msg: string): void {
  checks++;
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

function assertThrows(fn: () => void, ctor: new (...args: never[]) => Error, what: string): void {
  checks++;
  try {
    fn();
  } catch (err) {
    if (err instanceof ctor) return;
    throw new Error(`${what}: expected ${ctor.name}, got ${String(err)}`);
  }
  throw new Error(`${what}: expected ${ctor.name}, but nothing was thrown`);
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------------------------------------------------------------------------
// Deterministic checks: semantics, branching, errors, GC.
// ---------------------------------------------------------------------------
function testDeterministic(): void {
  const ms = new PersistentMultiset();

  const v0 = ms.create();
  assert(ms.size(v0) === 0 && ms.isEmpty(v0), "empty version");
  assertThrows(() => ms.select(v0, 0), EmptyMultisetError, "select on empty");
  assertThrows(() => ms.remove(v0, 1), KeyNotFoundError, "remove from empty");

  // Duplicates are counted.
  let v = v0;
  const chain: VersionId[] = [v0];
  for (const x of [5, 3, 5, 7, 3, 5]) {
    v = ms.insert(v, x);
    chain.push(v);
  }
  assert(ms.size(v) === 6, "size counts duplicates");
  assert(ms.count(v, 5) === 3 && ms.count(v, 3) === 2 && ms.count(v, 9) === 0, "count");
  assert(JSON.stringify(ms.toArray(v)) === JSON.stringify([3, 3, 5, 5, 5, 7]), "toArray");
  assert(ms.rank(v, 5) === 2 && ms.rank(v, 3) === 0 && ms.rank(v, 8) === 6, "rank");
  assert(ms.select(v, 0) === 3 && ms.select(v, 2) === 5 && ms.select(v, 5) === 7, "select 0-based");
  assertThrows(() => ms.select(v, -1), RangeError, "select negative");
  assertThrows(() => ms.select(v, 6), RangeError, "select past end");
  assertThrows(() => ms.select(v, 2.5), RangeError, "select non-integer");

  // Branching from an old version never mutates it.
  const removedOne = ms.remove(v, 5); // intermediate version, released below
  const branch = ms.insert(removedOne, 4); // branch off v
  assert(ms.count(v, 5) === 3 && ms.size(v) === 6, "old version unchanged after branch");
  assert(ms.count(branch, 5) === 2 && ms.count(branch, 4) === 1, "branch has its own edits");
  assert(JSON.stringify(ms.toArray(branch)) === JSON.stringify([3, 3, 4, 5, 5, 7]), "branch array");

  // fork() is an O(1) explicit branch.
  const forked = ms.fork(v);
  assert(JSON.stringify(ms.toArray(forked)) === JSON.stringify(ms.toArray(v)), "fork copies view");

  // GC: releasing a branch reclaims only nodes unreachable from live versions.
  const before = ms.liveNodeCount();
  assert(before === ms.reachableNodeCount(), "live == reachable before release");
  ms.release(branch);
  assert(ms.liveNodeCount() === ms.reachableNodeCount(), "live == reachable after release");
  assert(ms.liveNodeCount() < before, "unreachable nodes reclaimed");
  assert(ms.size(v) === 6 && ms.count(v, 5) === 3, "shared nodes keep old version intact");

  // Errors around released / unknown versions.
  ms.release(forked);
  assertThrows(() => ms.size(forked), UnknownVersionError, "use after release");
  assertThrows(() => ms.release(forked), UnknownVersionError, "double release");
  assertThrows(() => ms.insert(9999, 1), UnknownVersionError, "unknown version");
  assertThrows(() => ms.remove(v, 42), KeyNotFoundError, "remove absent key");

  // Releasing everything reclaims all nodes.
  ms.release(removedOne);
  for (const ver of chain) ms.release(ver);
  assert(ms.liveNodeCount() === 0 && ms.reachableNodeCount() === 0, "all nodes reclaimed");
  assert(ms.versionCount() === 0, "all versions released");
}

// ---------------------------------------------------------------------------
// Randomized model-based test: sorted-array oracle, branching, repeated
// releases, and GC consistency after every release.
// ---------------------------------------------------------------------------
function testRandom(seed: number, ops: number): void {
  const rand = mulberry32(seed);
  const pick = <T>(arr: T[]): T => arr[Math.floor(rand() * arr.length)];
  const KEY_SPACE = 40; // small on purpose: forces heavy duplication

  const ms = new PersistentMultiset();
  const model = new Map<VersionId, number[]>(); // sorted arrays per live version

  const v0 = ms.create();
  model.set(v0, []);

  const live = (): VersionId[] => [...model.keys()];

  const checkAll = (): void => {
    for (const [v, arr] of model) {
      assert(ms.size(v) === arr.length, `size of v${v} (seed ${seed})`);
      assert(
        JSON.stringify(ms.toArray(v)) === JSON.stringify(arr),
        `toArray of v${v} (seed ${seed})`
      );
    }
  };

  for (let op = 0; op < ops; op++) {
    const roll = rand();
    const ctx = `seed ${seed} op ${op}`;
    if (roll < 0.35) {
      // insert
      const base = pick(live());
      const key = Math.floor(rand() * KEY_SPACE);
      const nv = ms.insert(base, key);
      const arr = model.get(base)!.slice();
      let i = 0;
      while (i < arr.length && arr[i] <= key) i++;
      arr.splice(i, 0, key);
      model.set(nv, arr);
    } else if (roll < 0.6) {
      // remove one copy of a present key
      const nonEmpty = live().filter((v) => model.get(v)!.length > 0);
      if (nonEmpty.length === 0) continue;
      const base = pick(nonEmpty);
      const key = pick(model.get(base)!);
      const nv = ms.remove(base, key);
      const arr = model.get(base)!.slice();
      arr.splice(arr.indexOf(key), 1);
      model.set(nv, arr);
    } else if (roll < 0.7) {
      // rank against oracle
      const v = pick(live());
      const key = Math.floor(rand() * (KEY_SPACE + 4)) - 2; // also probe absent keys
      const expected = model.get(v)!.filter((x) => x < key).length;
      assert(ms.rank(v, key) === expected, `rank(${key}) on v${v} (${ctx})`);
    } else if (roll < 0.8) {
      // select against oracle, including invalid indices
      const v = pick(live());
      const arr = model.get(v)!;
      if (arr.length === 0) {
        assertThrows(() => ms.select(v, 0), EmptyMultisetError, `select empty (${ctx})`);
      } else {
        const k = Math.floor(rand() * (arr.length + 4)) - 2;
        if (k < 0 || k >= arr.length) {
          assertThrows(() => ms.select(v, k), RangeError, `select ${k} (${ctx})`);
        } else {
          assert(ms.select(v, k) === arr[k], `select ${k} on v${v} (${ctx})`);
        }
      }
    } else if (roll < 0.9) {
      // explicit branch
      const base = pick(live());
      model.set(ms.fork(base), model.get(base)!.slice());
    } else {
      // release (keep at least one version alive), then verify GC precisely
      if (model.size <= 1) continue;
      const v = pick(live());
      ms.release(v);
      model.delete(v);
      assert(!ms.hasVersion(v), `hasVersion after release (${ctx})`);
      assert(
        ms.liveNodeCount() === ms.reachableNodeCount(),
        `GC: live ${ms.liveNodeCount()} != reachable ${ms.reachableNodeCount()} (${ctx})`
      );
    }
    if (op % 64 === 0) {
      checkAll();
      for (const v of live()) ms.validate(v);
      assert(
        ms.liveNodeCount() === ms.reachableNodeCount(),
        `GC invariant (${ctx})`
      );
    }
  }

  checkAll();
  // Repeatedly release everything; the allocator must drain to zero.
  for (const v of live()) {
    ms.release(v);
    assert(ms.liveNodeCount() === ms.reachableNodeCount(), "GC during final drain");
  }
  assert(ms.liveNodeCount() === 0, "all nodes reclaimed after releasing all versions");
}

testDeterministic();
for (const seed of [1, 7, 42, 1337, 20261004]) {
  testRandom(seed, 3000);
}
console.log(`OK: all deterministic and randomized model checks passed (${checks} assertions)`);
