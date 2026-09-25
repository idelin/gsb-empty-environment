import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { KeyDirectory } from "../src/keys.ts";
import { ManualClock } from "../src/clock.ts";
import { DelegationError } from "../src/errors.ts";
import { DelegationAuthority, orderChain } from "../src/authority.ts";
import { attrIn, intCmp } from "../src/policy.ts";

const T0 = 1_000_000;

function setup() {
  const keys = new KeyDirectory();
  for (const name of ["corp", "alice", "bob", "carol", "eve"]) keys.generatePrincipal(name);
  const clock = new ManualClock(T0);
  const authority = new DelegationAuthority(keys, clock);
  return { keys, clock, authority };
}

describe("root grants", () => {
  it("issues a self-signed root and authorizes its holder", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a", "doc-b"],
      actions: ["read"],
      maxDepth: 2,
    });
    const result = authority.authorize({
      grantId: root.grant.id,
      subject: "corp",
      resource: "doc-a",
      action: "read",
      attrs: {},
      at: T0,
    });
    assert.equal(result.allowed, true, JSON.stringify(result.reasons));
  });

  it("rejects root requests outside resource/action scope", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a"],
      actions: ["read"],
      maxDepth: 0,
    });
    const result = authority.authorize({
      grantId: root.grant.id,
      subject: "corp",
      resource: "doc-z",
      action: "write",
      attrs: {},
      at: T0,
    });
    assert.equal(result.allowed, false);
    assert.deepEqual(Array.from(new Set(result.reasons.map((r) => r.code))), ["requestScope"]);
  });
});

describe("delegation scope and depth", () => {
  it("never allows a child to expand resources or actions", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a", "doc-b"],
      actions: ["read", "write"],
      maxDepth: 2,
    });
    assert.throws(
      () =>
        authority.delegate("corp", root.grant.id, {
          subject: "alice",
          resources: ["doc-a", "doc-c"],
          actions: ["read"],
        }),
      (error: unknown) => error instanceof DelegationError && error.code === "nesting",
    );
    assert.throws(
      () =>
        authority.delegate("corp", root.grant.id, {
          subject: "alice",
          resources: ["doc-a"],
          actions: ["delete"],
        }),
      (error: unknown) => error instanceof DelegationError && error.code === "nesting",
    );
  });

  it("enforces the remaining delegation depth", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a"],
      actions: ["read"],
      maxDepth: 1,
    });
    const alice = authority.delegate("corp", root.grant.id, {
      subject: "alice",
      resources: ["doc-a"],
      actions: ["read"],
    });
    assert.equal(alice.grant.maxDepth, 0);
    assert.throws(
      () =>
        authority.delegate("alice", alice.grant.id, {
          subject: "bob",
          resources: ["doc-a"],
          actions: ["read"],
        }),
      (error: unknown) => error instanceof DelegationError && error.code === "depth",
    );
  });

  it("cannot explicitly reserve more depth than the parent allows", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a"],
      actions: ["read"],
      maxDepth: 1,
    });
    assert.throws(
      () =>
        authority.delegate("corp", root.grant.id, {
          subject: "alice",
          resources: ["doc-a"],
          actions: ["read"],
          maxDepth: 1,
        }),
      (error: unknown) => error instanceof DelegationError && error.code === "depth",
    );
  });

  it("a sealed grant cannot be redelegated", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a"],
      actions: ["read"],
      maxDepth: 2,
    });
    const alice = authority.delegate("corp", root.grant.id, {
      subject: "alice",
      resources: ["doc-a"],
      actions: ["read"],
      sealed: true,
    });
    assert.throws(
      () =>
        authority.delegate("alice", alice.grant.id, {
          subject: "bob",
          resources: ["doc-a"],
          actions: ["read"],
        }),
      (error: unknown) => error instanceof DelegationError && error.code === "sealed",
    );
  });
});

describe("delegation time windows", () => {
  it("child grant cannot outlive its parent even with its own long duration", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a"],
      actions: ["read"],
      maxDepth: 2,
      durationSeconds: 100,
    });
    assert.throws(
      () =>
        authority.delegate("corp", root.grant.id, {
          subject: "alice",
          resources: ["doc-a"],
          actions: ["read"],
          durationSeconds: 500,
        }),
      (error: unknown) => error instanceof DelegationError && error.code === "time",
    );
  });

  it("an expired ancestor invalidates the leaf even if the leaf is current", () => {
    const { authority, clock } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a"],
      actions: ["read"],
      maxDepth: 2,
      durationSeconds: 100,
    });
    const alice = authority.delegate("corp", root.grant.id, {
      subject: "alice",
      resources: ["doc-a"],
      actions: ["read"],
      durationSeconds: 90,
    });
    const bob = authority.delegate("alice", alice.grant.id, {
      subject: "bob",
      resources: ["doc-a"],
      actions: ["read"],
      durationSeconds: 80,
    });
    clock.set(T0 + 95);
    const result = authority.authorize({
      grantId: bob.grant.id,
      subject: "bob",
      resource: "doc-a",
      action: "read",
      attrs: {},
      at: T0 + 95,
    });
    assert.equal(result.allowed, false);
    assert.ok(result.reasons.some((r) => r.code === "time"));
  });
});

