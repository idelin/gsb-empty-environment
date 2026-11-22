import assert from "node:assert/strict";
import { generateKeyPairSync } from "node:crypto";
import test from "node:test";
import {
  MigrationAcceptanceEngine,
  buildMigrationScenario,
  canRelease,
  compareTcbVectors,
  createEd25519Verifier,
  evaluateRelease,
  evaluateScenario,
  minimalSignedDependencies,
  signRecord,
  verifyScenarioAgainstReference,
  type MigrationEvent,
} from "../src/index.ts";

function decisionSignature(decision: ReturnType<typeof evaluateRelease>) {
  return {
    status: decision.status,
    targetKey: decision.targetKey,
    targetChipId: decision.targetChipId,
    dependencies: decision.dependencies,
    missing: decision.missing,
    releases: decision.released.map((item) => item.runtimeKey).sort(),
  };
}

function shuffled<T>(values: T[], seed: number): T[] {
  const copy = [...values];
  let state = seed >>> 0;
  for (let index = copy.length - 1; index > 0; index -= 1) {
    state = (1664525 * state + 1013904223) >>> 0;
    const swap = state % (index + 1);
    [copy[index], copy[swap]] = [copy[swap], copy[index]];
  }
  return copy;
}

test("complete evidence releases exactly the new runtime key and minimal records", () => {
  const scenario = buildMigrationScenario();
  const decision = evaluateScenario(scenario);

  assert.equal(decision.status, "release");
  assert.equal(decision.targetKey, scenario.options.newRuntimeKey);
  assert.equal(decision.release?.chipId, scenario.options.newChipId);
  assert.deepEqual(decision.dependencies, [
    scenario.newAttestationId,
    scenario.deactivationId,
    scenario.ownerIds[0],
    scenario.revocationId,
    scenario.transferId,
  ]);
  assert.deepEqual(
    minimalSignedDependencies(decision),
    decision.dependencies,
  );
  assert.deepEqual(decision.missing, []);
  assert.equal(canRelease(decision), true);
});

test("evidence arrival order does not change the decision", () => {
  const scenario = buildMigrationScenario();
  const baseline = decisionSignature(evaluateScenario(scenario));
  for (let seed = 1; seed <= 40; seed += 1) {
    const decision = evaluateRelease({
      ...scenario.input,
      events: shuffled(scenario.events, seed),
    });
    assert.deepEqual(decisionSignature(decision), baseline);
  }
});

test("a single valid new attestation never implies the old instance stopped", () => {
  const scenario = buildMigrationScenario({
    include: { deactivation: false, revocation: false },
  });
  const decision = evaluateScenario(scenario);
  assert.equal(decision.status, "wait");
  assert.ok(decision.missing.includes("deactivate-instance"));
  assert.ok(decision.missing.includes("revoke-key"));
  assert.equal(decision.release, null);
});

test("old instance resurrection after deactivation blocks release", () => {
  const scenario = buildMigrationScenario();
  const resurrection: MigrationEvent = {
    id: "old-resurrection",
    kind: "attestation",
    action: "attest",
    signer: scenario.options.oldChipId,
    payload: {
      chipId: scenario.options.oldChipId,
      workloadId: scenario.options.workloadId,
      bootCount: scenario.options.oldBootCount + 1,
      debug: false,
      runtimeKey: scenario.options.oldRuntimeKey,
      nonce: "old-resurrection-nonce",
      tcb: { svn: 6, fmw: 11 },
      policyDigest: scenario.options.policyDigest,
      generation: scenario.options.generation,
    },
  };
  const decision = evaluateScenario(scenario, {
    events: [...scenario.events, resurrection],
  });
  assert.equal(decision.status, "wait");
  assert.ok(decision.reasons.includes("old-instance-resurrected"));
  assert.equal(decision.release, null);
});

