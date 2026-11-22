import assert from "node:assert/strict";
import test from "node:test";
import {
  completeDFA,
  constraintDeclaration,
  createPolicySource,
  createTenantRoot,
  createWaiver,
  enumerateDFA,
  exactPathDFA,
  generateSigningKeyPair,
  inheritanceGraph,
  intervalContains,
  issueAssetSetScope,
  policyState,
  prefixDFA,
  resolveEffectivePolicy,
  resourceIdentity,
  validateProvenanceChain,
} from "../src/index.ts";

function fixture() {
  const tenant = createTenantRoot({ tenantId: "tenant-a", issuedAt: 0 });
  const pair = generateSigningKeyPair();
  const source1 = createPolicySource({
    id: "baseline-security",
    signerId: "platform-ops",
    keyPair: pair,
    issuedAt: 0,
  });
  const source2 = createPolicySource({
    id: "baseline-crypto",
    signerId: "platform-crypto",
    keyPair: pair,
    issuedAt: 0,
  });
  const a = resourceIdentity(tenant.root, "asset-1", ["prod", "a"], 1);
  const b = resourceIdentity(tenant.root, "asset-2", ["prod", "a"], 1);
  const renamed = resourceIdentity(tenant.root, "asset-1", ["prod", "renamed"], 2);
  const scope = issueAssetSetScope({
    tenantRoot: tenant.root,
    tenantPrivateKey: tenant.privateKey,
    identities: [a],
    setId: "promised-at-issuance",
    issuedAt: 0,
  });
  return { tenant, pair, source1, source2, a, b, renamed, scope };
}

test("same literal from two signed sources is intersected but kept separate", () => {
  const f = fixture();
  const graph = inheritanceGraph([
    {
      id: "asset",
      parents: ["security", "crypto"],
    },
    {
      id: "security",
      declarations: [
        constraintDeclaration({
          id: "tls",
          key: "tls.version",
          allowed: ["1.2", "1.3"],
          literal: "TLS must be at least 1.2",
          source: f.source1,
        }),
      ],
    },
    {
      id: "crypto",
      declarations: [
        constraintDeclaration({
          id: "tls",
          key: "tls.version",
          allowed: ["1.2"],
          literal: "TLS must be at least 1.2",
          source: f.source2,
        }),
      ],
    },
  ]);
  const result = resolveEffectivePolicy({
    state: policyState(graph, 100),
    target: "asset",
    identity: f.a,
  });

  assert.equal(result.status, "resolved");
  assert.deepEqual(result.constraints["tls.version"].allowed, ["1.2"]);
  assert.deepEqual(
    result.constraints["tls.version"].origins.map((origin) => origin.sourceId),
    ["baseline-crypto", "baseline-security"],
  );
  assert.equal(result.constraints["tls.version"].origins.length, 2);
  for (const origin of result.constraints["tls.version"].origins) {
    for (const chain of origin.chains) {
      assert.equal(validateProvenanceChain(graph, chain, "asset"), true);
    }
  }
});

test("empty set intersection is a conflict, never an implicit default", () => {
  const f = fixture();
  const graph = inheritanceGraph([
    { id: "asset", parents: ["a", "b"] },
    {
      id: "a",
      declarations: [
        constraintDeclaration({
          id: "mode",
          key: "encryption.mode",
          allowed: ["gcm"],
          source: f.source1,
        }),
      ],
    },
    {
      id: "b",
      declarations: [
        constraintDeclaration({
          id: "mode",
          key: "encryption.mode",
          allowed: ["cbc"],
          source: f.source2,
        }),
      ],
    },
  ]);
  const result = resolveEffectivePolicy({
    state: policyState(graph, 100),
    target: "asset",
    identity: f.a,
  });
  assert.equal(result.status, "conflict");
  assert.equal(result.constraints["encryption.mode"].status, "conflict");
  assert.deepEqual(result.constraints["encryption.mode"].allowed, []);
  assert.equal(result.constraints["encryption.mode"].origins.length, 2);
});

test("a waiver cancels only the named signed origin", () => {
  const f = fixture();
  const graph = inheritanceGraph([
    { id: "asset", parents: ["security", "crypto"] },
    {
      id: "security",
      declarations: [
        constraintDeclaration({
          id: "tls",
          key: "tls.version",
          allowed: ["1.2"],
          literal: "TLS must be at least 1.2",
          source: f.source1,
        }),
      ],
    },
    {
      id: "crypto",
      declarations: [
        constraintDeclaration({
          id: "tls",
          key: "tls.version",
          allowed: ["1.2"],
          literal: "TLS must be at least 1.2",
          source: f.source2,
        }),
      ],
    },
  ]);
  const waiver = createWaiver({
    id: "waiver-security-only",
    issuer: f.source1,
    issuerPrivateKey: f.pair.privateKey,
    target: { sourceId: "baseline-security", constraintId: "tls" },
    scope: f.scope,
    issuedAt: 10,
    expiresAt: 20,
  });
  const result = resolveEffectivePolicy({
    state: policyState(graph, 19, [waiver]),
    target: "asset",
    identity: f.a,
  });

  assert.equal(result.status, "resolved");
  assert.deepEqual(
    result.constraints["tls.version"].origins.map((origin) => origin.sourceId),
    ["baseline-crypto"],
  );
  assert.deepEqual(result.waived.map((application) => application.waiverId), [
    "waiver-security-only",
  ]);
});

