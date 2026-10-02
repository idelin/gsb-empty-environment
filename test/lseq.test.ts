import { test } from "node:test";
import assert from "node:assert/strict";
import {
  LSEQSequence,
  allocate,
  baseAt,
  compareIdentifiers,
  mulberry32,
  type Identifier,
  type InsertOp,
} from "../src/lseq.ts";

function shuffled<T>(items: T[], rng: () => number): T[] {
  const out = [...items];
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rng() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

function assertStrictlyIncreasing(ids: Identifier[]): void {
  for (let i = 1; i < ids.length; i++) {
    assert.ok(
      compareIdentifiers(ids[i - 1], ids[i]) < 0,
      `identifiers must be strictly increasing at ${i}`,
    );
  }
}

test("compareIdentifiers: total order with site tiebreak and prefix rule", () => {
  const a: Identifier = [{ digit: 1, site: 1 }];
  const b: Identifier = [{ digit: 1, site: 2 }];
  const c: Identifier = [
    { digit: 1, site: 1 },
    { digit: 0, site: 9 },
  ];
  assert.ok(compareIdentifiers(a, b) < 0);
  assert.ok(compareIdentifiers(b, a) > 0);
  assert.equal(compareIdentifiers(a, a), 0);
  assert.ok(compareIdentifiers(a, c) < 0); // proper prefix sorts first
});

test("allocate: identifier always lands strictly between neighbours", () => {
  const rng = mulberry32(7);
  let prev: Identifier | null = null;
  for (let i = 0; i < 500; i++) {
    const id = allocate(prev, null, 1, rng);
    if (prev) assert.ok(compareIdentifiers(prev, id) < 0);
    prev = id;
  }
});

test("variable base doubles with depth", () => {
  assert.deepEqual(
    [0, 1, 2, 3, 4].map(baseAt),
    [8, 16, 32, 64, 128],
  );
});

test("acceptance: 1000 dense alternating front/back inserts, gentle depth growth", () => {
  const seq = new LSEQSequence(1, mulberry32(42));
  const expected: string[] = [];

  for (let i = 0; i < 1000; i++) {
    const ch = String.fromCharCode(33 + (i % 94));
    if (i % 2 === 0) {
      seq.insert(0, ch); // dense prepend
      expected.unshift(ch);
    } else {
      seq.insert(seq.length, ch); // dense append
      expected.push(ch);
    }
  }

  assert.equal(seq.toArray().join(""), expected.join(""));
  assertStrictlyIncreasing(seq.identifiers());

  const { maxDepth, meanDepth } = seq.stats();
  // log2(1000) ~= 10: depth must stay poly-logarithmic, never linear.
  assert.ok(maxDepth <= 16, `maxDepth ${maxDepth} should stay <= 16`);
  assert.ok(meanDepth <= 8, `meanDepth ${meanDepth} should stay <= 8`);
});

test("acceptance: multi-replica random-permutation merge preserves strict causal order", () => {
  const SITES = 4;
  const OPS_PER_SITE = 250;
  const allOps: InsertOp[] = [];

  // Every replica edits its own live view; ops are collected for broadcast.
  const replicas = Array.from(
    { length: SITES },
    (_, i) => new LSEQSequence(i + 1, mulberry32(1000 + i)),
  );
  for (const [i, replica] of replicas.entries()) {
    const rng = mulberry32(5000 + i);
    for (let k = 0; k < OPS_PER_SITE; k++) {
      const index = Math.floor(rng() * (replica.length + 1));
      const op = replica.insert(index, `s${i + 1}#${k};`);
      allOps.push(op);
    }
  }

  // Late-joining replica receives every op in a random permutation.
  const late = new LSEQSequence(99, mulberry32(9));
  late.merge(shuffled(allOps, mulberry32(31337)));

  // And again, in a different permutation, into another fresh replica.
  const late2 = new LSEQSequence(100, mulberry32(10));
  late2.merge(shuffled(allOps, mulberry32(777)));

  const expectedTotal = SITES * OPS_PER_SITE;
  assert.equal(late.length, expectedTotal);
  assert.equal(late2.length, expectedTotal);

  // Convergence: both late joiners agree on the exact same sequence.
  assert.deepEqual(late.toArray(), late2.toArray());

  // Strict causal order: identifiers strictly increasing along the sequence.
  assertStrictlyIncreasing(late.identifiers());

  // Merging is idempotent: re-applying everything changes nothing.
  assert.equal(late.merge(shuffled(allOps, mulberry32(1))), 0);
  assert.equal(late.length, expectedTotal);

  // Cross-check: interleaving per site is preserved (per-site ops stay ordered
  // by the identifiers each replica generated on its own live view).
  for (const [i, replica] of replicas.entries()) {
    const mine = replica.toArray().join("");
    const inLate = late
      .toArray()
      .join("")
      .match(new RegExp(`s${i + 1}#\\d+;`, "g"))!
      .join("");
    assert.equal(inLate, mine);
  }
});

test("concurrent inserts at the same position converge across replicas", () => {
  const a = new LSEQSequence(1, mulberry32(11));
  const b = new LSEQSequence(2, mulberry32(22));
  const seed = a.insert(0, "X");
  b.apply(seed);

  // Both insert at index 0 concurrently.
  const opA = a.insert(0, "A");
  const opB = b.insert(0, "B");

  // Exchange ops in opposite orders.
  a.apply(opB);
  b.apply(opA);

  assert.equal(a.toString(), b.toString());
  assertStrictlyIncreasing(a.identifiers());
  assertStrictlyIncreasing(b.identifiers());
});