test("instance-scoped closed entries only cover the listed boot count", () => {
  const scenario = buildMigrationScenario({
    include: { deactivation: false },
    policy: { allowClosedDeviceAsDeactivation: true },
  });
  const stale = evaluateScenario(scenario, {
    closedDevices: [
      `${scenario.options.oldChipId}@${scenario.options.oldBootCount - 1}`,
    ],
  });
  assert.equal(stale.status, "wait");
  assert.ok(stale.reasons.includes("old-instance-not-stopped"));

  const exact = evaluateScenario(scenario, {
    closedDevices: [
      `${scenario.options.oldChipId}@${scenario.options.oldBootCount}`,
    ],
  });
  assert.equal(exact.status, "release");
});

test("ephemeral key rotation needs its own stop and revocation evidence", () => {
  const scenario = buildMigrationScenario();
  const rotated = "pk-old-rotated";
  const rotation: MigrationEvent = {
    id: "old-key-rotation",
    kind: "attestation",
    action: "attest",
    signer: scenario.options.oldChipId,
    payload: {
      chipId: scenario.options.oldChipId,
      workloadId: scenario.options.workloadId,
      bootCount: scenario.options.oldBootCount,
      debug: false,
      runtimeKey: rotated,
      nonce: "old-key-rotation-nonce",
      tcb: { svn: 6, fmw: 11 },
      policyDigest: scenario.options.policyDigest,
      generation: scenario.options.generation,
    },
  };
  const blocked = evaluateScenario(scenario, {
    events: [...scenario.events, rotation],
  });
  assert.equal(blocked.status, "wait");
  assert.ok(blocked.reasons.includes("old-instance-key-rotated"));
  assert.ok(blocked.missing.includes("revoke-key"));

  const rotationDeactivation: MigrationEvent = {
    id: "old-key-rotation-deactivate",
    kind: "instance-deactivation",
    action: "deactivate-instance",
    signer: scenario.options.oldChipId,
    payload: {
      migrationId: scenario.options.migrationId,
      chipId: scenario.options.oldChipId,
      workloadId: scenario.options.workloadId,
      bootCount: scenario.options.oldBootCount,
      runtimeKey: rotated,
      generation: scenario.options.generation,
    },
  };
  const rotationRevocation: MigrationEvent = {
    id: "old-key-rotation-revoke",
    kind: "key-revocation",
    action: "revoke-key",
    signer: "owner-control",
    payload: {
      migrationId: scenario.options.migrationId,
      workloadId: scenario.options.workloadId,
      runtimeKey: rotated,
      generation: scenario.options.generation,
    },
  };
  const released = evaluateScenario(scenario, {
    events: [
      ...scenario.events,
      rotation,
      rotationDeactivation,
      rotationRevocation,
    ],
  });
  assert.equal(released.status, "release");
  assert.ok(released.dependencies.includes(rotationDeactivation.id as string));
  assert.ok(released.dependencies.includes(rotationRevocation.id as string));
});

test("new-side runtime key rotation blocks release to a stale key", () => {
  const scenario = buildMigrationScenario();
  const rotatedNew: MigrationEvent = {
    id: "new-key-rotation",
    kind: "attestation",
    action: "attest",
    signer: scenario.options.newChipId,
    payload: {
      chipId: scenario.options.newChipId,
      workloadId: scenario.options.workloadId,
      bootCount: scenario.options.newBootCount,
      debug: false,
      runtimeKey: "pk-new-rotated",
      nonce: "new-key-rotation-nonce",
      tcb: { svn: 6, fmw: 11 },
      policyDigest: scenario.options.policyDigest,
      generation: scenario.options.generation,
    },
  };
  const decision = evaluateScenario(scenario, {
    events: [...scenario.events, rotatedNew],
  });
  assert.equal(decision.status, "wait");
  assert.ok(decision.reasons.includes("target-key-rotated"));
  assert.equal(decision.release, null);
});

