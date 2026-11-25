import assert from "node:assert/strict";
import { test } from "node:test";
import {
  AuthorizationService,
  FaultInjectingCasStore,
  InMemoryBusinessGateway,
  InMemoryCasStore,
  InMemoryPolicyStore,
  SimulatedCrash,
  SubmitCode,
  SubmitStatus,
  claimKey,
  decisionKey,
  snapshotKey,
  subKey,
} from "../src/index.ts";
import { buildGrant, buildSubmit, testAliases, testKey } from "../src/testing/index.ts";

function makeService(store = new InMemoryCasStore(), policy = new InMemoryPolicyStore(), gateway = new InMemoryBusinessGateway()) {
  return {
    service: new AuthorizationService({
      store,
      policy,
      gateway,
      signingKeys: [testKey],
      aliases: testAliases,
    }),
    store,
    policy,
    gateway,
  };
}

test("rejects mismatched subject, window, version and form digest", async () => {
  const { service } = makeService();
  const grant = buildGrant();

  assert.equal((await service.submit(buildSubmit(grant, { subject: "intruder" }))).status, SubmitStatus.Failed);
  assert.equal((await service.submit(buildSubmit(grant, { windowSessionId: "window-B" }))).code, SubmitCode.WindowMismatch);
  assert.equal((await service.submit(buildSubmit(grant, { objectVersion: "obj-v8" }))).code, SubmitCode.VersionMismatch);
  const wrongForm = await service.submit(buildSubmit(grant, { form: { beneficiary: "Evil Corp", amt: 1, memo: "invoice-7" } }));
  assert.equal(wrongForm.code, SubmitCode.FormDigestMismatch);
});

test("alias-form submission matches the digest and writes independent proofs", async () => {
  const { service, store } = makeService();
  const grant = buildGrant();
  const result = await service.submit(buildSubmit(grant));
  assert.equal(result.status, SubmitStatus.Success);

  const snapshot = await service.readProof(snapshotKey(grant.grantId, "confirm", "idem-1"));
  assert.equal(snapshot.formDigest, grant.formDigest);
  for (const key of result.proofKeys) {
    const entry = await store.read(key);
    assert.notEqual(entry.version, 0, `proof ${key} must be independently readable`);
  }
  assert.equal(result.proofKeys.length, 4);
});

test("shuffled sub-operation requests are executed in dependency order", async () => {
  const { service } = makeService();
  const grant = buildGrant();
  const request = buildSubmit(grant);
  // The fixture deliberately reverses the dependency order.
  assert.deepEqual(request.subOperations.map((sub) => sub.subId), ["notify", "ledger", "reserve"]);
  const result = await service.submit(request);
  assert.equal(result.status, SubmitStatus.Success);
  assert.deepEqual(result.decision!.requiredSubIds, ["reserve", "ledger", "notify"]);
  assert.deepEqual(result.decision!.executedSubIds, ["reserve", "ledger", "notify"]);
});

test("replaying an acknowledged submission is idempotent and returns the same decision", async () => {
  const { service, gateway } = makeService();
  const grant = buildGrant();
  const first = await service.submit(buildSubmit(grant));
  const second = await service.submit(buildSubmit(grant));
  assert.equal(second.status, SubmitStatus.Success);
  assert.equal(second.decision!.decidedAt, first.decision!.decidedAt);
  assert.equal(gateway.effectCount(), 3);
});

test("a crash before the claim means nothing executed and the attempt is retryable", async () => {
  const inner = new InMemoryCasStore();
  const store = new FaultInjectingCasStore(inner, [
    { keyIncludes: "ledger/claim", mode: "before", occurrence: 1 },
  ]);
  const gateway = new InMemoryBusinessGateway();
  const { service } = makeService(store, new InMemoryPolicyStore(), gateway);
  const grant = buildGrant();

  const crashed = await service.submit(buildSubmit(grant));
  assert.equal(crashed.status, SubmitStatus.Unknown);
  assert.equal(gateway.effectCount(), 0);

  const reconciled = await service.reconcile(grant.grantId, "confirm", "idem-1");
  assert.equal(reconciled.status, SubmitStatus.Retryable);

  const retried = await service.submit(buildSubmit(grant));
  assert.equal(retried.status, SubmitStatus.Success);
  assert.equal(gateway.effectCount(), 3);
});

test("ambiguous sub-write is reported UNKNOWN and reconciliation finds the truth", async () => {
  const inner = new InMemoryCasStore();
  const store = new FaultInjectingCasStore(inner, [
    { keyIncludes: "proof/sub/grant-1/confirm/idem-1/ledger", mode: "ambiguous", occurrence: 1 },
  ]);
  const gateway = new InMemoryBusinessGateway();
  const { service } = makeService(store, new InMemoryPolicyStore(), gateway);
  const grant = buildGrant();

  const crashed = await service.submit(buildSubmit(grant));
  assert.equal(crashed.status, SubmitStatus.Unknown);

  // Reconciliation must inspect per-sub proof + gateway effect, never the claim.
  const reconciled = await service.reconcile(grant.grantId, "confirm", "idem-1");
  assert.ok(
    reconciled.status === SubmitStatus.Partial || reconciled.status === SubmitStatus.Retryable,
  );

  const resumed = await service.submit(buildSubmit(grant));
  assert.equal(resumed.status, SubmitStatus.Success);
  const report = await service.reportConsumption(grant.grantId, "confirm");
  assert.deepEqual(report.subConsumptions, { reserve: 1, ledger: 1, notify: 1 });
  assert.equal(gateway.effectCount(), 3);
});

