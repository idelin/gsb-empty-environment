import { test } from "node:test";
import assert from "node:assert/strict";
import {
  MembershipTable,
  shouldReplace,
  type MemberRecord,
} from "../src/membership.ts";

const rec = (id: string, status: MemberRecord["status"], incarnation: number): MemberRecord => ({
  id,
  status,
  incarnation,
});

test("incremental digest always equals a from-scratch recompute", () => {
  const table = new MembershipTable();
  const history: MemberRecord[] = [];
  const ops: MemberRecord[] = [
    rec("a", "alive", 1),
    rec("b", "alive", 1),
    rec("c", "alive", 1),
    rec("b", "suspect", 2),
    rec("a", "dead", 2),
    rec("d", "alive", 1),
    rec("b", "alive", 3),
  ];
  for (const op of ops) {
    table.upsert(op);
    assert.equal(
      table.digest,
      MembershipTable.recomputeDigest(table.snapshot()),
      `digest drifted after applying ${JSON.stringify(op)}`,
    );
    history.push(op);
  }
});

test("digest is order-independent (commutative ring checksum)", () => {
  const records = [
    rec("n1", "alive", 1),
    rec("n2", "suspect", 4),
    rec("n3", "dead", 2),
    rec("n4", "alive", 7),
  ];
  const forward = new MembershipTable();
  const reverse = new MembershipTable();
  for (const r of records) forward.upsert(r);
  for (const r of [...records].reverse()) reverse.upsert(r);
  assert.equal(forward.digest, reverse.digest);
  assert.deepEqual(forward.bucketDigests(), reverse.bucketDigests());
});

test("diverged views produce different 4-byte digests", () => {
  const left = new MembershipTable();
  const right = new MembershipTable();
  for (const id of ["a", "b", "c"]) {
    left.upsert(rec(id, "alive", 1));
    right.upsert(rec(id, "alive", 1));
  }
  assert.equal(left.digest, right.digest);
  left.upsert(rec("d", "alive", 1)); // only left sees the join
  assert.notEqual(left.digest, right.digest);
});

test("merge rule: higher incarnation wins", () => {
  assert.equal(
    shouldReplace(rec("x", "dead", 3), rec("x", "alive", 4)),
    true,
  );
  assert.equal(
    shouldReplace(rec("x", "alive", 4), rec("x", "dead", 3)),
    false,
  );
});

test("merge rule: equal incarnation breaks ties by status rank", () => {
  assert.equal(shouldReplace(rec("x", "alive", 2), rec("x", "suspect", 2)), true);
  assert.equal(shouldReplace(rec("x", "suspect", 2), rec("x", "dead", 2)), true);
  assert.equal(shouldReplace(rec("x", "dead", 2), rec("x", "alive", 2)), false);
  assert.equal(shouldReplace(rec("x", "alive", 2), rec("x", "alive", 2)), false);
});

test("stale upserts are rejected and leave the digest untouched", () => {
  const table = new MembershipTable();
  table.upsert(rec("a", "alive", 5));
  const before = table.digest;
  assert.equal(table.upsert(rec("a", "suspect", 4)), false);
  assert.equal(table.digest, before);
  assert.deepEqual(table.get("a"), rec("a", "alive", 5));
});

test("bucket digests locate exactly the diverging intervals", () => {
  const a = new MembershipTable();
  const b = new MembershipTable();
  const shared = ["m1", "m2", "m3", "m4", "m5"];
  for (const id of shared) {
    a.upsert(rec(id, "alive", 1));
    b.upsert(rec(id, "alive", 1));
  }
  a.upsert(rec("only-a", "alive", 1));
  b.upsert(rec("m2", "suspect", 2)); // b has a newer view of m2

  const digA = a.bucketDigests();
  const digB = b.bucketDigests();
  const expected = new Set<number>();
  expected.add(a.bucketOf("only-a"));
  expected.add(a.bucketOf("m2"));

  const differing = new Set<number>();
  for (let i = 0; i < digA.length; i += 1) {
    if (digA[i] !== digB[i]) differing.add(i);
  }
  assert.deepEqual(differing, expected);

  const pulled = b.recordsInBuckets(differing).map((r) => r.id).sort();
  assert.deepEqual(pulled, [...new Set(b.recordsInBuckets(differing).map((r) => r.id))].sort());
  assert.ok(pulled.includes("m2"));
  assert.ok(!pulled.includes("only-a"));
});