describe("cycles are never a power source", () => {
  it("rejects self-delegation", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a"],
      actions: ["read"],
      maxDepth: 2,
    });
    assert.throws(
      () =>
        authority.delegate("corp", root.grant.id, {
          subject: "corp",
          resources: ["doc-a"],
          actions: ["read"],
        }),
      (error: unknown) => error instanceof DelegationError && error.code === "cycle",
    );
  });

  it("rejects return delegation to an ancestor principal", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a", "doc-b"],
      actions: ["read"],
      maxDepth: 3,
    });
    const alice = authority.delegate("corp", root.grant.id, {
      subject: "alice",
      resources: ["doc-a", "doc-b"],
      actions: ["read"],
    });
    const bob = authority.delegate("alice", alice.grant.id, {
      subject: "bob",
      resources: ["doc-a", "doc-b"],
      actions: ["read"],
    });
    assert.throws(
      () =>
        authority.delegate("bob", bob.grant.id, {
          subject: "corp",
          resources: ["doc-a"],
          actions: ["read"],
        }),
      (error: unknown) => error instanceof DelegationError && error.code === "cycle",
    );
  });

  it("rejects proofs that orderChain detects as cyclic or anchorless", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a"],
      actions: ["read"],
      maxDepth: 2,
    });
    assert.equal(orderChain([]).failures[0].code, "malformed");
    const link = { grant: root.grant, sig: root.sig };
    assert.equal(orderChain([link, link]).failures[0].code, "cycle");
    const fakeChild = {
      ...link,
      grant: { ...root.grant, id: "ghost", parentId: "missing", issuer: "corp", subject: "alice" },
    };
    assert.equal(orderChain([fakeChild]).failures[0].code, "anchor");

    const forged = authority.prove({
      grantId: root.grant.id,
      subject: "corp",
      resource: "doc-a",
      action: "read",
      attrs: {},
      at: T0,
    });
    const cyclic = { ...forged, chain: [...forged.chain, forged.chain[0]] };
    assert.equal(authority.verifyProof(cyclic).allowed, false);
  });
});

describe("revocation", () => {
  it("revoking an ancestor invalidates every descendant", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a"],
      actions: ["read"],
      maxDepth: 2,
    });
    const alice = authority.delegate("corp", root.grant.id, {
      subject: "alice",
      resources: ["doc-a"],
      actions: ["read"],
    });
    const bob = authority.delegate("alice", alice.grant.id, {
      subject: "bob",
      resources: ["doc-a"],
      actions: ["read"],
    });
    authority.revoke("corp", root.grant.id);
    const result = authority.authorize({
      grantId: bob.grant.id,
      subject: "bob",
      resource: "doc-a",
      action: "read",
      attrs: {},
      at: T0,
    });
    assert.equal(result.allowed, false);
    assert.ok(result.reasons.some((r) => r.code === "revoked" && r.grantId === root.grant.id));
  });

  it("only the issuer may revoke", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a"],
      actions: ["read"],
      maxDepth: 2,
    });
    assert.throws(
      () => authority.revoke("eve", root.grant.id),
      (error: unknown) => error instanceof DelegationError && error.code === "subject",
    );
  });
});

describe("chain policies conjoin", () => {
  it("a child cannot satisfy a denied integer condition imposed above", () => {
    const { authority } = setup();
    const root = authority.issueRoot("corp", {
      subject: "corp",
      resources: ["doc-a"],
      actions: ["read"],
      maxDepth: 2,
      policy: intCmp("clearance", ">=", 3),
    });
    const alice = authority.delegate("corp", root.grant.id, {
      subject: "alice",
      resources: ["doc-a"],
      actions: ["read"],
      policy: attrIn("env", ["prod"]),
    });
    const ok = authority.authorize({
      grantId: alice.grant.id,
      subject: "alice",
      resource: "doc-a",
      action: "read",
      attrs: { clearance: 5, env: "prod" },
      at: T0,
    });
    assert.equal(ok.allowed, true, JSON.stringify(ok.reasons));
    const bad = authority.authorize({
      grantId: alice.grant.id,
      subject: "alice",
      resource: "doc-a",
      action: "read",
      attrs: { clearance: 1, env: "prod" },
      at: T0,
    });
    assert.equal(bad.allowed, false);
    assert.ok(bad.reasons.some((r) => r.code === "policy"));
  });
});
