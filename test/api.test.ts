import { test } from "node:test";
import assert from "node:assert/strict";

import { CapabilitySet, createHost } from "../src/index.ts";

test("array graphs, root overrides and record-shaped policy edges are importable", async () => {
  const host = createHost({
    root: "b",
    graph: [
      { id: "a", properties: { value: { value: 1 } } },
      { id: "b", properties: { value: { value: 2 } } },
      { id: "c", properties: { value: { value: 3 } } },
    ],
    policy: {
      entry: "a",
      nodes: {
        a: { domain: "A" },
        bL: { domain: "B" },
        bR: { domain: "B" },
      },
      edges: {
        abL: { from: "a", to: "bL", action: "pass" },
        abR: { from: "a", to: "bR", action: "pass" },
        bLa: { from: "bL", to: "a", action: "pass" },
        bRa: { from: "bR", to: "a", action: "pass" },
      },
    },
  });

  assert.equal(host.view().value, 2);
  assert.equal(host.view({ capabilities: ["read"] }).value, 2);
  assert.equal(host.view("a", { capabilities: ["read", "enumerate"] }).value, 1);

  const inB = host.viewIn("B");
  assert.equal(host.domainOf(inB), "B");
  assert.equal(inB.value, 2);
  assert.equal(host.isSameIdentity(inB, host.pass(host.view("b"), "abR")), true);

  const asyncView = await host.requestView({ capabilities: ["read"] });
  assert.equal(host.nodeIdOf(asyncView), "b");
  assert.equal(host.isView(new Proxy(asyncView, {})), true);
});

test("capability sets are immutable and snapshot output is structural data", () => {
  const caps = new CapabilitySet(["read", "write"]);
  assert.deepEqual(caps.toArray(), ["read", "write"]);
  assert.equal(caps.has("enumerate"), false);
  assert.equal(Object.isFrozen(caps), true);

  const host = createHost({
    graph: { nodes: { root: { properties: { x: { value: 1 } } } } },
  });
  const view = host.view("root");
  const snapshot = host.snapshot();
  assert.equal(snapshot.cache.identities, 1);
  assert.equal(snapshot.cache.leases, 1);
  assert.equal(snapshot.views.length, 1);
  assert.deepEqual(snapshot.views[0].capabilities, [
    "read",
    "write",
    "invoke",
    "construct",
    "enumerate",
  ]);
  assert.doesNotThrow(() => JSON.stringify(snapshot));
  assert.equal(host.describe(view).nodeId, "root");
});

test("restore revives the edge but never resurrects revoked proxy views", () => {
  const host = createHost({
    graph: { nodes: { root: { properties: { x: { value: 1 } } } } },
    policy: {
      entry: "a",
      nodes: { a: { domain: "A" }, b: { domain: "B" } },
      edges: [{ id: "ab", from: "a", to: "b", action: "pass" }],
    },
  });
  const root = host.view("root");
  const inB = host.pass(root, "ab");
  host.revoke("ab");
  assert.equal(host.isRevoked(inB), true);
  host.restore("ab");
  assert.equal(host.isRevoked(inB), true);

  const fresh = host.view("root", { domain: "B" });
  assert.equal(host.isRevoked(fresh), false);
  assert.equal(fresh.x, 1);
});
