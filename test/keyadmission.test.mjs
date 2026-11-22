import test from "node:test";
import assert from "node:assert/strict";
import {
  KeyAdmissionMerger,
  canDiscardRevoke,
  canonicalEventBytes,
  eventId,
  parseAdmissionData,
  signEvent,
  stringifyAdmissionData,
  verifyKeyLog,
} from "../src/keyadmission.ts";
import { compareDots, formatDot, parseDot } from "../src/dot.ts";
import { HarnessRegistry, LocalLog, ownerRights, shuffle } from "./support/harness.ts";

const OBJECT = "obj-1";

function buildRegistry() {
  const registry = new HarnessRegistry();
  registry.addPrincipal("owner", ownerRights());
  registry.addPrincipal("admin", ["grant", "delegate", "revoke", "delete"]);
  registry.addPrincipal("alice", ["grant", "delegate", "revoke"]);
  registry.addPrincipal("bob", ["delegate"]);
  registry.addPrincipal("carol", ["delegate"]);
  registry.addPrincipal("dave", []);
  registry.addPrincipal("restorer", ["rebuild"]);
  registry.setOwner(OBJECT, "owner");
  return registry;
}

function mergeAll(registry, objectId, events, summaries = []) {
  const merger = new KeyAdmissionMerger(objectId, registry);
  for (const summary of summaries) assert.equal(merger.ingestSummary(summary).status, "stored");
  for (const event of events) {
    const result = merger.ingest(event);
    assert.notEqual(result.status, "rejected", `unexpected reject: ${result.reason}`);
  }
  return merger;
}

function claimIndex(view) {
  return new Map(view.claims.map((claim) => [`${claim.subject}:${claim.handle}:g${claim.generation}`, claim]));
}

test("alias document names resolve to one permanent object id", () => {
  const registry = buildRegistry();
  const log = new LocalLog(registry);
  const rebuild = log.tick(OBJECT, "owner", "rebuild", { keyContext: "root" }, { generation: 0 });
  const grant = log.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "k1" }, { generation: 0 });

  // The host may display two alias names, but admission keys on the object id.
  const byNameA = mergeAll(registry, OBJECT, [rebuild, grant]);
  const byNameB = mergeAll(registry, OBJECT, shuffle([grant, rebuild], 7));
  assert.deepEqual(byNameA.view().admitted.map((c) => c.subject), ["alice"]);
  assert.deepEqual(byNameB.view().admitted.map((c) => c.subject), ["alice"]);
  assert.ok(!JSON.stringify(canonicalEventBytes(rebuild)).includes("alias"));
});

test("delete closes only its generation; old envelopes never come back; rebuild re-enables a new one", () => {
  const registry = buildRegistry();
  const log = new LocalLog(registry);
  const rebuild0 = log.tick(OBJECT, "owner", "rebuild", { keyContext: "g0" }, { generation: 0 });
  const grant0 = log.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "old-key" }, { generation: 0 });
  // A stale replica's late envelope, authored before the delete but arriving after.
  const stale0 = log.tick(OBJECT, "bob", "delegate", { parent: grant0.dot, subject: "dave", handle: "old-key" }, { generation: 0 });
  const del = log.tick(OBJECT, "admin", "delete", { reason: "purge" }, { generation: 0 });
  const rebuild1 = log.tick(OBJECT, "owner", "rebuild", { keyContext: "g1" }, { generation: 1 });
  const grant1 = log.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "new-key" }, { generation: 1 });

  // Delivery order 1: stale envelope arrives last, like an old backup online.
  const merger = mergeAll(registry, OBJECT, [rebuild0, grant0, del, rebuild1, grant1, stale0]);
  const view = merger.view();
  const claims = claimIndex(view);
  assert.equal(claims.get("alice:old-key:g0").verdict, "denied");
  assert.equal(claims.get("dave:old-key:g0").verdict, "denied");
  assert.equal(claims.get("alice:new-key:g1").verdict, "admitted");
  assert.deepEqual(view.generations.map((g) => g.state), ["closed", "enabled"]);

  // Delivery order 2: reverse arrival must yield identical verdicts.
  const reverse = mergeAll(registry, OBJECT, [stale0, grant1, rebuild1, del, grant0, rebuild0]);
  assert.deepEqual(
    reverse.view().claims.map((c) => [c.subject, c.handle, c.verdict]),
    view.claims.map((c) => [c.subject, c.handle, c.verdict]),
  );
});

