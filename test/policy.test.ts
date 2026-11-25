import assert from "node:assert/strict";
import { test } from "node:test";
import {
  AuthorizationService,
  type BusinessGateway,
  InMemoryBusinessGateway,
  InMemoryCasStore,
  InMemoryPolicyStore,
  SubmitCode,
  SubmitStatus,
} from "../src/index.ts";
import { buildGrant, buildSubmit, testAliases, testKey } from "../src/testing/index.ts";

function makeService(policy = new InMemoryPolicyStore()) {
  const gateway = new InMemoryBusinessGateway();
  const service = new AuthorizationService({
    store: new InMemoryCasStore(),
    policy,
    gateway,
    signingKeys: [testKey],
    aliases: testAliases,
  });
  return { service, policy, gateway };
}

test("action-level cap is enforced across separate confirmations", async () => {
  const { service, gateway } = makeService();
  const grant = buildGrant({ maxActionConsumptions: 2, maxSubConsumptions: 2 });

  const first = await service.submit(buildSubmit(grant, { idemKey: "idem-1" }));
  assert.equal(first.status, SubmitStatus.Success);
  const second = await service.submit(buildSubmit(grant, { idemKey: "idem-2" }));
  assert.equal(second.status, SubmitStatus.Success);
  const third = await service.submit(buildSubmit(grant, { idemKey: "idem-3" }));
  assert.equal(third.status, SubmitStatus.Failed);
  assert.equal(third.code, SubmitCode.ActionLimitExhausted);

  const report = await service.reportConsumption(grant.grantId, "confirm");
  assert.equal(report.actionConsumptions, 2);
  // Two full confirmations each ran three sub-effects, each within its own cap.
  assert.equal(gateway.effectCount(), 6);
});

test("policy shrink can lower the signed action cap", async () => {
  const policy = new InMemoryPolicyStore();
  const { service } = makeService(policy);
  const grant = buildGrant({ maxActionConsumptions: 5 });
  policy.setActionCap("confirm", 1);

  const first = await service.submit(buildSubmit(grant, { idemKey: "idem-1" }));
  assert.equal(first.status, SubmitStatus.Success);
  const second = await service.submit(buildSubmit(grant, { idemKey: "idem-2" }));
  assert.equal(second.code, SubmitCode.ActionLimitExhausted);
});

test("policy shrink to zero blocks even the first confirmation", async () => {
  const policy = new InMemoryPolicyStore();
  policy.setActionCap("confirm", 0);
  const { service } = makeService(policy);
  const result = await service.submit(buildSubmit(buildGrant()));
  assert.equal(result.code, SubmitCode.ActionLimitExhausted);
});

test("sub-operation cap blocks extra effects under the same parent action", async () => {
  const { service } = makeService();
  const grant = buildGrant({ maxActionConsumptions: 3, maxSubConsumptions: 1 });
  await service.submit(buildSubmit(grant, { idemKey: "idem-1" }));
  const second = await service.submit(buildSubmit(grant, { idemKey: "idem-2" }));
  // The second confirmation reserves the action slot but the sub cap trips first.
  assert.equal(second.status, SubmitStatus.Failed);
  assert.equal(second.code, SubmitCode.SubLimitExhausted);
  const report = await service.reportConsumption(grant.grantId, "confirm");
  assert.equal(report.subConsumptions.reserve, 1);
});

test("a field revoked before submission is rejected even if validation started earlier", async () => {
  const policy = new InMemoryPolicyStore();
  const { service } = makeService(policy);
  const grant = buildGrant();
  policy.revokePath(["payee", "legalName"]);
  const result = await service.submit(buildSubmit(grant));
  assert.equal(result.status, SubmitStatus.Failed);
  assert.equal(result.code, SubmitCode.FieldRevoked);
});

test("late revocation between sub-operations stops remaining work and stays partial", async () => {
  const policy = new InMemoryPolicyStore();
  const inner = new InMemoryBusinessGateway();
  const revokingGateway = {
    async applyEffect(request: Parameters<BusinessGateway["applyEffect"]>[0]) {
      const result = await inner.applyEffect(request);
      if (request.subId === "reserve") {
        policy.revokePath(["payee", "legalName"]);
      }
      return result;
    },
    findEffect(effectId: string) {
      return inner.findEffect(effectId);
    },
    countEffects(grantId: string, actionId: string, subId: string) {
      return inner.countEffects(grantId, actionId, subId);
    },
  } satisfies BusinessGateway;
  const service = new AuthorizationService({
    store: new InMemoryCasStore(),
    policy,
    gateway: revokingGateway,
    signingKeys: [testKey],
    aliases: testAliases,
  });
  const grant = buildGrant();
  const result = await service.submit(buildSubmit(grant));
  assert.equal(result.status, SubmitStatus.Partial);
  assert.deepEqual(result.executedSubIds, ["reserve"]);
  assert.deepEqual(result.pendingSubIds, ["ledger", "notify"]);
  assert.equal(result.decision!.code, SubmitCode.FieldRevoked);
});
