import { strict as assert } from "assert";
import { RgaList, Op, Id, idKey } from "../src/rga-list";

// ---- tiny test harness -----------------------------------------------------

const tests: Array<{ name: string; fn: () => void }> = [];
function test(name: string, fn: () => void): void {
  tests.push({ name, fn });
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

function shuffled<T>(items: T[], rand: () => number): T[] {
  const out = items.slice();
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

function isSubsequence<T>(sub: T[], seq: T[], eq: (a: T, b: T) => boolean): boolean {
  let i = 0;
  for (const item of seq) {
    if (i < sub.length && eq(sub[i], item)) i++;
  }
  return i === sub.length;
}

// ---- tests -----------------------------------------------------------------

test("concurrent inserts at the same position converge deterministically", () => {
  const a = new RgaList("A");
  const base = a.insertAt(0, "base");

  // B and C go offline with only `base` visible, both insert after it.
  const b = new RgaList("B");
  const c = new RgaList("C");
  b.apply(base);
  c.apply(base);

  const opsB = [b.insertAt(1, "b1"), b.insertAt(2, "b2"), b.insertAt(1, "b3")];
  const opsC = [c.insertAt(1, "c1"), c.insertAt(1, "c2")];

  a.applyAll([...opsB, ...opsC]);
  b.applyAll(opsC);
  c.applyAll(opsB);

  assert.deepStrictEqual(a.toArray(), b.toArray());
  assert.deepStrictEqual(b.toArray(), c.toArray());
  assert.deepStrictEqual(a.serialize(), c.serialize());
  assert.strictEqual(a.toArray()[0], "base");
  assert.strictEqual(a.toArray().length, 6);
});

test("insert anchored at a deleted element still resolves", () => {
  const a = new RgaList("A");
  const x = a.insertAt(0, "x");
  const y = a.insertAt(1, "y");

  const b = new RgaList("B");
  b.applyAll([x, y]);

  // A deletes y; concurrently B (unaware) appends after y.
  const del = a.delete(y.id);
  const ins = b.insertAfter(y.id, "y-child");

  a.apply(ins);
  b.apply(del);

  assert.deepStrictEqual(a.toArray(), ["x", "y-child"]);
  assert.deepStrictEqual(a.toArray(), b.toArray());
  assert.deepStrictEqual(a.serialize(), b.serialize());
});

test("out-of-order delivery: child buffered until parent arrives", () => {
  const a = new RgaList("A");
  const parent = a.insertAt(0, "parent");
  const child = a.insertAfter(parent.id, "child");
  const grandchild = a.insertAfter(child.id, "grandchild");

  const b = new RgaList("B");
  b.apply(grandchild); // missing parent chain -> buffered
  b.apply(child); // still missing `parent` -> buffered
  assert.strictEqual(b.pendingCount, 2);
  assert.deepStrictEqual(b.toArray(), []);

  b.apply(parent); // flushes the whole chain
  assert.strictEqual(b.pendingCount, 0);
  assert.deepStrictEqual(b.toArray(), ["parent", "child", "grandchild"]);
  assert.deepStrictEqual(b.serialize(), a.serialize());
});

test("delete delivered before the insert it targets", () => {
  const a = new RgaList("A");
  const ins = a.insertAt(0, "doomed");
  const del = a.delete(ins.id);

  const b = new RgaList("B");
  b.apply(del); // target unknown yet: recorded in the tombstone set
  b.apply(ins);
  assert.deepStrictEqual(b.toArray(), []);
  assert.ok(b.isDeleted(ins.id));
  assert.deepStrictEqual(b.serialize(), a.serialize());
});

test("duplicate delivery is idempotent", () => {
  const a = new RgaList("A");
  const ops: Op[] = [a.insertAt(0, "one"), a.insertAt(1, "two")];
  ops.push(a.deleteAt(0));

  const b = new RgaList("B");
  b.applyAll([...ops, ...ops, ...ops]);
  assert.deepStrictEqual(b.toArray(), ["two"]);
  assert.deepStrictEqual(b.serialize(), a.serialize());
});

test("random ops converge under many delivery orders", () => {
  for (let seed = 1; seed <= 20; seed++) {
    const rand = mulberry32(seed);
    const names = ["A", "B", "C"];
    const replicas = names.map((n) => new RgaList(n));
    const log: Op[] = [];
    const broadcast = (op: Op) => {
      log.push(op);
      for (const r of replicas) r.apply(op);
    };

    // Phase 1: shared base list built by replica A.
    for (let i = 0; i < 5; i++) broadcast(replicas[0].insertAt(i, "base" + i));

    // Phase 2: replicas go offline and mutate concurrently.
    const offlineOps: Op[] = [];
    for (const r of replicas) {
      const known = r.entries();
      const steps = 8;
      for (let i = 0; i < steps; i++) {
        const visible = r.entries();
        if (visible.length > 0 && rand() < 0.3) {
          const victim = visible[Math.floor(rand() * visible.length)];
          offlineOps.push(r.delete(victim.id));
        } else {
          const anchor =
            known.length === 0 || rand() < 0.2
              ? null
              : known[Math.floor(rand() * known.length)].id;
          offlineOps.push(r.insertAfter(anchor, r.replica + "-" + i));
        }
      }
    }

    // Phase 3: deliver the same op set in many random orders.
    const allOps = [...log, ...offlineOps];
    let reference: string | null = null;
    for (let round = 0; round < 25; round++) {
      const fresh = new RgaList("FRESH");
      fresh.applyAll(shuffled(allOps, mulberry32(seed * 1000 + round)));
      assert.strictEqual(fresh.pendingCount, 0, "buffer must drain");
      const dump = fresh.serialize();
      if (reference === null) reference = dump;
      assert.strictEqual(dump, reference, "seed " + seed + " round " + round);
    }

    // And the origin replicas, once synced, agree with the fresh ones.
    for (const r of replicas) r.applyAll(allOps);
    for (const r of replicas) {
      assert.strictEqual(r.serialize(), reference, "replica " + r.replica);
    }
  }
});

test("deleting anchors never reorders surviving elements", () => {
  for (let seed = 1; seed <= 20; seed++) {
    const rand = mulberry32(seed * 7 + 3);

    const a = new RgaList("A");
    const baseOps: Op[] = [];
    for (let i = 0; i < 12; i++) baseOps.push(a.insertAt(i, "v" + i));

    const b = new RgaList("B");
    b.applyAll(baseOps);

    const survivorsBefore = a.entries(); // all currently visible, in order

    // B deletes a random subset (these stay as tombstone anchors).
    const deleteOps: Op[] = [];
    const deletedKeys = new Set<string>();
    for (const e of b.entries()) {
      if (rand() < 0.4) {
        deleteOps.push(b.delete(e.id));
        deletedKeys.add(idKey(e.id));
      }
    }

    // Concurrently A inserts new elements anchored anywhere — including at
    // elements B is deleting.
    const insertOps: Op[] = [];
    for (let i = 0; i < 8; i++) {
      const known = a.entries();
      const anchor =
        rand() < 0.15 ? null : known[Math.floor(rand() * known.length)].id;
      insertOps.push(a.insertAfter(anchor, "new" + i));
    }

    // Sync both ways, in adversarial orders.
    a.applyAll(shuffled(deleteOps, rand));
    b.applyAll(shuffled(insertOps, rand));

    assert.deepStrictEqual(a.serialize(), b.serialize());

    const expectedSurvivors = survivorsBefore
      .filter((e) => !deletedKeys.has(idKey(e.id)))
      .map((e) => idKey(e.id));
    const finalOrder = a.entries().map((e) => idKey(e.id));
    assert.ok(
      isSubsequence(expectedSurvivors, finalOrder, (x, y) => x === y),
      "survivors must keep their relative order; seed " + seed
    );
  }
});

test("deleting an anchor keeps its children in place", () => {
  const a = new RgaList("A");
  const x = a.insertAt(0, "x");
  const mid = a.insertAt(1, "mid");
  const child = a.insertAfter(mid.id, "child-of-mid");
  const z = a.insertAt(3, "z");

  const b = new RgaList("B");
  b.applyAll([x, mid, child, z]);
  assert.deepStrictEqual(b.toArray(), ["x", "mid", "child-of-mid", "z"]);

  const del = a.delete(mid.id);
  b.apply(del);
  // The child stays exactly where it was relative to x and z.
  assert.deepStrictEqual(b.toArray(), ["x", "child-of-mid", "z"]);
  assert.deepStrictEqual(a.toArray(), b.toArray());
});

// ---- runner ----------------------------------------------------------------

let failed = 0;
for (const { name, fn } of tests) {
  try {
    fn();
    console.log("ok   - " + name);
  } catch (err) {
    failed++;
    console.error("FAIL - " + name);
    console.error(err);
  }
}
if (failed > 0) {
  console.error(failed + " test(s) failed");
  process.exit(1);
}
console.log(tests.length + " tests passed");