test("multiple rebuilds create independent generations and conflicted rebuilds fail closed", () => {
  const registry = buildRegistry();
  const log = new LocalLog(registry);
  const events = [
    log.tick(OBJECT, "owner", "rebuild", { keyContext: "g0" }, { generation: 0 }),
    log.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "h0" }, { generation: 0 }),
    log.tick(OBJECT, "admin", "delete", {}, { generation: 0 }),
    log.tick(OBJECT, "owner", "rebuild", { keyContext: "g1" }, { generation: 1 }),
    log.tick(OBJECT, "alice", "grant", { subject: "bob", handle: "h1" }, { generation: 1 }),
    log.tick(OBJECT, "admin", "delete", {}, { generation: 1 }),
    log.tick(OBJECT, "owner", "rebuild", { keyContext: "g2" }, { generation: 2 }),
    log.tick(OBJECT, "alice", "grant", { subject: "carol", handle: "h2" }, { generation: 2 }),
  ];
  const view = mergeAll(registry, OBJECT, shuffle(events, 13)).view();
  assert.deepEqual(view.generations.map((g) => g.state), ["closed", "closed", "enabled"]);
  assert.deepEqual(view.admitted.map((c) => `${c.subject}:${c.handle}`), ["carol:h2"]);

  // Two owner rebuilds for the same generation: conflict, fail closed.
  const log2 = new LocalLog(registry);
  const r1 = log2.tick(OBJECT, "owner", "rebuild", { keyContext: "a" }, { generation: 0, sequence: 1n });
  const r2 = log2.tick(OBJECT, "owner", "rebuild", { keyContext: "b" }, { generation: 0, sequence: 2n });
  const g = log2.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "x" }, { generation: 0 });
  const conflicted = mergeAll(registry, OBJECT, [r1, r2, g]).view();
  assert.equal(conflicted.generations[0].state, "conflicted");
  assert.equal(claimIndex(conflicted).get("alice:x:g0").verdict, "denied");
});

test("mutually unseen revoke and delegate: causal scope and fail-closed forks", () => {
  const registry = buildRegistry();
  const log = new LocalLog(registry);
  const rebuild = log.tick(OBJECT, "owner", "rebuild", { keyContext: "g0" }, { generation: 0 });
  const root = log.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "k" }, { generation: 0 });
  // Two forks: neither sees the other.
  const revoke = log.tick(OBJECT, "alice", "revoke", { target: root.dot }, { generation: 0 });
  const delegate = log.tick(OBJECT, "bob", "delegate", { parent: root.dot, subject: "carol", handle: "k" }, { generation: 0 });

  for (const order of [
    [rebuild, root, revoke, delegate],
    [rebuild, root, delegate, revoke],
    [delegate, revoke, root, rebuild],
  ]) {
    const view = mergeAll(registry, OBJECT, order).view();
    const claims = claimIndex(view);
    assert.equal(claims.get("alice:k:g0").verdict, "denied");
    // The fork itself cannot be killed by a tombstone it never saw, but its
    // parent root is definitively dead (direct revoke), so the branch cannot
    // be admitted either.
    assert.equal(claims.get("carol:k:g0").verdict, "denied");
    assert.match(claims.get("carol:k:g0").reasons.join(","), /parent-dead/);
  }

  // Once a replica observes the revoke before delegating, the branch is dead.
  log.observe("carol", "alice", log.sequenceOf("alice"));
  const late = log.tick(OBJECT, "carol", "delegate", { parent: delegate.dot, subject: "dave", handle: "k" }, { generation: 0 });
  const lateView = mergeAll(registry, OBJECT, [rebuild, root, revoke, delegate, late]).view();
  // The intermediate fork never saw the tombstone: its own status stays
  // undecided, and that pending state propagates to descendants. Nothing is
  // admitted on unknown evidence.
  assert.equal(claimIndex(lateView).get("dave:k:g0").verdict, "pending");
  assert.match(claimIndex(lateView).get("dave:k:g0").reasons.join(","), /parent-pending|revoke-concurrent/);

  // A fresh delegate taken directly from the killed root after observing the
  // revoke is, however, definitively denied.
  const after = log.tick(OBJECT, "carol", "delegate", { parent: root.dot, subject: "dave", handle: "k" }, { generation: 0 });
  const afterView = mergeAll(registry, OBJECT, [rebuild, root, revoke, after]).view();
  assert.equal(claimIndex(afterView).get("dave:k:g0").verdict, "denied");
  assert.match(claimIndex(afterView).get("dave:k:g0").reasons.join(","), /parent-dead/);

  // And the verdict never depends on compareDots storage order.
  const sorted = mergeAll(
    registry,
    OBJECT,
    [rebuild, root, revoke, delegate].sort((a, b) => compareDots(a.dot, b.dot)),
  );
  assert.equal(claimIndex(sorted.view()).get("carol:k:g0").verdict, "denied");
});

