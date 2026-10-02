import { test } from "node:test";
import assert from "node:assert/strict";
import { ClusterNode, DIGEST_BYTES } from "../src/node.ts";
import { MAX_ROUNDS, runSimulation } from "../src/simulation.ts";
import type { MemberRecord } from "../src/membership.ts";

const rec = (id: string, status: MemberRecord["status"], incarnation: number): MemberRecord => ({
  id,
  status,
  incarnation,
});

test("in-sync probe costs only the 4-byte digest exchange", () => {
  const a = new ClusterNode("a");
  const b = new ClusterNode("b");
  for (const id of ["m1", "m2", "m3"]) {
    a.updateLocal(rec(id, "alive", 1));
    b.updateLocal(rec(id, "alive", 1));
  }
  a.connect(b);
  const outcome = a.probe(b);
  assert.equal(outcome.inSync, true);
  assert.equal(outcome.digestBytesExchanged, DIGEST_BYTES * 2);
  assert.equal(outcome.recordBytesExchanged, 0);
  assert.deepEqual(outcome.differingBuckets, []);
});

test("digest mismatch triggers interval reconciliation and aligns both nodes", () => {
  const a = new ClusterNode("a");
  const b = new ClusterNode("b");
  for (const id of ["m1", "m2", "m3", "m4"]) {
    a.updateLocal(rec(id, "alive", 1));
    b.updateLocal(rec(id, "alive", 1));
  }
  // Diverge: a knows about a join, b has a newer suspicion.
  a.updateLocal(rec("m5", "alive", 1));
  b.updateLocal(rec("m2", "suspect", 2));
  a.connect(b);

  const outcome = a.probe(b);
  assert.equal(outcome.inSync, false);
  assert.ok(outcome.digestBytesExchanged >= DIGEST_BYTES * 2);
  assert.deepEqual(
    outcome.differingBuckets,
    [...new Set([a.table.bucketOf("m5"), a.table.bucketOf("m2")])].sort((x, y) => x - y),
  );

  // Bidirectional alignment: identical digest and identical records.
  assert.equal(a.table.digest, b.table.digest);
  assert.deepEqual(a.table.snapshot(), b.table.snapshot());
  assert.deepEqual(a.table.get("m5"), rec("m5", "alive", 1));
  assert.deepEqual(b.table.get("m5"), rec("m5", "alive", 1));
  assert.deepEqual(a.table.get("m2"), rec("m2", "suspect", 2));
});

test("reconciliation transfers only records inside differing buckets", () => {
  const a = new ClusterNode("a");
  const b = new ClusterNode("b");
  for (let i = 0; i < 40; i += 1) {
    a.updateLocal(rec(`member-${i}`, "alive", 1));
    b.updateLocal(rec(`member-${i}`, "alive", 1));
  }
  a.updateLocal(rec("member-7", "suspect", 2)); // single diverging entry
  a.connect(b);

  const outcome = a.probe(b);
  assert.equal(outcome.inSync, false);
  assert.deepEqual(outcome.differingBuckets, [a.table.bucketOf("member-7")]);
  assert.equal(outcome.recordsPushed, 1);
  assert.equal(outcome.recordsPulled, 0);
  assert.equal(a.table.digest, b.table.digest);
});

test("probing an unconnected peer is rejected", () => {
  const a = new ClusterNode("a");
  const b = new ClusterNode("b");
  assert.throws(() => a.probe(b), /not connected/);
});

test("partition heal: full bidirectional alignment within 5 rounds", () => {
  const result = runSimulation();
  assert.equal(result.converged, true);
  assert.ok(
    result.rounds <= MAX_ROUNDS,
    `expected convergence within ${MAX_ROUNDS} rounds, took ${result.rounds}`,
  );
  assert.ok(result.stats.mismatchesDetected >= 1);

  // Every node holds the identical view: same digest, same records.
  const reference = result.nodes[0];
  for (const node of result.nodes) {
    assert.equal(node.table.digest, reference.table.digest);
    assert.deepEqual(node.table.snapshot(), reference.table.snapshot());
  }

  // The reconciled view is the union of both partitioned views,
  // resolved by incarnation: 8 members, all alive.
  const snapshot = reference.table.snapshot();
  assert.equal(snapshot.length, 8);
  for (const record of snapshot) {
    assert.equal(record.status, "alive", `${record.id} should be alive`);
  }
  assert.deepEqual(reference.table.get("L1"), rec("L1", "alive", 3));
  assert.deepEqual(reference.table.get("L2"), rec("L2", "alive", 2));
  assert.deepEqual(reference.table.get("R3"), rec("R3", "alive", 3));
  assert.deepEqual(reference.table.get("L4"), rec("L4", "alive", 1));
  assert.deepEqual(reference.table.get("R4"), rec("R4", "alive", 1));

  // Fast comparison really was cheap: digest traffic stays in the
  // hundreds of bytes while the cluster holds tens of members.
  assert.ok(result.stats.digestBytesExchanged < 4096);
});