test("two visually identical waivers are distinct signed acts", () => {
  const f = fixture();
  const graph = inheritanceGraph([
    { id: "asset", parents: ["security", "crypto"] },
    {
      id: "security",
      declarations: [
        constraintDeclaration({
          id: "tls",
          key: "tls.version",
          allowed: ["1.2"],
          literal: "TLS must be at least 1.2",
          source: f.source1,
        }),
      ],
    },
    {
      id: "crypto",
      declarations: [
        constraintDeclaration({
          id: "tls",
          key: "tls.version",
          allowed: ["1.2"],
          literal: "TLS must be at least 1.2",
          source: f.source2,
        }),
      ],
    },
  ]);
  const common = {
    issuer: f.source1,
    issuerPrivateKey: f.pair.privateKey,
    scope: f.scope,
    issuedAt: 10,
    expiresAt: 20,
  };
  const first = createWaiver({
    ...common,
    id: "waiver-1",
    target: { sourceId: "baseline-security", constraintId: "tls" },
  });
  const second = createWaiver({
    ...common,
    id: "waiver-2",
    target: { sourceId: "baseline-crypto", constraintId: "tls" },
  });
  assert.notEqual(first.signature, second.signature);

  const result = resolveEffectivePolicy({
    state: policyState(graph, 15, [first, second]),
    target: "asset",
    identity: f.a,
  });
  assert.equal(result.status, "resolved");
  assert.equal(result.constraints["tls.version"], undefined);
  assert.deepEqual(
    result.waived.map((application) => application.waiverId),
    ["waiver-1", "waiver-2"],
  );
});

test("crossing inheritance is revoked only for the signed target source", () => {
  const f = fixture();
  const graph = inheritanceGraph([
    { id: "asset", parents: ["left", "right"] },
    { id: "left", parents: ["source-one"] },
    { id: "right", parents: ["source-one", "source-two"] },
    {
      id: "source-one",
      declarations: [
        constraintDeclaration({
          id: "tls",
          key: "tls.version",
          allowed: ["1.2"],
          source: f.source1,
        }),
      ],
    },
    {
      id: "source-two",
      declarations: [
        constraintDeclaration({
          id: "tls",
          key: "tls.version",
          allowed: ["1.2"],
          source: f.source2,
        }),
      ],
    },
  ]);
  const waiver = createWaiver({
    id: "cross-cut",
    issuer: f.source1,
    issuerPrivateKey: f.pair.privateKey,
    target: { sourceId: "baseline-security", constraintId: "tls" },
    scope: f.scope,
    issuedAt: 0,
    expiresAt: 100,
  });
  const result = resolveEffectivePolicy({
    state: policyState(graph, 10, [waiver]),
    target: "asset",
    identity: f.a,
  });
  assert.deepEqual(
    result.constraints["tls.version"].origins.map((origin) => origin.sourceId),
    ["baseline-crypto"],
  );
});

test("waiver expiry uses an inclusive issue and exclusive expiry boundary", () => {
  const f = fixture();
  const graph = inheritanceGraph([
    { id: "asset", parents: ["security"] },
    {
      id: "security",
      declarations: [
        constraintDeclaration({
          id: "tls",
          key: "tls.version",
          allowed: ["1.2"],
          source: f.source1,
        }),
      ],
    },
  ]);
  const waiver = createWaiver({
    id: "bounded-waiver",
    issuer: f.source1,
    issuerPrivateKey: f.pair.privateKey,
    target: { sourceId: "baseline-security", constraintId: "tls" },
    scope: f.scope,
    issuedAt: 10,
    expiresAt: 20,
  });
  const at = (time: number) =>
    resolveEffectivePolicy({
      state: policyState(graph, time, [waiver]),
      target: "asset",
      identity: f.a,
    });

  assert.equal(at(9.999).constraints["tls.version"].origins.length, 1);
  assert.equal(at(10).constraints["tls.version"], undefined);
  assert.equal(at(19.999).constraints["tls.version"], undefined);
  assert.equal(at(20).constraints["tls.version"].origins.length, 1);

  assert.equal(intervalContains({ start: 10, end: 20 }, 10), true);
  assert.equal(intervalContains({ start: 10, end: 20 }, 20), false);
  assert.equal(
    intervalContains(
      { start: 10, end: 20, startInclusive: false, endInclusive: true },
      10,
    ),
    false,
  );
  assert.equal(
    intervalContains(
      { start: 10, end: 20, startInclusive: false, endInclusive: true },
      20,
    ),
    true,
  );
});