test("a revoke aimed at a missing edge stays pending and cannot be silently dropped", () => {
  const registry = buildRegistry();
  const log = new LocalLog(registry);
  const rebuild = log.tick(OBJECT, "owner", "rebuild", { keyContext: "g0" }, { generation: 0 });
  const grant = log.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "k" }, { generation: 0 });
  // A different edge (3@alice) exists somewhere but is absent from this replica.
  const tombstone = log.tick(OBJECT, "alice", "revoke", { target: parseDot("3@alice") }, { generation: 0 });
  const summary = log.frontier(OBJECT, "owner", { alice: 3n, owner: 1n });

  const merger = new KeyAdmissionMerger(OBJECT, registry);
  merger.ingestSummary(summary);
  merger.ingest(rebuild);
  merger.ingest(grant);
  merger.ingest(tombstone);
  const view = merger.view();
  assert.equal(view.complete, false);
  // The known grant is not the revoke target, but the gap on a negating
  // author means the full authority picture is unknown: hold pending.
  assert.equal(claimIndex(view).get("alice:k:g0").verdict, "pending");

  // Offline recomputation over the same raw subset has no gap evidence (no
  // sealed summary), so the partial log itself yields admitted: the pending
  // verdict exists only where evidence of a missing prefix was attested.
  const offline = verifyKeyLog(OBJECT, [rebuild, grant, tombstone], registry).view;
  assert.equal(claimIndex(offline).get("alice:k:g0").verdict, "admitted");

  // The tombstone itself is not garbage collectable while the generation is open.
  assert.equal(canDiscardRevoke(tombstone.dot, merger.snapshot()), false);
});

test("revoke target generation is scoped; deletes never reach other generations", () => {
  const registry = buildRegistry();
  const log = new LocalLog(registry);
  const r0 = log.tick(OBJECT, "owner", "rebuild", { keyContext: "g0" }, { generation: 0 });
  const g0 = log.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "k" }, { generation: 0 });
  const del0 = log.tick(OBJECT, "admin", "delete", {}, { generation: 0 });
  const r1 = log.tick(OBJECT, "owner", "rebuild", { keyContext: "g1" }, { generation: 1 });
  const g1 = log.tick(OBJECT, "alice", "grant", { subject: "bob", handle: "k" }, { generation: 1 });

  // A g1 tombstone that claims a g0 target is rejected at ingestion.
  const wrongGen = signEvent(
    {
      author: "alice",
      objectId: OBJECT,
      generation: 1,
      dot: parseDot("4@alice"),
      observed: {},
      kind: "revoke",
      payload: { target: g0.dot, targetGeneration: 0 },
    },
    registry.principals.get("alice").keys.privateKey,
  );
  assert.equal(new KeyAdmissionMerger(OBJECT, registry).ingest(wrongGen).reason, "target-generation-mismatch");

  // A delete aimed at a generation that has no rebuild yet leaves it pending,
  // and cannot silence the enabled sibling generation.
  const strayDelete = log.tick(OBJECT, "admin", "delete", {}, { generation: 7 });
  const merger = mergeAll(registry, OBJECT, [r0, g0, del0, r1, g1, strayDelete]);
  const states = new Map(merger.view().generations.map((g) => [g.generation, g.state]));
  assert.equal(states.get(0), "closed");
  assert.equal(states.get(1), "enabled");
  assert.equal(states.get(7), "pending");
  assert.equal(claimIndex(merger.view()).get("bob:k:g1").verdict, "admitted");
});