test("ambiguous gateway effect never double-applies after resume", async () => {
  const store = new InMemoryCasStore();
  const gateway = new InMemoryBusinessGateway([
    { effectIdIncludes: ":reserve:", mode: "ambiguous", occurrence: 1 },
  ]);
  const { service } = makeService(store, new InMemoryPolicyStore(), gateway);
  const grant = buildGrant();
  const first = await service.submit(buildSubmit(grant));
  assert.equal(first.status, SubmitStatus.Unknown);

  const resumed = await service.submit(buildSubmit(grant));
  assert.equal(resumed.status, SubmitStatus.Success);
  // The ambiguous effect is keyed by effectId, so it can only exist once.
  assert.equal(gateway.effectCount(), 3);
});

test("occupied claim alone never proves a sub-operation completed", async () => {
  const inner = new InMemoryCasStore();
  const store = new FaultInjectingCasStore(inner, [
    { keyIncludes: "counter/sub/grant-1/confirm/reserve", mode: "before", occurrence: 1 },
  ]);
  const gateway = new InMemoryBusinessGateway();
  const { service } = makeService(store, new InMemoryPolicyStore(), gateway);
  const grant = buildGrant();
  const crashed = await service.submit(buildSubmit(grant));
  assert.equal(crashed.status, SubmitStatus.Unknown);

  const claim = await service.readProof(claimKey(grant.grantId, "confirm"));
  assert.equal(claim.idemKey, "idem-1");
  const report = await service.reportConsumption(grant.grantId, "confirm");
  assert.deepEqual(report.subConsumptions, {});
  assert.equal(gateway.effectCount(), 0);

  const resumed = await service.submit(buildSubmit(grant));
  assert.equal(resumed.status, SubmitStatus.Success);
});

test("a mandatory sub-operation missing means the decision can never be SUCCESS", async () => {
  const store = new InMemoryCasStore();
  const gateway = new InMemoryBusinessGateway([
    { effectIdIncludes: ":notify:", mode: "permanent", occurrence: 1 },
  ]);
  const { service } = makeService(store, new InMemoryPolicyStore(), gateway);
  const grant = buildGrant();
  const result = await service.submit(buildSubmit(grant));
  assert.equal(result.status, SubmitStatus.Partial);
  assert.deepEqual(result.executedSubIds, ["reserve", "ledger"]);
  assert.deepEqual(result.pendingSubIds, ["notify"]);
  assert.equal(result.decision!.outcome, "FAILED");

  const storedDecision = await service.readProof(
    decisionKey(grant.grantId, "confirm", "idem-1"),
  );
  assert.equal(storedDecision.outcome, "FAILED");
  const reserveProof = await service.readProof(
    subKey(grant.grantId, "confirm", "idem-1", "reserve"),
  );
  assert.equal((reserveProof as { subId: string }).subId, "reserve");
});

test("every write point can fail and a later retry converges to one decision", async () => {
  const writePoints = [
    "proof/snapshot",
    "ledger/claim",
    "counter/sub/grant-1/confirm/reserve",
    "proof/sub/grant-1/confirm/idem-1/reserve",
    "counter/sub/grant-1/confirm/ledger",
    "proof/sub/grant-1/confirm/idem-1/ledger",
    "counter/sub/grant-1/confirm/notify",
    "proof/sub/grant-1/confirm/idem-1/notify",
    "ledger/decision",
  ];
  for (const point of writePoints) {
    const inner = new InMemoryCasStore();
    const store = new FaultInjectingCasStore(inner, [{ keyIncludes: point, mode: "before", occurrence: 1 }]);
    const gateway = new InMemoryBusinessGateway();
    const { service } = makeService(store, new InMemoryPolicyStore(), gateway);
    const grant = buildGrant();
    let attempt = 0;
    let result;
    try {
      result = await service.submit(buildSubmit(grant));
    } catch (error) {
      assert.ok(error instanceof SimulatedCrash);
      result = await service.submit(buildSubmit(grant));
    }
    while (result.status !== SubmitStatus.Success && attempt < 6) {
      attempt += 1;
      result = await service.submit(buildSubmit(grant));
    }
    assert.equal(result.status, SubmitStatus.Success, `convergence after fault at ${point}`);
    const keys = await inner.listKeys();
    const decisions = keys.filter((key) => key.startsWith("ledger/decision/"));
    assert.equal(decisions.length, 1, `exactly one decision after fault at ${point}`);
    assert.equal(gateway.effectCount(), 3, `exactly three effects after fault at ${point}`);
  }
});