test("old waiver scope does not expand to a new resource or a renamed resource", () => {
  const f = fixture();
  const graph = inheritanceGraph([
    { id: "asset", parents: ["security"] },
    {
      id: "security",
      declarations: [
        constraintDeclaration({
          id: "tls",
          key: "tls.version",
          allowed: ["1.2"],
          source: f.source1,
        }),
      ],
    },
  ]);
  const waiver = createWaiver({
    id: "old-scope",
    issuer: f.source1,
    issuerPrivateKey: f.pair.privateKey,
    target: { sourceId: "baseline-security", constraintId: "tls" },
    scope: f.scope,
    issuedAt: 10,
    expiresAt: 20,
  });
  const newVersion = resourceIdentity(
    f.tenant.root,
    "asset-1",
    ["prod", "a"],
    2,
  );

  for (const identity of [f.b, f.renamed, newVersion]) {
    const result = resolveEffectivePolicy({
      state: policyState(graph, 15, [waiver]),
      target: "asset",
      identity,
    });
    assert.equal(result.status, "unknown");
    assert.ok(
      result.unknown.some((reason) => reason.code === "missing-membership-proof"),
    );
    assert.equal(result.constraints["tls.version"].origins.length, 1);
    assert.equal(result.waived.length, 0);
  }
});

test("cycles reach a stable fixed point without losing another support path", () => {
  const f = fixture();
  const graph = inheritanceGraph([
    {
      id: "cycle-a",
      parents: ["cycle-b"],
      declarations: [
        constraintDeclaration({
          id: "cycle-rule",
          key: "audit",
          allowed: ["on", "verbose"],
          source: f.source1,
        }),
      ],
    },
    {
      id: "cycle-b",
      parents: ["cycle-a"],
      declarations: [
        constraintDeclaration({
          id: "cycle-rule-2",
          key: "audit",
          allowed: ["on"],
          source: f.source2,
        }),
      ],
    },
  ]);
  const cycleResult = resolveEffectivePolicy({
    state: policyState(graph, 100),
    target: "cycle-b",
    identity: f.a,
  });
  assert.equal(cycleResult.status, "resolved");
  assert.deepEqual(cycleResult.constraints.audit.allowed, ["on"]);
  assert.equal(cycleResult.constraints.audit.origins.length, 2);
});

test("multiple inheritance paths are all represented in provenance", () => {
  const f = fixture();
  const graph = inheritanceGraph([
    { id: "asset", parents: ["left", "right"] },
    { id: "left", parents: ["base"] },
    { id: "right", parents: ["base"] },
    {
      id: "base",
      declarations: [
        constraintDeclaration({
          id: "shared",
          key: "shared.rule",
          allowed: ["yes"],
          source: f.source1,
        }),
      ],
    },
  ]);
  const result = resolveEffectivePolicy({
    state: policyState(graph, 100),
    target: "asset",
    identity: f.a,
  });
  const chains = result.constraints["shared.rule"].origins[0].chains;
  assert.equal(chains.length, 2);
  assert.deepEqual(
    chains.map((chain) => chain.nodeIds.join(">")).sort(),
    ["base>left>asset", "base>right>asset"],
  );
  assert.ok(chains.every((chain) => validateProvenanceChain(graph, chain, "asset")));
});

test("exhaustive path enumeration matches path-scoped declarations", () => {
  const f = fixture();
  const graph = inheritanceGraph([
    { id: "asset", parents: ["left", "right"] },
    {
      id: "left",
      scope: { path: exactPathDFA(["prod", "a"]) },
      declarations: [
        constraintDeclaration({
          id: "left-rule",
          key: "side",
          allowed: ["left"],
          source: f.source1,
        }),
      ],
    },
    {
      id: "right",
      scope: { path: exactPathDFA(["prod", "b"]) },
      declarations: [
        constraintDeclaration({
          id: "right-rule",
          key: "side",
          allowed: ["right"],
          source: f.source2,
        }),
      ],
    },
  ]);
  const universe = completeDFA(prefixDFA(["prod"]), ["prod", "a", "b"]);
  const words = enumerateDFA(universe, { maxDepth: 2, maxWords: 100 });
  const observed = new Map<string, string[]>();
  for (const word of words) {
    const identity = resourceIdentity(f.tenant.root, "asset-1", word, word.join("/"));
    const result = resolveEffectivePolicy({
      state: policyState(graph, 100),
      target: "asset",
      identity,
    });
    observed.set(
      word.join("/"),
      result.constraints.side?.origins.map((origin) => origin.sourceId) ?? [],
    );
  }
  assert.deepEqual(observed.get("prod/a"), ["baseline-security"]);
  assert.deepEqual(observed.get("prod/b"), ["baseline-crypto"]);
  assert.deepEqual(observed.get("prod"), []);
});