test("old backup joining late sees stale envelopes denied and cannot GC open-generation revokes", () => {
  const registry = buildRegistry();
  const log = new LocalLog(registry);
  const rebuild = log.tick(OBJECT, "owner", "rebuild", { keyContext: "g0" }, { generation: 0 });
  const root = log.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "k" }, { generation: 0 });
  const backupEnvelope = log.tick(OBJECT, "bob", "delegate", { parent: root.dot, subject: "bob", handle: "k" }, { generation: 0 });
  // Backup goes offline here; the live side revokes, deletes, rebuilds.
  const revoke = log.tick(OBJECT, "alice", "revoke", { target: root.dot }, { generation: 0 });
  const del = log.tick(OBJECT, "admin", "delete", {}, { generation: 0 });
  const rebuild1 = log.tick(OBJECT, "owner", "rebuild", { keyContext: "g1" }, { generation: 1 });
  const fresh = log.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "k2" }, { generation: 1 });

  const merger = mergeAll(registry, OBJECT, [rebuild, root, revoke, del, rebuild1, fresh, backupEnvelope]);
  const view = merger.view();
  assert.equal(claimIndex(view).get("bob:k:g0").verdict, "denied");
  assert.equal(claimIndex(view).get("alice:k2:g1").verdict, "admitted");

  // GC only safe because a retained delete closed g0.
  assert.equal(canDiscardRevoke(revoke.dot, merger.snapshot()), true);
  assert.deepEqual(merger.discardableRevocations(), [formatDot(revoke.dot)]);

  // Without the delete (open generation), the revoke must be retained even if
  // this replica never syncs again; admission stays denied.
  const open = mergeAll(registry, OBJECT, [rebuild, root, revoke, backupEnvelope]);
  assert.equal(canDiscardRevoke(revoke.dot, open.snapshot()), false);
  assert.deepEqual(open.discardableRevocations(), []);
  assert.equal(claimIndex(open.view()).get("bob:k:g0").verdict, "denied");
});

test("compressed summary plus incomplete suffix leaves gaps pending, never empty", () => {
  const registry = buildRegistry();
  const log = new LocalLog(registry);
  const rebuild = log.tick(OBJECT, "owner", "rebuild", { keyContext: "g0" }, { generation: 0 });
  const e1 = log.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "k" }, { generation: 0 });
  const e2 = log.tick(OBJECT, "alice", "grant", { subject: "bob", handle: "k2" }, { generation: 0 });
  const e3 = log.tick(OBJECT, "alice", "grant", { subject: "carol", handle: "k3" }, { generation: 0 });

  // Only the last event arrives, but a sealed summary proves the prefix exists.
  const summary = log.frontier(OBJECT, "alice", { alice: 3n, owner: 1n });
  const partial = new KeyAdmissionMerger(OBJECT, registry);
  partial.ingestSummary(summary);
  partial.ingest(rebuild);
  partial.ingest(e3);
  const gappy = partial.view();
  assert.equal(gappy.complete, false);
  assert.equal(claimIndex(gappy).get("carol:k3:g0").verdict, "pending");

  // Filling the gap upgrades to admitted without ever having denied it.
  partial.ingest(e1);
  partial.ingest(e2);
  assert.equal(partial.view().complete, true);
  assert.equal(claimIndex(partial.view()).get("carol:k3:g0").verdict, "admitted");

  // A principal without the grant right cannot forge an envelope.
  const forged = signEvent(
    { author: "dave", objectId: OBJECT, generation: 0, dot: parseDot("1@dave"), observed: {}, kind: "grant", payload: { subject: "x", handle: "y" } },
    registry.principals.get("dave").keys.privateKey,
  );
  assert.equal(new KeyAdmissionMerger(OBJECT, registry).ingest(forged).reason, "missing-right");

  // Tampered signature is rejected.
  const tampered = { ...e1, signature: new Uint8Array(e1.signature) };
  tampered.signature[0] ^= 0xff;
  assert.equal(new KeyAdmissionMerger(OBJECT, registry).ingest(tampered).reason, "bad-signature");
});

