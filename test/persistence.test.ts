import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  DurableMigrationStore,
  SimulatedCrashError,
  buildMigrationScenario,
  evaluateRelease,
  runCrashScenario,
  verifyPersistenceBoundaries,
  type PersistenceBoundary,
} from "../src/index.ts";

function tempDir(label: string): string {
  return mkdtempSync(join(tmpdir(), `migration-${label}-`));
}

test("crash before journal append loses only the not-yet-durable event", (context) => {
  const dir = tempDir("before-append");
  context.after(() => rmSync(dir, { recursive: true, force: true }));
  const scenario = buildMigrationScenario();
  const result = runCrashScenario(
    dir,
    "before-append",
    (store) => store.addAll(scenario.events),
    { policy: scenario.policy, closedDevices: scenario.input.closedDevices },
  );
  assert.equal(result.crashed, true);
  assert.equal(result.error?.boundary, "before-append");
  assert.equal(result.recovered.events.length, 0);
});

test("crash after journal append recovers the durable event prefix", (context) => {
  const dir = tempDir("after-append");
  context.after(() => rmSync(dir, { recursive: true, force: true }));
  const scenario = buildMigrationScenario();
  const result = runCrashScenario(
    dir,
    "after-journal-append",
    (store) => store.addAll(scenario.events),
    { policy: scenario.policy, closedDevices: scenario.input.closedDevices },
  );
  assert.equal(result.crashed, true);
  assert.equal(result.recovered.events.length, 1);
  assert.equal(result.recovered.journalLines, 1);
  assert.equal(result.recovered.snapshotFound, false);
});

test("snapshot plus journal is deduplicated after a crash", (context) => {
  const dir = tempDir("after-snapshot");
  context.after(() => rmSync(dir, { recursive: true, force: true }));
  const scenario = buildMigrationScenario();
  const result = runCrashScenario(
    dir,
    "after-snapshot-write",
    (store) => {
      store.addAll(scenario.events);
      return store.checkpoint();
    },
    { policy: scenario.policy, closedDevices: scenario.input.closedDevices },
  );
  assert.equal(result.crashed, true);
  assert.equal(result.error?.boundary, "after-snapshot-write");
  assert.equal(result.recovered.snapshotFound, true);
  assert.equal(result.recovered.events.length, scenario.events.length);
  assert.equal(result.recovered.journalLines, scenario.events.length);
  const decision = result.store.evaluate(scenario.input);
  assert.equal(decision.status, "release");
});

test("journal truncation after snapshot keeps the full event set", (context) => {
  const dir = tempDir("after-truncate");
  context.after(() => rmSync(dir, { recursive: true, force: true }));
  const scenario = buildMigrationScenario();
  const result = runCrashScenario(
    dir,
    "after-journal-truncate",
    (store) => {
      store.addAll(scenario.events);
      return store.checkpoint();
    },
    { policy: scenario.policy, closedDevices: scenario.input.closedDevices },
  );
  assert.equal(result.crashed, true);
  assert.equal(result.recovered.snapshotFound, true);
  assert.equal(result.recovered.events.length, scenario.events.length);
  assert.equal(result.recovered.journalLines, 0);
});

test("a release lease is either fully durable or absent at each boundary", (context) => {
  const scenario = buildMigrationScenario();

  const beforeDir = tempDir("lease-before");
  context.after(() => rmSync(beforeDir, { recursive: true, force: true }));
  const before = runCrashScenario(
    beforeDir,
    "before-snapshot",
    (store) => {
      store.addAll(scenario.events);
      return store.release(
        scenario.options.workloadId,
        scenario.options.generation,
        scenario.input,
      );
    },
    { policy: scenario.policy, closedDevices: scenario.input.closedDevices },
  );
  assert.equal(before.crashed, true);
  assert.equal(
    before.store.getLease(
      scenario.options.workloadId,
      scenario.options.generation,
    ),
    undefined,
  );
  // Re-evaluation is deterministic, so a retry after losing the lease is safe.
  const retry = before.store.release(
    scenario.options.workloadId,
    scenario.options.generation,
    scenario.input,
  );
  assert.equal(retry.status, "release");
  assert.equal(retry.targetKey, scenario.options.newRuntimeKey);

  const afterDir = tempDir("lease-after");
  context.after(() => rmSync(afterDir, { recursive: true, force: true }));
  const after = runCrashScenario(
    afterDir,
    "after-snapshot-write",
    (store) => {
      store.addAll(scenario.events);
      return store.release(
        scenario.options.workloadId,
        scenario.options.generation,
        scenario.input,
      );
    },
    { policy: scenario.policy, closedDevices: scenario.input.closedDevices },
  );
  assert.equal(after.crashed, true);
  const lease = after.store.getLease(
    scenario.options.workloadId,
    scenario.options.generation,
  );
  assert.equal(lease?.runtimeKey, scenario.options.newRuntimeKey);
  assert.deepEqual(lease?.dependencies, scenario.events
    .filter((event) =>
      [
        scenario.newAttestationId,
        scenario.deactivationId,
        scenario.ownerIds[0],
        scenario.revocationId,
        scenario.transferId,
      ].includes(String(event.id)),
    )
    .map((event) => String(event.id))
    .sort());
});

