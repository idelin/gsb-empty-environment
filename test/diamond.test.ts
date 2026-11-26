import { test } from "node:test";
import assert from "node:assert/strict";

import {
  CapabilityError,
  RevokedViewError,
  createHost,
} from "../src/index.ts";

function diamond(onRevoke?: "invalidate" | { attenuate: string }) {
  const graph = {
    nodes: {
      doc: {
        properties: {
          alpha: { value: "a" },
          title: { value: "shared" },
          leftSecret: { value: "left", grants: "eL2" },
          middle: { value: "m" },
          rightSecret: { value: "right", grants: "eR2" },
          zeta: { value: "z" },
          child: { ref: "child" },
        },
      },
      child: { properties: { value: { value: 7 } } },
      fn: { kind: "function", invoke: { ref: "child" } },
    },
  };
  const policy = {
    entry: "s",
    nodes: {
      s: { domain: "A" },
      l: { domain: "B" },
      r: { domain: "C" },
      t: { domain: "D" },
    },
    edges: [
      { id: "eL", from: "s", to: "l", kind: "grant", action: "open" },
      {
        id: "eL2",
        from: "l",
        to: "t",
        kind: "grant",
        action: "open",
        ...(onRevoke === undefined ? {} : { onRevoke }),
      },
      { id: "eR", from: "s", to: "r", kind: "grant", action: "open" },
      { id: "eR2", from: "r", to: "t", kind: "grant", action: "open" },
    ],
  };
  return createHost({ graph, policy });
}

test("diamond branches share bisimulation identity but have independent leases", () => {
  const host = diamond();
  const root = host.view("doc");
  const left = host.pass(host.pass(root, "eL"), "eL2");
  const right = host.pass(host.pass(root, "eR"), "eR2");

  assert.notEqual(left, right);
  assert.equal(host.isSameIdentity(left, right), true);
  assert.equal(host.identityOf(left), host.identityOf(right));
  assert.deepEqual(host.describe(left).support, ["eL", "eL2"]);
  assert.deepEqual(host.describe(right).support, ["eR", "eR2"]);

  assert.equal(left.child, left.child);
  assert.notEqual(left.child, right.child);
  assert.equal(host.isSameIdentity(left.child, right.child), true);
});

test("revoking one diamond edge invalidates only its dependent cache views", async () => {
  const host = diamond();
  const root = host.view("doc");
  const midLeft = host.pass(root, "eL");
  const midRight = host.pass(root, "eR");
  const left = host.pass(midLeft, "eL2");
  const right = host.pass(midRight, "eR2");
  const leftChild = left.child;
  const rightChild = right.child;
  const rightIdentity = host.identityOf(right);

  const pendingLeft = host.requestPass(midLeft, "eL2", { delayMs: 20 });
  const pendingRight = host.requestPass(midRight, "eR2", { delayMs: 20 });

  const event = host.revoke("eL2");

  assert.equal(host.isRevoked(left), true);
  assert.equal(host.isRevoked(leftChild), true);
  assert.equal(host.isRevoked(right), false);
  assert.equal(host.isRevoked(rightChild), false);
  assert.equal(host.identityOf(right), rightIdentity);
  assert.equal(right.title, "shared");
  assert.equal(rightChild.value, 7);
  assert.deepEqual(Object.keys(right).sort(), [
    "alpha",
    "child",
    "middle",
    "rightSecret",
    "title",
    "zeta",
  ]);
  assert.ok(event.invalidated.length >= 2);
  assert.equal(event.invalidated.every((snapshot) => snapshot.revoked), true);
  assert.throws(() => left.title, (error) => error instanceof RevokedViewError);

  await assert.rejects(pendingLeft, (error) => error instanceof RevokedViewError);
  const delivered = await pendingRight;
  assert.equal(host.nodeIdOf(delivered), "doc");
  assert.equal(host.domainOf(delivered), "D");
  assert.equal(host.pendingCount, 0);
});

test("async invoke results are invalidated only on the revoked branch", async () => {
  const host = diamond();
  const root = host.view("fn");
  const left = host.pass(host.pass(root, "eL"), "eL2");
  const right = host.pass(host.pass(root, "eR"), "eR2");

  const pendingLeft = host.requestInvoke(left, [], { delayMs: 20 });
  const pendingRight = host.requestInvoke(right, [], { delayMs: 20 });
  host.revoke("eL2");

  await assert.rejects(pendingLeft, (error) => error instanceof RevokedViewError);
  const child = await pendingRight;
  assert.equal(host.nodeIdOf(child), "child");
  assert.equal((child as { value: number }).value, 7);
});