test("equivocation at one dot quarantines that author's authorizations only", () => {
  const registry = buildRegistry();
  const log = new LocalLog(registry);
  const rebuild = log.tick(OBJECT, "owner", "rebuild", { keyContext: "g0" }, { generation: 0 });
  const good = log.tick(OBJECT, "owner", "grant", { subject: "bob", handle: "safe" }, { generation: 0 });
  const a = log.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "fork" }, { generation: 0, sequence: 2n });
  const b = log.tick(OBJECT, "alice", "grant", { subject: "carol", handle: "other-fork" }, { generation: 0, sequence: 2n });

  const merger = mergeAll(registry, OBJECT, [rebuild, good, a, b]);
  const view = merger.view();
  assert.deepEqual(view.equivocatingAuthors, ["alice"]);
  const claims = claimIndex(view);
  assert.equal(claims.get("alice:fork:g0").verdict, "denied");
  assert.equal(claims.get("carol:other-fork:g0").verdict, "denied");
  assert.equal(claims.get("bob:safe:g0").verdict, "admitted");

  const offline = verifyKeyLog(OBJECT, shuffle([rebuild, good, a, b], 99), registry).view;
  assert.deepEqual(offline.equivocatingAuthors, ["alice"]);
  assert.deepEqual(
    offline.claims.map((c) => c.verdict).sort(),
    view.claims.map((c) => c.verdict).sort(),
  );
});

test("the five signature permissions are distinct, including owner-only rebuild", () => {
  const registry = buildRegistry();
  const log = new LocalLog(registry);
  const rebuild = log.tick(OBJECT, "owner", "rebuild", { keyContext: "g0" }, { generation: 0 });
  mergeAll(registry, OBJECT, [rebuild]);

  const attempt = (author, kind, payload, extra = {}) =>
    signEvent(
      { author, objectId: OBJECT, generation: 0, dot: parseDot(`1@${author}`), observed: {}, kind, payload, ...extra },
      registry.principals.get(author).keys.privateKey,
    );

  assert.equal(new KeyAdmissionMerger(OBJECT, registry).ingest(attempt("dave", "grant", { subject: "x", handle: "h" })).reason, "missing-right");
  assert.equal(new KeyAdmissionMerger(OBJECT, registry).ingest(attempt("bob", "revoke", { target: parseDot("1@alice") })).reason, "missing-right");
  assert.equal(new KeyAdmissionMerger(OBJECT, registry).ingest(attempt("alice", "delete", {})).reason, "missing-right");
  // admin holds delete but not the owner-only rebuild right.
  assert.equal(new KeyAdmissionMerger(OBJECT, registry).ingest(attempt("admin", "rebuild", { keyContext: "x" }, { generation: 1 })).reason, "not-owner");
  // A non-owner holding the rebuild permission is still refused: only the
  // owner's signed rebuild may enable a new generation.
  assert.equal(new KeyAdmissionMerger(OBJECT, registry).ingest(
    signEvent(
      { author: "restorer", objectId: OBJECT, generation: 1, dot: parseDot("1@restorer"), observed: {}, kind: "rebuild", payload: { keyContext: "x" } },
      registry.principals.get("restorer").keys.privateKey,
    ),
  ).reason, "not-owner");
});

test("online merger and offline verifier agree under every legal delivery order", () => {
  const registry = buildRegistry();
  const log = new LocalLog(registry);
  const events = [
    log.tick(OBJECT, "owner", "rebuild", { keyContext: "g0" }, { generation: 0 }),
    log.tick(OBJECT, "alice", "grant", { subject: "alice", handle: "k" }, { generation: 0 }),
    log.tick(OBJECT, "bob", "delegate", { parent: parseDot("1@alice"), subject: "bob", handle: "k" }, { generation: 0 }),
    log.tick(OBJECT, "alice", "grant", { subject: "carol", handle: "k2" }, { generation: 0 }),
    log.tick(OBJECT, "admin", "delete", {}, { generation: 0 }),
    log.tick(OBJECT, "owner", "rebuild", { keyContext: "g1" }, { generation: 1 }),
    log.tick(OBJECT, "alice", "grant", { subject: "dave", handle: "k3" }, { generation: 1 }),
  ];
  const fingerprint = (view) =>
    JSON.stringify({
      generations: view.generations.map((g) => [g.generation, g.state]),
      claims: view.claims.map((c) => [c.subject, c.handle, c.generation, c.verdict]),
      complete: view.complete,
    });

  const expected = fingerprint(verifyKeyLog(OBJECT, events, registry).view);
  for (let seed = 1; seed <= 12; seed += 1) {
    const merged = mergeAll(registry, OBJECT, shuffle(events, seed)).view();
    assert.equal(fingerprint(merged), expected, `seed ${seed}`);
  }

  // Partial evidence: summary + suffix online matches offline over the same
  // subset, both holding the g0 claim pending rather than denying it.
  const summary = log.frontier(OBJECT, "owner", { owner: 2n, alice: 4n, admin: 1n, bob: 1n });
  const merger = new KeyAdmissionMerger(OBJECT, registry);
  merger.ingestSummary(summary);
  for (const event of events.slice(4)) merger.ingest(event);
  const online = merger.view();
  const offline = verifyKeyLog(OBJECT, events.slice(4), registry).view;
  assert.ok(online.claims.every((claim) => claim.verdict !== "admitted" || claim.generation === 1));
  assert.deepEqual(
    offline.claims.filter((c) => c.generation === 1).map((c) => c.verdict),
    online.claims.filter((c) => c.generation === 1).map((c) => c.verdict),
  );
});

