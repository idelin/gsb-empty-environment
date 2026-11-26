import { test } from "node:test";
import assert from "node:assert/strict";

import {
  CapabilityError,
  InvalidWriteError,
  createHost,
} from "../src/index.ts";

function scenario() {
  const graph = {
    root: "root",
    nodes: {
      root: {
        kind: "object",
        prototype: "base",
        properties: {
          name: { value: "root" },
          child: { ref: "child" },
          fn: { ref: "fn" },
        },
      },
      child: {
        kind: "object",
        prototype: "base",
        properties: {
          n: { value: 1 },
        },
      },
      base: {
        kind: "object",
        properties: {
          inherited: { value: "from-base" },
        },
      },
      fn: {
        kind: "function",
        invoke: { ref: "child" },
        construct: { ref: "child" },
      },
    },
  } as const;

  const policy = {
    entry: "a",
    nodes: {
      a: { domain: "A" },
      bL: { domain: "B" },
      bR: { domain: "B" },
      cL: { domain: "C" },
      cR: { domain: "C" },
      a2: { domain: "A" },
    },
    edges: [
      { id: "aL", from: "a", to: "bL", action: "pass" },
      { id: "aR", from: "a", to: "bR", action: "pass" },
      { id: "bLcL", from: "bL", to: "cL", action: "pass" },
      { id: "bRcR", from: "bR", to: "cR", action: "pass" },
      { id: "cLa2", from: "cL", to: "a2", action: "pass" },
      { id: "cRa2", from: "cR", to: "a2", action: "pass" },
      { id: "a2bL", from: "a2", to: "bL", action: "pass" },
      { id: "a2bR", from: "a2", to: "bR", action: "pass" },
    ],
  } as const;

  return createHost({ graph, policy });
}

test("A -> B -> C -> A restores one cached identity across folded policy nodes", () => {
  const host = scenario();
  const v0 = host.view("root");
  const left = host.pass(host.pass(host.pass(v0, "aL"), "bLcL"), "cLa2");
  const right = host.pass(host.pass(host.pass(v0, "aR"), "bRcR"), "cRa2");

  assert.equal(host.domainOf(v0), "A");
  assert.equal(host.domainOf(left), "A");
  assert.ok(["a", "a2"].includes(host.policyNodeOf(left)));
  assert.equal(left, v0);
  assert.equal(right, v0);
  assert.equal(host.isSameIdentity(left, right), true);
  assert.equal(host.identityOf(left), host.identityOf(right));

  const b = host.pass(v0, "aL");
  const c = host.pass(b, "bLcL");
  const aAgain = host.pass(c, "cLa2");
  assert.equal(aAgain, v0);
  assert.equal(host.isSameIdentity(host.pass(v0, "aR"), b), true);
});

test("property and prototype child views share cache identity", () => {
  const host = scenario();
  const root = host.view("root");
  const child = root.child;

  assert.equal(root.child, child);
  assert.equal(host.view("child"), child);
  assert.equal(host.isSameIdentity(root.child, host.view("child")), true);

  assert.equal(child.n, 1);
  assert.equal(child.inherited, "from-base");
  assert.equal("inherited" in child, true);
  assert.deepEqual(Object.keys(child), ["n"]);

  const proto = Object.getPrototypeOf(child);
  assert.ok(proto);
  assert.equal(Object.getPrototypeOf(child), proto);
  assert.deepEqual(Object.keys(proto), ["inherited"]);
  assert.equal(proto.inherited, "from-base");
  assert.equal(host.nodeIdOf(proto), "base");
});

test("attenuation on a round trip never widens capabilities", () => {
  const host = scenario();
  const v0 = host.view("root");
  const readWrite = host.attenuate(v0, ["read", "write"]);
  assert.deepEqual(host.capabilitiesOf(readWrite).toArray(), ["read", "write"]);

  const attenuated = host.rewrap(readWrite, {
    capabilities: ["read", "write", "invoke", "construct", "enumerate"],
  });
  assert.deepEqual(host.capabilitiesOf(attenuated).toArray(), ["read", "write"]);

  const wrappedAgain = host.rewrap(attenuated, {
    capabilities: ["write"],
  });
  assert.deepEqual(host.capabilitiesOf(wrappedAgain).toArray(), ["write"]);
  assert.throws(() => wrappedAgain.name, (error) => {
    return error instanceof CapabilityError;
  });

  const noWrite = host.attenuate(v0, ["read", "enumerate"]);
  assert.throws(() => {
    (noWrite as { name?: unknown }).name = "changed";
  }, (error) => error instanceof CapabilityError);
});

test("an attenuating three-domain cycle creates a distinct, downgraded identity", () => {
  const graph = {
    nodes: {
      root: { properties: { name: { value: "root" } } },
    },
  };
  const policy = {
    entry: "a",
    nodes: {
      a: { domain: "A" },
      b: { domain: "B" },
      c: { domain: "C" },
      a2: { domain: "A" },
    },
    edges: [
      { id: "ab", from: "a", to: "b", action: "pass", attenuate: "write" },
      { id: "bc", from: "b", to: "c", action: "pass" },
      { id: "ca", from: "c", to: "a2", action: "pass" },
      { id: "a2b", from: "a2", to: "b", action: "pass" },
    ],
  };
  const host = createHost({ graph, policy });
  const v0 = host.view("root");
  const back = host.pass(host.pass(host.pass(v0, "ab"), "bc"), "ca");

  assert.notEqual(back, v0);
  assert.equal(host.isSameIdentity(back, v0), false);
  assert.equal(host.capabilitiesOf(back).has("write"), false);
  assert.equal(back.name, "root");
  assert.throws(() => {
    (back as { name?: unknown }).name = "x";
  }, (error) => error instanceof CapabilityError);

  const rewrap = host.rewrap(back, {
    capabilities: ["read", "write", "enumerate"],
  });
  assert.equal(host.capabilitiesOf(rewrap).has("write"), false);
});

test("writes remain symbolic and cannot introduce raw executable values", () => {
  const host = scenario();
  const root = host.view("root");
  root.name = "updated";
  assert.equal(root.name, "updated");
  assert.equal(host.view("root").name, "updated");

  const child = root.child;
  root.child = child;
  assert.equal(root.child, child);
  assert.equal(root.child.n, 1);

  root.fn = host.view("fn");
  assert.equal((root.fn as () => unknown)(), host.view("child"));

  assert.throws(() => {
    (root as { raw?: unknown }).raw = { not: "a view" };
  }, (error) => error instanceof InvalidWriteError);
  assert.throws(() => {
    (root as { raw?: unknown }).raw = () => 1;
  }, (error) => error instanceof InvalidWriteError);
});

test("symbolic function views invoke and construct without executing code", () => {
  const host = scenario();
  const fn = host.view("fn");
  assert.equal(typeof fn, "function");
  assert.equal((fn as () => unknown)(), host.view("child"));
  assert.equal((new (fn as new () => { n: number })()).n, 1);
  assert.equal(host.capabilitiesOf(fn).has("invoke"), true);
  assert.equal(host.capabilitiesOf(fn).has("construct"), true);
});