test("new-side key rotation with a newer acceptance releases the rotated key", () => {
  const scenario = buildMigrationScenario();
  const rotatedKey = "pk-new-rotated";
  const rotatedAttestation: MigrationEvent = {
    id: "mig-1:attest:new2",
    kind: "attestation",
    action: "attest",
    signer: scenario.options.newChipId,
    payload: {
      chipId: scenario.options.newChipId,
      workloadId: scenario.options.workloadId,
      bootCount: scenario.options.newBootCount,
      debug: false,
      runtimeKey: rotatedKey,
      nonce: "new-key-rotation-2-nonce",
      tcb: { svn: 6, fmw: 11 },
      policyDigest: scenario.options.policyDigest,
      generation: scenario.options.generation,
    },
  };
  const rotatedTransfer: MigrationEvent = {
    id: "mig-1:transfer:rotated",
    kind: "transfer-acceptance",
    action: "accept-transfer",
    signer: scenario.options.newChipId,
    payload: {
      migrationId: scenario.options.migrationId,
      workloadId: scenario.options.workloadId,
      generation: scenario.options.generation,
      targetChipId: scenario.options.newChipId,
      targetRuntimeKey: rotatedKey,
      oldChipId: scenario.options.oldChipId,
      oldRuntimeKey: scenario.options.oldRuntimeKey,
      attestationId: rotatedAttestation.id,
    },
  };
  const rotatedOwner: MigrationEvent = {
    id: "mig-1:owner:rotated",
    kind: "owner-confirmation",
    action: "confirm-migration",
    signer: scenario.options.ownerDomains[0],
    payload: {
      migrationId: scenario.options.migrationId,
      workloadId: scenario.options.workloadId,
      generation: scenario.options.generation,
      adminDomainId: scenario.options.ownerDomains[0],
      targetChipId: scenario.options.newChipId,
      targetRuntimeKey: rotatedKey,
    },
  };
  const decision = evaluateScenario(scenario, {
    events: [
      ...scenario.events,
      rotatedAttestation,
      rotatedTransfer,
      rotatedOwner,
    ],
  });
  assert.equal(decision.status, "release");
  assert.equal(decision.targetKey, rotatedKey);
  assert.ok(decision.dependencies.includes(String(rotatedTransfer.id)));
  assert.equal(decision.dependencies.includes(scenario.transferId), false);
});

test("two concurrent migrations produce at most one release", () => {
  const a = buildMigrationScenario({
    migrationId: "mig-a",
    newChipId: "chip-new-a",
    newRuntimeKey: "pk-new-a",
  });
  const b = buildMigrationScenario({
    migrationId: "mig-b",
    newChipId: "chip-new-b",
    newRuntimeKey: "pk-new-b",
  });
  for (const events of [
    [...a.events, ...b.events],
    shuffled([...a.events, ...b.events], 77),
  ]) {
    const decision = evaluateRelease({ ...a.input, events });
    assert.equal(decision.status, "release");
    assert.equal(decision.released.length, 1);
    const releasedKeys = decision.released.map((item) => item.runtimeKey);
    assert.deepEqual(releasedKeys, ["pk-new-a"]);
    assert.equal(decision.conflicts.length, 1);
    const bDecision = decision.migrations.find(
      (item) => item.migrationId === "mig-b",
    );
    assert.equal(bDecision?.status, "reject");
    assert.equal(bDecision?.conflictingMigrationId, "mig-a");
  }
});

test("a later migration can take over a revoked prior lease", () => {
  const first = buildMigrationScenario({
    migrationId: "mig-1",
    newChipId: "chip-a",
    newRuntimeKey: "pk-a",
  });
  const engine = new MigrationAcceptanceEngine({
    policy: first.policy,
    closedDevices: first.input.closedDevices,
    events: first.events,
  });
  const firstRelease = engine.release(
    first.options.workloadId,
    first.options.generation,
  );
  assert.equal(firstRelease.status, "release");
  assert.equal(engine.getLease(first.options.workloadId, first.options.generation)?.runtimeKey, "pk-a");

  const second = buildMigrationScenario({
    migrationId: "mig-2",
    oldChipId: "chip-a",
    oldRuntimeKey: "pk-a",
    oldBootCount: first.options.newBootCount,
    newChipId: "chip-b",
    newRuntimeKey: "pk-b",
  });
  engine.addAll(second.events);
  const secondRelease = engine.release(
    second.options.workloadId,
    second.options.generation,
  );
  assert.equal(secondRelease.status, "release");
  assert.equal(secondRelease.targetKey, "pk-b");
  assert.equal(engine.getLease(first.options.workloadId, first.options.generation)?.runtimeKey, "pk-b");
});