test("uint64 sequences survive beyond Number.MAX_SAFE_INTEGER with byte-faithful fixtures", () => {
  const registry = buildRegistry();
  const huge = 18446744073709551615n; // 2^64 - 1
  const overSafe = 9007199254740993n; // MAX_SAFE_INTEGER + 1

  // Historic fixture persisted as JSON text, never as a JS number.
  const persisted = stringifyAdmissionData({
    objectId: OBJECT,
    dot: { actor: "alice", sequence: huge },
    observed: { owner: overSafe },
  });
  const restored = parseAdmissionData(persisted);
  assert.equal(restored.dot.sequence, huge);
  assert.equal(restored.observed.owner, overSafe);
  assert.ok(persisted.includes("18446744073709551615"));

  // Canonical bytes are exactly 8 big-endian bytes for the max uint64.
  const event = signEvent(
    {
      author: "alice",
      objectId: OBJECT,
      generation: 0,
      dot: { actor: "alice", sequence: huge },
      observed: { owner: overSafe },
      kind: "grant",
      payload: { subject: "alice", handle: "h" },
    },
    registry.principals.get("alice").keys.privateKey,
  );
  const bytes = canonicalEventBytes(event);
  // Layout: kind(1) | author(len4+utf8) | objectId(len4+utf8) | gen(4)
  //         | dotActor(len4+utf8) | dotSeq(8). The final block is all 0xff.
  let offset = 1 + 4 + event.author.length + 4 + event.objectId.length + 4 + 4 + event.dot.actor.length;
  assert.deepEqual([...bytes.slice(offset, offset + 8)], [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff]);
  // The observed overSafe value keeps all 64 bits as well.
  assert.ok(bytes.length > offset + 8);
  assert.equal(formatDot(parseDot(formatDot(event.dot))), `${huge}@alice`);

  // A sealed frontier at the huge boundary makes every missing earlier dot a
  // pending gap instead of being treated as "never written".
  const log = new LocalLog(registry);
  const rebuild = log.tick(OBJECT, "owner", "rebuild", { keyContext: "g0" }, { generation: 0 });
  const merger = new KeyAdmissionMerger(OBJECT, registry);
  merger.ingest(rebuild);
  merger.ingestSummary(log.frontier(OBJECT, "owner", { alice: huge }));
  const view = merger.view();
  assert.equal(view.complete, false);
  assert.deepEqual([...merger.snapshot().frontier.entries()].map(([a, s]) => [a, s.toString()]), [
    ["owner", "1"],
    ["alice", huge.toString()],
  ]);

  // Offline and online agree on the same large-sequence event set.
  const offline = verifyKeyLog(OBJECT, [rebuild, event], registry).view;
  const online = mergeAll(registry, OBJECT, [rebuild, event]).view();
  assert.deepEqual(
    offline.claims.map((c) => [c.subject, c.handle, c.edge.sequence.toString(), c.verdict]),
    online.claims.map((c) => [c.subject, c.handle, c.edge.sequence.toString(), c.verdict]),
  );
  assert.equal(online.claims[0].edge.sequence, huge);
  assert.equal(eventId(event), eventId({ ...event, signature: new Uint8Array(64) }));
});
