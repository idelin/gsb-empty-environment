import test from "node:test";
import assert from "node:assert/strict";
import {
  OnlineMerger,
  OfflineVerifier,
  HmacSigner,
  canonicalBytes,
  parseDot,
  compareDots,
  dotId,
} from "../src/keylog/index.ts";
import { Builder, keyMap, rng, shuffled } from "./fixture.ts";

const OBJ = "obj_perm_123";

async function ingestAll(target, envelopes, verify) {
  const results = [];
  for (const envelope of envelopes) {
    results.push(target.merge ? await target.merge(envelope, verify) : await target.replay(envelope, verify));
  }
  return results;
}

function handlesOf(report) {
  return report.claimable.map((h) => `${h.generation.toString(10)}:${h.handle}`).sort();
}

test("aliased names never split admission: permanent object id is the only key", async () => {
  const keys = keyMap(["owner", "alice"]);
  const b = new Builder(OBJ, keys);
  const genesis = b.rebuild("owner", 0n, [], "k0");
  const grant = b.grant("owner", 0n, [genesis.event.dot], "alice", "k0");
  const log = [genesis, grant];

  const online = new OnlineMerger();
  const offline = new OfflineVerifier();
  await ingestAll(online, log, b.signer.verify);
  await ingestAll(offline, log, b.signer.verify);

  assert.equal(online.objectId, OBJ);
  assert.equal(offline.objectId, OBJ);
  assert.deepEqual(handlesOf(online.report("alice")), ["0:k0"]);
  assert.deepEqual(handlesOf(offline.report("alice")), ["0:k0"]);

  const foreign = new Builder("obj_perm_999", keys).rebuild("owner", 0n, [], "k0");
  const result = await online.merge(foreign, b.signer.verify);
  assert.equal(result.outcome, "rejected");
});

test("delete closes only its generation; late envelopes cannot resurrect it; rebuild enables the next", async () => {
  const keys = keyMap(["owner", "alice"]);
  const b = new Builder(OBJ, keys);
  const genesis = b.rebuild("owner", 0n, [], "k0");
  const oldGrant = b.grant("owner", 0n, [genesis.event.dot], "alice", "k0");
  const del = b.delete("owner", 0n, [genesis.event.dot, oldGrant.event.dot]);
  const lateGrant = b.grant("owner", 0n, [genesis.event.dot], "alice", "k0-late");
  const rebuild = b.rebuild("owner", 1n, [del.event.dot], "k1");
  const newGrant = b.grant("owner", 1n, [rebuild.event.dot], "alice", "k1");
  const log = [genesis, oldGrant, del, lateGrant, rebuild, newGrant];

  const offline = new OfflineVerifier();
  await ingestAll(offline, log, b.signer.verify);
  const report = offline.report("alice");
  assert.deepEqual(handlesOf(report), ["1:k1"]);
  const closed = report.decidedDenials
    .filter((d) => d.status === "closed")
    .map((d) => d.edgeId)
    .sort();
  assert.ok(closed.includes(dotId(oldGrant.event.dot)));
  assert.ok(closed.includes(dotId(lateGrant.event.dot)));
  assert.deepEqual(handlesOf(offline.report("owner")), ["1:k1"]);
});

test("multiple rebuilds are isolated per generation", async () => {
  const keys = keyMap(["owner", "alice"]);
  const b = new Builder(OBJ, keys);
  const g0 = b.rebuild("owner", 0n, [], "k0");
  const grant0 = b.grant("owner", 0n, [g0.event.dot], "alice", "k0");
  const d0 = b.delete("owner", 0n, [g0.event.dot, grant0.event.dot]);
  const r1 = b.rebuild("owner", 1n, [d0.event.dot], "k1");
  const d1 = b.delete("owner", 1n, [r1.event.dot]);
  const r2 = b.rebuild("owner", 2n, [d1.event.dot], "k2");
  const grant2 = b.grant("owner", 2n, [r2.event.dot], "alice", "k2");

  const online = new OnlineMerger();
  await ingestAll(online, [g0, grant0, d0, r1, d1, r2, grant2], b.signer.verify);
  assert.deepEqual(handlesOf(online.report("alice")), ["2:k2"]);
});