test("recovery confirmations dedupe physical chips and admin domains", () => {
  const baseOptions = {
    include: { owner: false, recovery: true },
    policy: {
      ownerThreshold: 1,
      allowRecovery: true,
      recovery: {
        enabled: true,
        deviceThreshold: 2,
        adminDomainThreshold: 2,
      },
    },
  };

  const sameChip = buildMigrationScenario({
    ...baseOptions,
    recoveryDevices: [
      { chipId: "device-1", adminDomainId: "domain-a" },
      { chipId: "device-1", adminDomainId: "domain-b" },
    ],
  });
  const sameChipDecision = evaluateScenario(sameChip);
  assert.equal(sameChipDecision.status, "wait");
  assert.ok(sameChipDecision.missing.includes("confirm-recovery"));

  const sameDomain = buildMigrationScenario({
    ...baseOptions,
    recoveryDevices: [
      { chipId: "device-1", adminDomainId: "domain-a" },
      { chipId: "device-2", adminDomainId: "domain-a" },
    ],
  });
  const sameDomainDecision = evaluateScenario(sameDomain);
  assert.equal(sameDomainDecision.status, "wait");
  assert.ok(sameDomainDecision.missing.includes("confirm-recovery"));

  const distinct = buildMigrationScenario({
    ...baseOptions,
    recoveryDevices: [
      { chipId: "device-1", adminDomainId: "domain-a" },
      { chipId: "device-1", adminDomainId: "domain-b" },
      { chipId: "device-2", adminDomainId: "domain-a" },
      { chipId: "device-2", adminDomainId: "domain-b" },
    ],
  });
  const distinctDecision = evaluateScenario(distinct);
  assert.equal(distinctDecision.status, "release");
  const selectedRecovery = distinctDecision.dependencies.filter((id) =>
    id.includes(":recovery:"),
  );
  assert.equal(selectedRecovery.length, 2);
  const byId = new Map(
    distinct.events.map((event) => [String(event.id), event] as const),
  );
  const selectedPairs = selectedRecovery.map((id) => {
    const payload = byId.get(id)?.payload as
      | { chipId?: string; adminDomainId?: string }
      | undefined;
    return `${payload?.chipId}/${payload?.adminDomainId}`;
  });
  assert.equal(new Set(selectedPairs).size, 2);
  assert.equal(
    new Set(selectedPairs.map((pair) => pair.split("/")[0])).size,
    2,
  );
  assert.equal(
    new Set(selectedPairs.map((pair) => pair.split("/")[1])).size,
    2,
  );

  const crossPaired = buildMigrationScenario({
    ...baseOptions,
    recoveryDevices: [
      { chipId: "device-1", adminDomainId: "domain-a" },
      { chipId: "device-1", adminDomainId: "domain-b" },
      { chipId: "device-2", adminDomainId: "domain-a" },
    ],
  });
  const crossPairedDecision = evaluateScenario(crossPaired);
  assert.equal(crossPairedDecision.status, "release");
  assert.equal(
    crossPairedDecision.dependencies.filter((id) => id.includes(":recovery:"))
      .length,
    2,
  );
});

test("recovery is the active path when owner threshold is zero", () => {
  const policy = {
    ownerThreshold: 0,
    allowRecovery: true,
    recovery: {
      enabled: true,
      deviceThreshold: 2,
      adminDomainThreshold: 2,
    },
  };
  const enough = buildMigrationScenario({
    include: { owner: false, recovery: true },
    recoveryDevices: [
      { chipId: "device-1", adminDomainId: "domain-a" },
      { chipId: "device-2", adminDomainId: "domain-b" },
    ],
    policy,
  });
  assert.equal(evaluateScenario(enough).status, "release");

  const notEnough = buildMigrationScenario({
    include: { owner: false, recovery: true },
    recoveryDevices: [
      { chipId: "device-1", adminDomainId: "domain-a" },
      { chipId: "device-1", adminDomainId: "domain-b" },
    ],
    policy,
  });
  const blocked = evaluateScenario(notEnough);
  assert.equal(blocked.status, "wait");
  assert.ok(blocked.missing.includes("confirm-recovery"));
});