test("revocation can downgrade instead of invalidating, with no read capability left", async () => {
  const host = diamond({ attenuate: "read" });
  const root = host.view("fn");
  const left = host.pass(host.pass(root, "eL"), "eL2");
  const right = host.pass(host.pass(root, "eR"), "eR2");
  const pending = host.requestInvoke(left, [], { delayMs: 20 });

  const event = host.revoke("eL2");
  assert.equal(event.downgraded.length >= 1, true);
  assert.equal(
    event.downgraded.every((snapshot) => !snapshot.capabilities.includes("read")),
    true,
  );
  assert.equal(host.isRevoked(left), false);
  assert.equal(host.capabilitiesOf(left).has("read"), false);
  assert.equal(host.capabilitiesOf(left).has("enumerate"), true);
  assert.equal(host.isRevoked(right), false);
  assert.equal(host.capabilitiesOf(right).has("read"), true);
  await assert.rejects(pending, (error) => error instanceof RevokedViewError);
  assert.throws(() => (left as { title?: unknown }).title, (error) => {
    return error instanceof CapabilityError;
  });

  const afterDowngrade = await host.requestInvoke(left, [], { delayMs: 1 });
  assert.equal(host.nodeIdOf(afterDowngrade), "child");
});

test("enumerate never leaks a revoked property or its ordering slot", () => {
  const host = diamond();
  const root = host.view("doc");
  const left = host.pass(host.pass(root, "eL"), "eL2");
  const right = host.pass(host.pass(root, "eR"), "eR2");

  const before = Object.keys(right);
  assert.deepEqual(before, [
    "alpha",
    "child",
    "leftSecret",
    "middle",
    "rightSecret",
    "title",
    "zeta",
  ]);
  assert.equal(left.title, "shared");

  host.revoke("eL2");
  const after = Object.keys(right);
  assert.deepEqual(after, [
    "alpha",
    "child",
    "middle",
    "rightSecret",
    "title",
    "zeta",
  ]);
  assert.equal("leftSecret" in right, false);
  assert.equal(right.leftSecret, undefined);
  assert.equal(
    Object.getOwnPropertyDescriptor(right, "leftSecret"),
    undefined,
  );
  assert.equal(right.rightSecret, "right");
  const sharedBefore = before.filter((key) => key !== "leftSecret");
  assert.deepEqual(after, sharedBefore);
});

test("properties added through a branch disappear for independent branches on revocation", () => {
  const host = diamond();
  const root = host.view("doc");
  const left = host.pass(host.pass(root, "eL"), "eL2");
  const right = host.pass(host.pass(root, "eR"), "eR2");

  (left as Record<string, unknown>).added = "from-left";
  assert.equal((right as Record<string, unknown>).added, "from-left");
  assert.equal(Object.keys(right).includes("added"), true);

  host.revoke("eL2");
  assert.equal(host.isRevoked(left), true);
  assert.equal(host.isRevoked(right), false);
  assert.equal((right as Record<string, unknown>).added, undefined);
  assert.equal("added" in right, false);
  assert.equal(Object.keys(right).includes("added"), false);
});

test("writes to an existing public property do not damage the independent branch", () => {
  const host = diamond();
  const root = host.view("doc");
  const left = host.pass(host.pass(root, "eL"), "eL2");
  const right = host.pass(host.pass(root, "eR"), "eR2");

  left.title = left.child;
  assert.equal(host.nodeIdOf(right.title), "child");

  host.revoke("eL2");
  assert.equal(host.isRevoked(right), false);
  assert.equal(host.nodeIdOf(right.title), "child");
  assert.equal("title" in right, true);
  assert.equal(Object.keys(right).includes("title"), true);
});

test("revocation listeners fire only for dependent authorization edges", () => {
  const host = diamond();
  const seen: string[] = [];
  const offLeft = host.onRevoke((event) => seen.push(`left:${event.edgeId}`), "eL2");
  const offRight = host.onRevoke((event) => seen.push(`right:${event.edgeId}`), ["eR2"]);
  host.onRevoke((event) => seen.push(`all:${event.edgeId}`));

  host.revoke("eL2");
  assert.deepEqual(seen, ["left:eL2", "all:eL2"]);
  offLeft();
  offRight();
});
