import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { KeyDirectory } from "../src/keys.ts";
import { ManualClock } from "../src/clock.ts";
import { DelegationAuthority } from "../src/authority.ts";
import type { Proof } from "../src/types.ts";

const T0 = 1_700_000_000;

function buildWorld() {
  const keys = new KeyDirectory();
  for (const name of ["corp", "alice", "bob", "carol", "mallory"]) keys.generatePrincipal(name);
  const clock = new ManualClock(T0);
  const authority = new DelegationAuthority(keys, clock);

  // corp owns doc-a/doc-b, read/write, two more delegation hops, 1000s lifetime.
  const root = authority.issueRoot("corp", {
    subject: "corp",
    resources: ["doc-a", "doc-b"],
    actions: ["read", "write"],
    maxDepth: 2,
    durationSeconds: 1000,
  });
  // alice receives read-only access on doc-a.
  const alice = authority.delegate("corp", root.grant.id, {
    subject: "alice",
    resources: ["doc-a"],
    actions: ["read"],
    durationSeconds: 500,
  });
  // bob receives a sealed read grant on doc-a, 300s lifetime.
  const bob = authority.delegate("alice", alice.grant.id, {
    subject: "bob",
    resources: ["doc-a"],
    actions: ["read"],
    sealed: true,
    durationSeconds: 300,
  });
  return { keys, clock, authority, root, alice, bob };
}

describe("full chain proof", () => {
  it("produces a root-first complete chain and verifies", () => {
    const { authority, bob } = buildWorld();
    const result = authority.authorize({
      grantId: bob.grant.id,
      subject: "bob",
      resource: "doc-a",
      action: "read",
      attrs: {},
      at: T0 + 10,
    });
    assert.equal(result.allowed, true, JSON.stringify(result.reasons));
    assert.equal(result.proof.chain.length, 3);
    assert.deepEqual(
      result.proof.chain.map((link) => link.grant.subject),
      ["corp", "alice", "bob"],
    );
    for (const link of result.proof.chain) {
      assert.match(link.sig, /^[A-Za-z0-9+/]+=*$/);
    }
  });

  it("fails verification when a grant payload is tampered", () => {
    const { authority, bob } = buildWorld();
    const proof = authority.prove({
      grantId: bob.grant.id,
      subject: "bob",
      resource: "doc-a",
      action: "read",
      attrs: {},
      at: T0 + 10,
    });
    const tampered: Proof = {
      ...proof,
      chain: proof.chain.map((link) =>
        link.grant.id === bob.grant.id
          ? { ...link, grant: { ...link.grant, resources: ["doc-a", "doc-b"] } }
          : link,
      ),
    };
    const result = authority.verifyProof(tampered);
    assert.equal(result.allowed, false);
    assert.ok(result.reasons.some((r) => r.code === "sig"));
  });

  it("fails verification when a signature is forged", () => {
    const { authority, bob, keys } = buildWorld();
    const proof = authority.prove({
      grantId: bob.grant.id,
      subject: "bob",
      resource: "doc-a",
      action: "read",
      attrs: {},
      at: T0 + 10,
    });
    const forged: Proof = {
      ...proof,
      chain: proof.chain.map((link) =>
        link.grant.id === bob.grant.id
          ? { ...link, sig: keys.sign("mallory", link.grant) }
          : link,
      ),
    };
    const result = authority.verifyProof(forged);
    assert.equal(result.allowed, false);
    assert.ok(result.reasons.some((r) => r.code === "sig"));
  });

  it("fails verification when chain links are reordered", () => {
    const { authority, bob } = buildWorld();
    const proof = authority.prove({
      grantId: bob.grant.id,
      subject: "bob",
      resource: "doc-a",
      action: "read",
      attrs: {},
      at: T0 + 10,
    });
    const reordered: Proof = { ...proof, chain: [...proof.chain].reverse() };
    const result = authority.verifyProof(reordered);
    assert.equal(result.allowed, false);
    assert.ok(result.reasons.some((r) => r.code === "anchor" || r.code === "linkage"));
  });

  it("rejects a self-contained forged chain that never existed in the store", () => {
    const { authority, keys, clock } = buildWorld();
    const forgedRoot = {
      id: "fake-root",
      parentId: null,
      issuer: "mallory",
      subject: "mallory",
      resources: ["doc-a"],
      actions: ["read"],
      maxDepth: 0,
      sealed: false,
      notBefore: T0,
      expiresAt: T0 + 100,
      policy: { kind: "allowAll" as const },
      parentSig: null,
    };
    const signedRoot = { grant: forgedRoot, sig: keys.sign("mallory", forgedRoot) };
    const result = authority.verifyProof(
      {
        leafId: forgedRoot.id,
        subject: "mallory",
        resource: "doc-a",
        action: "read",
        attrs: {},
        at: clock.now(),
        chain: [signedRoot],
      },
      { bindToStore: true },
    );
    assert.equal(result.allowed, false);
    assert.ok(result.reasons.some((r) => r.code === "linkage"));
  });

  it("survives JSON serialization round-trip and still verifies", () => {
    const { authority, bob } = buildWorld();
    const proof = authority.prove({
      grantId: bob.grant.id,
      subject: "bob",
      resource: "doc-a",
      action: "read",
      attrs: { ticket: "t-1" },
      at: T0 + 10,
    });
    const restored = JSON.parse(JSON.stringify(proof)) as Proof;
    const result = authority.verifyProof(restored);
    assert.equal(result.allowed, true, JSON.stringify(result.reasons));
  });

  it("denies wrong subject and wrong resource at the leaf", () => {
    const { authority, bob } = buildWorld();
    const wrongSubject = authority.verifyProof(
      authority.prove({
        grantId: bob.grant.id,
        subject: "carol",
        resource: "doc-a",
        action: "read",
        attrs: {},
        at: T0 + 10,
      }),
    );
    assert.equal(wrongSubject.allowed, false);
    assert.ok(wrongSubject.reasons.some((r) => r.code === "subject"));

    const wrongResource = authority.verifyProof(
      authority.prove({
        grantId: bob.grant.id,
        subject: "bob",
        resource: "doc-b",
        action: "read",
        attrs: {},
        at: T0 + 10,
      }),
    );
    assert.equal(wrongResource.allowed, false);
    assert.ok(wrongResource.reasons.some((r) => r.code === "requestScope"));
  });
});
