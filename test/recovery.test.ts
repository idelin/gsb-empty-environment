import assert from "node:assert/strict";
import { test } from "node:test";
import {
  AuthorizationService,
  FaultInjectingCasStore,
  InMemoryBusinessGateway,
  InMemoryCasStore,
  InMemoryPolicyStore,
  SubmitStatus,
} from "../src/index.ts";
import { buildGrant, buildSubmit, testAliases, testKey } from "../src/testing/index.ts";

function makeService(store: FaultInjectingCasStore | InMemoryCasStore, gateway = new InMemoryBusinessGateway()) {
  return new AuthorizationService({
    store,
    policy: new InMemoryPolicyStore(),
    gateway,
    signingKeys: [testKey],
    aliases: testAliases,
  });
}

test("dangling counter reservation is recycled without a duplicate effect", async () => {
  const inner = new InMemoryCasStore();
  const store = new FaultInjectingCasStore(inner, [
    { keyIncludes: "counter/sub/grant-1/confirm/reserve", mode: "before", occurrence: 1 },
  ]);
  const gateway = new InMemoryBusinessGateway();
  const service = makeService(store, gateway);
  const grant = buildGrant();

  const crashed = await service.submit(buildSubmit(grant));
  assert.equal(crashed.status, SubmitStatus.Unknown);
  assert.equal(gateway.effectCount(), 0);

  const resumed = await service.submit(buildSubmit(grant));
  assert.equal(resumed.status, SubmitStatus.Success);
  assert.equal(gateway.effectCount(), 3);
  const report = await service.reportConsumption(grant.grantId, "confirm");
  assert.equal(report.subConsumptions.reserve, 1);
});

test("ambiguous sub-proof write converges and the effect is never duplicated", async () => {
  for (let iteration = 0; iteration < 8; iteration += 1) {
    const inner = new InMemoryCasStore();
    const store = new FaultInjectingCasStore(inner, [
      { keyIncludes: "proof/sub/grant-1/confirm/idem-1/ledger", mode: "ambiguous", occurrence: 1 },
    ]);
    const gateway = new InMemoryBusinessGateway();
    const service = makeService(store, gateway);
    const grant = buildGrant();

    const first = await service.submit(buildSubmit(grant));
    assert.ok(
      first.status === SubmitStatus.Unknown || first.status === SubmitStatus.Success,
      `iteration ${iteration}: ${first.status}`,
    );
    const final = await service.submit(buildSubmit(grant));
    assert.equal(final.status, SubmitStatus.Success, `iteration ${iteration}`);
    assert.equal(gateway.effectCount(), 3, `iteration ${iteration}: no duplicated effects`);
    const keys = await inner.listKeys();
    assert.equal(
      keys.filter((key) => key.startsWith("ledger/decision/")).length,
      1,
      `iteration ${iteration}: single decision`,
    );
  }
});

test("reconcile distinguishes partial progress from full success", async () => {
  const inner = new InMemoryCasStore();
  const store = new FaultInjectingCasStore(inner, [
    { keyIncludes: "counter/sub/grant-1/confirm/ledger", mode: "before", occurrence: 1 },
  ]);
  const gateway = new InMemoryBusinessGateway();
  const service = makeService(store, gateway);
  const grant = buildGrant();

  const crashed = await service.submit(buildSubmit(grant));
  assert.equal(crashed.status, SubmitStatus.Unknown);
  const reconciled = await service.reconcile(grant.grantId, "confirm", "idem-1");
  assert.equal(reconciled.status, SubmitStatus.Partial);
  assert.deepEqual(reconciled.executedSubIds, ["reserve"]);

  const resumed = await service.submit(buildSubmit(grant));
  assert.equal(resumed.status, SubmitStatus.Success);
});

test("crash after decision write but before pointer release still replays success", async () => {
  const inner = new InMemoryCasStore();
  const store = new FaultInjectingCasStore(inner, [
    { keyIncludes: "ledger/active-claim", mode: "ambiguous", occurrence: 2 },
  ]);
  const gateway = new InMemoryBusinessGateway();
  const service = makeService(store, gateway);
  const grant = buildGrant();

  const first = await service.submit(buildSubmit(grant));
  // The decision is durable even though releasing the pointer failed.
  assert.ok(
    first.status === SubmitStatus.Unknown || first.status === SubmitStatus.Success,
    first.status,
  );
  const retried = await service.submit(buildSubmit(grant));
  assert.equal(retried.status, SubmitStatus.Success);
  assert.equal(gateway.effectCount(), 3);
  // The replay eventually releases the writer pointer for the next confirmation.
  const report = await service.reportConsumption(grant.grantId, "confirm");
  assert.equal(report.activeClaim, undefined);
});
