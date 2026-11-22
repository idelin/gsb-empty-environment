import assert from "node:assert/strict";
import test from "node:test";
import {
  analyzePolicyChange,
  constraintDeclaration,
  createPolicySource,
  createTenantRoot,
  createWaiver,
  generateSigningKeyPair,
  inheritanceGraph,
  issueAssetSetScope,
  policyState,
  resolveEffectivePolicy,
  resourceIdentity,
} from "../src/index.ts";

function setup() {
  const tenant = createTenantRoot({ tenantId: "tenant-diff", issuedAt: 0 });
  const pair = generateSigningKeyPair();
  const source = createPolicySource({
    id: "security-baseline",
    signerId: "security",
    keyPair: pair,
    issuedAt: 0,
  });
  const second = createPolicySource({
    id: "crypto-baseline",
    signerId: "crypto",
    keyPair: pair,
    issuedAt: 0,
  });
  const identity = resourceIdentity(tenant.root, "asset-1", ["prod", "a"], 1);
  return { tenant, pair, source, second, identity };
}

function singleRuleGraph(source: ReturnType<typeof setup>["source"], allowed: string[]) {
  return inheritanceGraph([
    {
      id: "asset",
      declarations: [
        constraintDeclaration({
          id: "tls",
          key: "tls.version",
          allowed,
          literal: "TLS minimum",
          source,
        }),
      ],
    },
  ]);
}

test("policy diff classifies tightening, loosening and equality", () => {
  const f = setup();
  const widened = singleRuleGraph(f.source, ["1.2", "1.3"]);
  const narrowed = singleRuleGraph(f.source, ["1.2"]);
  const asset = {
    id: f.identity.resourceId,
    before: { identity: f.identity, target: "asset" },
    after: { identity: f.identity, target: "asset" },
  };

  const tightened = analyzePolicyChange({
    before: policyState(widened, 100),
    after: policyState(narrowed, 100),
    assets: [asset],
  });
  assert.equal(tightened.regions[0].classification, "tightened");
  assert.equal(tightened.regions[0].deltas[0].relation, "strict");
  assert.equal(tightened.disjoint, true);

  const loosened = analyzePolicyChange({
    before: policyState(narrowed, 100),
    after: policyState(widened, 100),
    assets: [asset],
  });
  assert.equal(loosened.regions[0].classification, "loosened");
  assert.equal(loosened.regions[0].deltas[0].relation, "permissive");

  const unchanged = analyzePolicyChange({
    before: policyState(narrowed, 100),
    after: policyState(narrowed, 100),
    assets: [asset],
  });
  assert.equal(unchanged.regions[0].classification, "unchanged");
  assert.equal(unchanged.chains.length > 0, true);
  assert.ok(unchanged.chains.every((chain) => chain.valid));
});

test("missing membership proof produces an unknown-only-for-proof region", () => {
  const f = setup();
  const graph = singleRuleGraph(f.source, ["1.2"]);
  const scope = issueAssetSetScope({
    tenantRoot: f.tenant.root,
    tenantPrivateKey: f.tenant.privateKey,
    identities: [f.identity],
    setId: "unproved-set",
    issuedAt: 0,
    proofFor: [],
  });
  const waiver = createWaiver({
    id: "unproved-waiver",
    issuer: f.source,
    issuerPrivateKey: f.pair.privateKey,
    target: { sourceId: "security-baseline", constraintId: "tls" },
    scope,
    issuedAt: 0,
    expiresAt: 100,
  });
  const before = policyState(graph, 50);
  const after = policyState(graph, 50, [waiver]);
  const asset = {
    id: "asset-1",
    before: { identity: f.identity, target: "asset" },
    after: { identity: f.identity, target: "asset" },
  };

  const direct = resolveEffectivePolicy({
    state: after,
    target: "asset",
    identity: f.identity,
  });
  assert.equal(direct.status, "unknown");
  assert.equal(direct.constraints["tls.version"].status, "active");

  const analysis = analyzePolicyChange({
    before,
    after,
    assets: [asset],
  });
  assert.equal(analysis.regions[0].classification, "unknown");
  assert.equal(analysis.regions[0].unknownOnlyForProof, true);
  assert.ok(
    analysis.regions[0].unknown.some((reason) => reason.code === "missing-membership-proof"),
  );
});

test("a conflict remains conflicted in the diff", () => {
  const f = setup();
  const before = singleRuleGraph(f.source, ["1.2"]);
  const after = inheritanceGraph([
    { id: "asset", parents: ["one", "two"] },
    {
      id: "one",
      declarations: [
        constraintDeclaration({
          id: "tls-a",
          key: "tls.version",
          allowed: ["1.2"],
          source: f.source,
        }),
      ],
    },
    {
      id: "two",
      declarations: [
        constraintDeclaration({
          id: "tls-b",
          key: "tls.version",
          allowed: ["1.3"],
          source: f.second,
        }),
      ],
    },
  ]);
  const asset = {
    id: "asset-1",
    before: { identity: f.identity, target: "asset" },
    after: { identity: f.identity, target: "asset" },
  };
  const analysis = analyzePolicyChange({
    before: policyState(before, 100),
    after: policyState(after, 100),
    assets: [asset],
  });
  assert.equal(analysis.regions[0].classification, "conflict");
  assert.equal(analysis.regions[0].deltas[0].relation, "conflict");
});

test("regions for multiple assets are disjoint", () => {
  const f = setup();
  const second = resourceIdentity(f.tenant.root, "asset-2", ["prod", "b"], 1);
  const before = singleRuleGraph(f.source, ["1.2", "1.3"]);
  const after = singleRuleGraph(f.source, ["1.2"]);
  const analysis = analyzePolicyChange({
    before: policyState(before, 100),
    after: policyState(after, 100),
    assets: [
      {
        id: "asset-1",
        before: { identity: f.identity, target: "asset" },
        after: { identity: f.identity, target: "asset" },
      },
      {
        id: "asset-2",
        before: { identity: second, target: "asset" },
        after: { identity: second, target: "asset" },
      },
    ],
  });
  assert.equal(analysis.regions.length, 2);
  assert.equal(analysis.disjoint, true);
  assert.ok(analysis.regions.every((region) => region.classification === "tightened"));
});