test("revoke and delegate that never see each other resolve fail-closed", async () => {
  const keys = keyMap(["owner", "bob", "carol"]);
  const b = new Builder(OBJ, keys);
  const genesis = b.rebuild("owner", 0n, [], "k0");
  const grant = b.grant("owner", 0n, [genesis.event.dot], "bob", "k0");
  const revoke = b.revoke("owner", 0n, [genesis.event.dot, grant.event.dot], grant.event.dot);
  const delegate = b.delegate("bob", 0n, [genesis.event.dot, grant.event.dot], "carol", 1);
  const carolGrant = b.grant("bob", 0n, [delegate.event.dot], "carol", "k0");
  const log = [genesis, grant, revoke, delegate, carolGrant];

  for (const order of [log, [...log].reverse(), shuffled(log, rng(42))]) {
    const offline = new OfflineVerifier();
    await ingestAll(offline, order, b.signer.verify);
    assert.deepEqual(handlesOf(offline.report("bob")), []);
    assert.ok(offline.report("bob").decidedDenials.some((d) => d.status === "revoked"));
    assert.deepEqual(handlesOf(offline.report("carol")), []);
  }
});

test("delegation chain grants authority up to maxDepth and stops beyond it", async () => {
  const keys = keyMap(["owner", "a1", "a2", "a3"]);
  const b = new Builder(OBJ, keys);
  const genesis = b.rebuild("owner", 0n, [], "k0");
  const d1 = b.delegate("owner", 0n, [genesis.event.dot], "a1", 1);
  const d2 = b.delegate("a1", 0n, [d1.event.dot], "a2", 1);
  const g2 = b.grant("a2", 0n, [d2.event.dot], "a2", "k0");
  const d3 = b.delegate("a2", 0n, [d2.event.dot], "a3", 1);
  const g3 = b.grant("a3", 0n, [d3.event.dot], "a3", "k0");

  const online = new OnlineMerger();
  await ingestAll(online, [genesis, d1, d2, g2, d3, g3], b.signer.verify);
  assert.deepEqual(handlesOf(online.report("a2")), ["0:k0"]);
  assert.deepEqual(handlesOf(online.report("a3")), []);
  assert.ok(online.report("a3").decidedDenials.some((d) => d.status === "denied"));
});

test("old backup replay after delete cannot read; new generation stays usable", async () => {
  const keys = keyMap(["owner", "alice"]);
  const b = new Builder(OBJ, keys);
  const genesis = b.rebuild("owner", 0n, [], "k0");
  const oldGrant = b.grant("owner", 0n, [genesis.event.dot], "alice", "k0");
  const del = b.delete("owner", 0n, [genesis.event.dot, oldGrant.event.dot]);
  const rebuild = b.rebuild("owner", 1n, [del.event.dot], "k1");

  const current = new OnlineMerger();
  await ingestAll(current, [genesis, oldGrant, del, rebuild], b.signer.verify);
  const summary = current.summary();

  const restored = new OnlineMerger();
  restored.mergeSummary(summary);
  const replay = await restored.merge(oldGrant, b.signer.verify);
  assert.equal(replay.outcome, "duplicate");
  assert.deepEqual(handlesOf(restored.report("alice")), []);
  assert.ok(restored.report("alice").decidedDenials.some((d) => d.status === "closed"));
});

