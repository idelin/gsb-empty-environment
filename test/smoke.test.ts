import assert from "node:assert/strict";
import { test } from "node:test";
import {
  AuthorizationService,
  InMemoryBusinessGateway,
  InMemoryCasStore,
  InMemoryPolicyStore,
  SubmitStatus,
} from "../src/index.ts";
import { buildGrant, buildSubmit, testAliases, testKey } from "../src/testing/index.ts";

test("smoke: happy path succeeds with proofs", async () => {
  const service = new AuthorizationService({
    store: new InMemoryCasStore(),
    policy: new InMemoryPolicyStore(),
    gateway: new InMemoryBusinessGateway(),
    signingKeys: [testKey],
    aliases: testAliases,
  });
  const grant = buildGrant();
  const result = await service.submit(buildSubmit(grant));
  assert.equal(result.status, SubmitStatus.Success);
  assert.ok(result.decision);
  assert.equal(result.proofKeys.length, 4);
});