test("merging partial evidence never borrows the other migration's step", () => {
  const incompleteA = buildMigrationScenario({
    migrationId: "mig-a",
    newChipId: "chip-new-a",
    newRuntimeKey: "pk-new-a",
    include: { transfer: false },
  });
  const completeB = buildMigrationScenario({
    migrationId: "mig-b",
    newChipId: "chip-new-b",
    newRuntimeKey: "pk-new-b",
  });
  const decision = evaluateRelease({
    ...incompleteA.input,
    events: [...incompleteA.events, ...completeB.events],
  });
  const aDecision = decision.migrations.find(
    (item) => item.migrationId === "mig-a",
  );
  const bDecision = decision.migrations.find(
    (item) => item.migrationId === "mig-b",
  );
  assert.equal(aDecision?.status, "wait");
  assert.deepEqual(aDecision?.missing, ["accept-transfer"]);
  assert.equal(aDecision?.dependencies.includes(completeB.transferId), false);
  assert.equal(bDecision?.status, "release");
  assert.equal(decision.released.length, 1);
  assert.equal(decision.released[0].migrationId, "mig-b");
});

test("TCB vectors are partially ordered and never cross-substituted", () => {
  assert.equal(
    compareTcbVectors({ svn: 7, fmw: 11 }, { svn: 6, fmw: 10 }),
    "newer",
  );
  assert.equal(
    compareTcbVectors({ svn: 7, fmw: 9 }, { svn: 6, fmw: 10 }),
    "older",
  );
  assert.equal(
    compareTcbVectors({ svn: 7 }, { svn: 6, fmw: 10 }),
    "incomparable",
  );
  assert.equal(
    compareTcbVectors({ svn: 7, fmw: 11, vendor: 1 }, { svn: 6, fmw: 10 }),
    "incomparable",
  );
  assert.equal(
    compareTcbVectors(
      { epoch: "vendor-a", svn: 7 },
      { epoch: "vendor-b", svn: 6 },
    ),
    "incomparable",
  );
  assert.equal(
    compareTcbVectors(
      { svn: 7, fmw: 11 },
      { svn: 6, fmw: 10 },
      { rules: { fmw: "exact" } },
    ),
    "incomparable",
  );

  const scenario = buildMigrationScenario({
    oldTcb: { svn: 5, fmw: 10 },
    newTcb: { svn: 9 },
  });
  const decision = evaluateScenario(scenario);
  assert.equal(decision.status, "reject");
  assert.ok(
    decision.reasons.some((reason) => reason === "tcb-incomparable"),
  );
});

test("debug, policy digest and nonce replay are rejected", () => {
  const scenario = buildMigrationScenario();
  const debugEvents = scenario.events.map((event) =>
    event.id === scenario.newAttestationId &&
    event.payload &&
    typeof event.payload === "object"
      ? { ...event, payload: { ...event.payload, debug: true } }
      : event,
  );
  const debugDecision = evaluateScenario(scenario, { events: debugEvents });
  assert.equal(debugDecision.status, "reject");
  assert.ok(debugDecision.reasons.includes("debug-enabled"));

  const policyEvents = scenario.events.map((event) =>
    event.id === scenario.newAttestationId &&
    event.payload &&
    typeof event.payload === "object"
      ? { ...event, payload: { ...event.payload, policyDigest: "policy-other" } }
      : event,
  );
  const policyDecision = evaluateScenario(scenario, { events: policyEvents });
  assert.equal(policyDecision.status, "reject");
  assert.ok(policyDecision.reasons.includes("policy-digest-mismatch"));

  const replay: MigrationEvent = {
    ...scenario.events.find((event) => event.id === scenario.oldAttestationId)!,
    id: "replayed-old-attestation",
  };
  const replayDecision = evaluateScenario(scenario, {
    events: [...scenario.events, replay],
  });
  assert.notEqual(replayDecision.status, "release");
  assert.ok(replayDecision.migrations.some((item) => item.reasons.includes("nonce-replay")));
});