test("gaps are explicit and never read as the author having written nothing", async () => {
  const keys = keyMap(["owner", "alice"]);
  const b = new Builder(OBJ, keys);
  const genesis = b.rebuild("owner", 0n, [], "k0");
  const missing = b.grant("owner", 0n, [genesis.event.dot], "alice", "k0");
  const later = b.delete("owner", 0n, [genesis.event.dot, missing.event.dot]);

  // A genuine compressed summary (real fingerprints), then drop the missing
  // grant record to simulate an incomplete suffix with the frontier retained.
  const source = new OnlineMerger();
  await ingestAll(source, [genesis, missing, later], b.signer.verify);
  const fullSummary = source.summary();
  const partialSummary = {
    ...fullSummary,
    records: fullSummary.records.filter(
      (r) => r.id !== dotId(missing.event.dot),
    ),
  };
  const online = new OnlineMerger();
  online.mergeSummary(partialSummary);
  // Genesis is retained, only the sequence-2 grant is missing.
  assert.deepEqual(online.gaps(), [dotId(missing.event.dot)]);

  const partial = new OfflineVerifier();
  await partial.replay(later, b.signer.verify);
  assert.deepEqual(partial.gaps(), [dotId(genesis.event.dot), dotId(missing.event.dot)]);
});

test("equivocation at one dot quarantines the author's authority but keeps denies", async () => {
  const keys = keyMap(["owner", "mallory", "victim"]);
  const b = new Builder(OBJ, keys);
  const genesis = b.rebuild("owner", 0n, [], "k0");
  const delegate = b.delegate("owner", 0n, [genesis.event.dot], "mallory", 2);
  const goodGrant = b.grant("mallory", 0n, [delegate.event.dot], "victim", "k0");
  const evilGrant = b.grant(
    "mallory",
    0n,
    [delegate.event.dot],
    "intruder",
    "k0",
    goodGrant.event.dot.sequence,
  );

  const offline = new OfflineVerifier();
  await ingestAll(offline, [genesis, delegate, goodGrant], b.signer.verify);
  assert.deepEqual(handlesOf(offline.report("victim")), ["0:k0"]);
  const conflict = await offline.replay(evilGrant, b.signer.verify);
  assert.equal(conflict.outcome, "equivocation");
  assert.deepEqual(offline.equivocatedActors(), ["mallory"]);
  assert.deepEqual(handlesOf(offline.report("victim")), []);
  assert.ok(offline.report("victim").decidedDenials.some((d) => d.status === "tainted"));
});

test("uint64 sequences beyond Number.MAX_SAFE_INTEGER stay exact", async () => {
  const keys = keyMap(["owner", "alice"]);
  const b = new Builder(OBJ, keys);
  const huge = 18446744073709551600n;
  assert.ok(huge > BigInt(Number.MAX_SAFE_INTEGER));
  const genesis = b.rebuild("owner", 0n, [], "k0", huge);
  const grant = b.grant("owner", 0n, [genesis.event.dot], "alice", "k0", huge + 1n);
  assert.equal(dotId(genesis.event.dot), `${huge.toString(10)}@owner`);
  assert.equal(parseDot(dotId(genesis.event.dot)).sequence, huge);

  const offline = new OfflineVerifier();
  await ingestAll(offline, [genesis, grant], b.signer.verify);
  assert.deepEqual(handlesOf(offline.report("alice")), ["0:k0"]);
  assert.ok(compareDots(parseDot(`${huge}@a`), parseDot(`${huge + 1n}@a`)) < 0);
});

test("online and offline entries agree across many randomized delivery orders", async () => {
  const keys = keyMap(["owner", "alice", "bob"]);
  const b = new Builder(OBJ, keys);
  const genesis = b.rebuild("owner", 0n, [], "k0");
  const gA = b.grant("owner", 0n, [genesis.event.dot], "alice", "k0");
  const gB = b.grant("owner", 0n, [genesis.event.dot], "bob", "k0");
  const revoke = b.revoke("owner", 0n, [gB.event.dot], gB.event.dot);
  const del = b.delete(
    "owner",
    0n,
    [genesis.event.dot, gA.event.dot, gB.event.dot, revoke.event.dot],
  );
  const rebuild = b.rebuild("owner", 1n, [del.event.dot], "k1");
  const gA2 = b.grant("owner", 1n, [rebuild.event.dot], "alice", "k1");
  const log = [genesis, gA, gB, revoke, del, rebuild, gA2];

  const reference = new OfflineVerifier();
  await ingestAll(reference, log, b.signer.verify);
  const expected = ["owner", "alice", "bob"].map((s) => ({
    subject: s,
    claimable: handlesOf(reference.report(s)),
    pending: reference.report(s).pending.length,
    denied: reference.report(s).decidedDenials.length,
  }));

  const random = rng(7);
  for (let iteration = 0; iteration < 40; iteration++) {
    const order = shuffled(log, random);
    const online = new OnlineMerger();
    const offline = new OfflineVerifier();
    await ingestAll(online, order, b.signer.verify);
    await ingestAll(offline, order, b.signer.verify);
    for (const subject of ["owner", "alice", "bob"]) {
      assert.deepEqual(handlesOf(online.report(subject)), expected.find((e) => e.subject === subject).claimable);
      assert.deepEqual(handlesOf(offline.report(subject)), expected.find((e) => e.subject === subject).claimable);
      assert.equal(online.report(subject).pending.length, offline.report(subject).pending.length);
      assert.equal(
        online.report(subject).decidedDenials.length,
        offline.report(subject).decidedDenials.length,
      );
    }
  }
});