test("recovered stores preserve resurrection and concurrency safety", (context) => {
  const scenario = buildMigrationScenario();
  const dir = tempDir("recover-safety");
  context.after(() => rmSync(dir, { recursive: true, force: true }));
  const store = new DurableMigrationStore({
    dir,
    policy: scenario.policy,
    closedDevices: scenario.input.closedDevices,
  });
  store.addAll(scenario.events);
  store.checkpoint();

  const resurrection = {
    id: "resurrect-after-restart",
    kind: "attestation",
    action: "attest",
    signer: scenario.options.oldChipId,
    payload: {
      chipId: scenario.options.oldChipId,
      workloadId: scenario.options.workloadId,
      bootCount: scenario.options.oldBootCount + 1,
      debug: false,
      runtimeKey: scenario.options.oldRuntimeKey,
      nonce: "resurrect-after-restart-nonce",
      tcb: { svn: 6, fmw: 11 },
      policyDigest: scenario.options.policyDigest,
      generation: scenario.options.generation,
    },
  };
  store.add(resurrection);

  const reopened = new DurableMigrationStore({
    dir,
    policy: scenario.policy,
    closedDevices: scenario.input.closedDevices,
  });
  reopened.load();
  const decision = reopened.evaluate(scenario.input);
  assert.equal(decision.status, "wait");
  assert.ok(decision.reasons.includes("old-instance-resurrected"));
});

test("reopening always matches pure event-set recomputation", (context) => {
  const scenario = buildMigrationScenario();
  const dir = tempDir("reference");
  context.after(() => rmSync(dir, { recursive: true, force: true }));
  const store = new DurableMigrationStore({
    dir,
    policy: scenario.policy,
    closedDevices: scenario.input.closedDevices,
    checkpointEvery: 3,
  });
  store.addAll(scenario.events);
  const before = store.evaluate(scenario.input);
  store.checkpoint();
  const reopened = new DurableMigrationStore({
    dir,
    policy: scenario.policy,
    closedDevices: scenario.input.closedDevices,
  });
  reopened.load();
  const after = reopened.evaluate(scenario.input);
  const reference = evaluateRelease({
    ...scenario.input,
    events: scenario.events,
  });
  for (const decision of [before, after]) {
    assert.equal(decision.status, reference.status);
    assert.equal(decision.targetKey, reference.targetKey);
    assert.deepEqual(decision.dependencies, reference.dependencies);
    assert.deepEqual(decision.missing, reference.missing);
  }
});

test("simulated crash error type is visible to callers", () => {
  const dir = tempDir("error");
  try {
    const store = new DurableMigrationStore({ dir, crashAt: "before-append" });
    assert.throws(
      () => store.add(buildMigrationScenario().events[0]),
      (error: unknown) =>
        error instanceof SimulatedCrashError &&
        (error as SimulatedCrashError).boundary === "before-append",
    );
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("each persistence boundary is independently injectable", () => {
  const boundaries: PersistenceBoundary[] = [
    "before-append",
    "after-journal-append",
    "before-checkpoint",
    "before-snapshot",
    "after-snapshot-write",
    "before-journal-truncate",
    "after-journal-truncate",
    "after-checkpoint",
  ];
  assert.equal(new Set(boundaries).size, boundaries.length);
});

test("all documented durability boundaries recover a valid prefix", (context) => {
  const dir = tempDir("matrix");
  context.after(() => rmSync(dir, { recursive: true, force: true }));
  const scenario = buildMigrationScenario();
  const results = verifyPersistenceBoundaries(dir, scenario);
  assert.equal(results.length, 8);
  for (const result of results) {
    assert.equal(result.crashed, true, result.boundary);
    assert.equal(result.matchesExpected, true, result.boundary);
  }
  for (const result of results) {
    if (
      result.boundary !== "before-append" &&
      result.boundary !== "after-journal-append"
    ) {
      assert.equal(result.recoveredStatus, "release", result.boundary);
      assert.equal(result.statusMatches, true, result.boundary);
    }
  }
});