test("signatures bind the declared action and payload", () => {
  const scenario = buildMigrationScenario();
  const { privateKey, publicKey } = generateKeyPairSync("ed25519");
  const publicPem = publicKey.export({ type: "spki", format: "pem" });
  const signers = new Set(
    scenario.events.map((event) => String(event.signer)),
  );
  const verifier = createEd25519Verifier(
    Object.fromEntries([...signers].map((signer) => [signer, publicPem])),
  );
  const signed = scenario.events.map((event) =>
    signRecord(event, privateKey),
  );
  const valid = evaluateScenario(scenario, {
    events: signed,
    verifier,
  });
  assert.equal(valid.status, "release");

  const actionSwap = signed.map((event) =>
    event.id === scenario.transferId
      ? { ...event, action: "confirm-recovery" }
      : event,
  );
  const swapped = evaluateScenario(scenario, {
    events: actionSwap,
    verifier,
  });
  assert.equal(swapped.status, "reject");
  assert.equal(swapped.release, null);
  assert.ok(
    allReasons(swapped).some((reason) => reason.includes("action")),
  );

  const payloadSwap = signed.map((event) =>
    event.id === scenario.deactivationId &&
    event.payload &&
    typeof event.payload === "object"
      ? { ...event, payload: { ...event.payload, bootCount: 0 } }
      : event,
  );
  const payloadDecision = evaluateScenario(scenario, {
    events: payloadSwap,
    verifier,
  });
  assert.equal(payloadDecision.status, "reject");
  assert.equal(payloadDecision.release, null);
});

test("causal prefix closure is enforced", () => {
  const scenario = buildMigrationScenario();
  const events = scenario.events.map((event) =>
    event.id === scenario.transferId
      ? { ...event, causalParents: ["not-in-prefix"] }
      : event,
  );
  const decision = evaluateScenario(scenario, { events });
  assert.equal(decision.status, "wait");
  assert.equal(decision.causalPrefixComplete, false);
  assert.deepEqual(decision.missingCausalParents, ["not-in-prefix"]);
});

test("event-set recomputation matches the stateful engine", () => {
  const scenario = buildMigrationScenario();
  const comparison = verifyScenarioAgainstReference(scenario);
  assert.equal(comparison.equal, true, comparison.differences.join("; "));
  assert.deepEqual(
    decisionSignature(comparison.actual),
    decisionSignature(comparison.reference),
  );
});

test("randomized partial sets never double-release and match reference", () => {
  const a = buildMigrationScenario({
    migrationId: "mig-a",
    newChipId: "chip-new-a",
    newRuntimeKey: "pk-new-a",
  });
  const b = buildMigrationScenario({
    migrationId: "mig-b",
    newChipId: "chip-new-b",
    newRuntimeKey: "pk-new-b",
  });
  const allEvents = [...a.events, ...b.events];
  let state = 0x12345678;
  const next = () => {
    state = (1103515245 * state + 12345) >>> 0;
    return state;
  };
  for (let round = 0; round < 200; round += 1) {
    const mask = next();
    const subset = allEvents.filter((_, index) => ((mask >>> (index % 30)) & 1) === 1);
    const events = shuffled(subset, next());
    const engine = new MigrationAcceptanceEngine({
      policy: a.policy,
      events,
      closedDevices: a.input.closedDevices,
    });
    const actual = engine.evaluate({
      ...a.input,
      events,
    });
    const reference = evaluateRelease({ ...a.input, events });
    assert.deepEqual(decisionSignature(actual), decisionSignature(reference));
    assert.ok(actual.released.length <= 1);
    if (actual.released.length === 1) {
      assert.equal(actual.status, "release");
    }
  }
});

function allReasons(decision: ReturnType<typeof evaluateRelease>): string[] {
  return [
    ...decision.invalidEvents.flatMap((item) => item.reasons),
    ...decision.blockers,
    ...decision.reasons,
  ];
}