test("compressed summary round-trips and yields the same admission as the full log", async () => {
  const keys = keyMap(["owner", "alice"]);
  const b = new Builder(OBJ, keys);
  const genesis = b.rebuild("owner", 0n, [], "k0");
  const grant = b.grant("owner", 0n, [genesis.event.dot], "alice", "k0");
  const del = b.delete("owner", 0n, [genesis.event.dot, grant.event.dot]);
  const rebuild = b.rebuild("owner", 1n, [del.event.dot], "k1");
  const grant2 = b.grant("owner", 1n, [rebuild.event.dot], "alice", "k1");
  const log = [genesis, grant, del, rebuild, grant2];

  const source = new OnlineMerger();
  await ingestAll(source, log, b.signer.verify);
  const summary = source.summary();
  // Online evidence carries no signatures/envelopes, only compact records.
  assert.ok(!JSON.stringify(summary).includes("signature"));

  const fed = new OnlineMerger();
  fed.mergeSummary(summary);
  assert.deepEqual(handlesOf(fed.report("alice")), ["1:k1"]);
});

test("revoke GC is allowed only after the generation is closed or disabled", async () => {
  const keys = keyMap(["owner", "bob"]);
  const b = new Builder(OBJ, keys);
  const genesis = b.rebuild("owner", 0n, [], "k0");
  const grant = b.grant("owner", 0n, [genesis.event.dot], "bob", "k0");
  const revoke = b.revoke("owner", 0n, [grant.event.dot], grant.event.dot);

  const open = new OfflineVerifier();
  await ingestAll(open, [genesis, grant, revoke], b.signer.verify);
  // Generation still open: a never-online replica could replay the grant.
  assert.equal(open.canDiscardRevoke(dotId(revoke.event.dot)), false);

  const del = b.delete("owner", 0n, [genesis.event.dot, grant.event.dot, revoke.event.dot]);
  const closed = new OfflineVerifier();
  await ingestAll(closed, [genesis, grant, revoke, del], b.signer.verify);
  assert.equal(closed.canDiscardRevoke(dotId(revoke.event.dot)), true);
  assert.deepEqual(closed.discardableRevokes(), [dotId(revoke.event.dot)]);
});

test("each permission kind has distinct signed bytes, so a signature cannot be replayed across kinds", async () => {
  const keys = keyMap(["owner"]);
  const b = new Builder(OBJ, keys);
  const genesis = b.rebuild("owner", 0n, [], "k0");
  const grant = b.grant("owner", 0n, [genesis.event.dot], "x", "k0");
  const del = b.delete("owner", 0n, [genesis.event.dot]);
  assert.notDeepEqual(canonicalBytes(grant.event), canonicalBytes(del.event));
  // A forged envelope (wrong key) is rejected by verification.
  const forgedKeys = keyMap(["owner"]);
  forgedKeys.set("owner", new TextEncoder().encode("secret:attacker"));
  const forged = new HmacSigner(forgedKeys).sign(genesis.event);
  const offline = new OfflineVerifier();
  const result = await offline.replay(forged, b.signer.verify);
  assert.equal(result.outcome, "rejected");
});
