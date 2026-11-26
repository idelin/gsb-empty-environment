import { test } from "node:test";
import assert from "node:assert/strict";

import {
  CapabilityError,
  createHost,
} from "../src/index.ts";

test("property requires, caps and prototype links attenuate without leaking", () => {
  const graph = {
    nodes: {
      root: {
        prototype: { ref: "base", requires: ["read", "write"] },
        properties: {
          visible: { value: 1 },
          elevated: { value: 2, requires: "write" },
          child: { ref: "child", caps: ["read", "enumerate"] },
          restricted: { ref: "child", caps: ["read"] },
        },
      },
      base: {
        properties: {
          inherited: { value: 3 },
          inheritedChild: { ref: "child", caps: ["read"] },
        },
      },
      child: { properties: { leaf: { value: 4 } } },
    },
  };
  const host = createHost({ graph });

  const readOnly = host.view("root", { capabilities: ["read", "enumerate"] });
  assert.deepEqual(Object.keys(readOnly), ["child", "restricted", "visible"]);
  assert.equal(readOnly.elevated, undefined);
  assert.equal("elevated" in readOnly, false);
  assert.equal("inherited" in readOnly, false);
  assert.equal(Object.getPrototypeOf(readOnly), null);

  const child = readOnly.child;
  assert.deepEqual(host.capabilitiesOf(child).toArray(), ["read", "enumerate"]);
  assert.equal(child.leaf, 4);

  const enumOnly = host.view("root", { capabilities: ["enumerate"] });
  assert.equal("visible" in enumOnly, true);
  assert.equal("elevated" in enumOnly, false);
  assert.throws(() => (enumOnly as { visible?: unknown }).visible, (error) => {
    return error instanceof CapabilityError;
  });

  const full = host.view("root");
  assert.deepEqual(Object.keys(full), [
    "child",
    "elevated",
    "restricted",
    "visible",
  ]);
  assert.equal(full.elevated, 2);
  assert.equal(full.inherited, 3);

  full.restricted = host.view("child");
  assert.deepEqual(host.capabilitiesOf(full.restricted).toArray(), ["read"]);
  Object.defineProperty(full, "restricted", { value: host.view("child") });
  assert.deepEqual(host.capabilitiesOf(full.restricted).toArray(), ["read"]);
  Object.defineProperty(full, "definedHidden", { value: 9 });
  assert.equal(full.definedHidden, 9);
  assert.equal(Object.keys(full).includes("definedHidden"), false);
  assert.equal(Reflect.ownKeys(full).includes("definedHidden"), true);

  full.inheritedChild = host.view("child");
  assert.deepEqual(host.capabilitiesOf(full.inheritedChild).toArray(), ["read"]);
});

test("node and edge capability limits are enforced for invoke", () => {
  const host = createHost({
    graph: {
      nodes: {
        result: { properties: { ok: { value: true } } },
        fn: { kind: "function", invoke: { ref: "result" } },
      },
    },
    policy: {
      entry: "a",
      nodes: {
        a: { domain: "A" },
        b: { domain: "B", capabilities: ["read", "enumerate"] },
        c: { domain: "C" },
      },
      edges: [
        { id: "ab", from: "a", to: "b", attenuate: "invoke" },
        { id: "bc", from: "b", to: "c", attenuate: "invoke" },
      ],
    },
  });
  const fn = host.view("fn");
  assert.equal((fn as () => { ok: boolean })().ok, true);

  const inB = host.pass(fn, "ab");
  assert.deepEqual(host.capabilitiesOf(inB).toArray(), ["read", "enumerate"]);
  assert.throws(() => (inB as () => unknown)(), (error) => {
    return error instanceof CapabilityError;
  });

  const inC = host.pass(inB, "bc");
  assert.equal(host.capabilitiesOf(inC).has("invoke"), false);
  assert.throws(() => (inC as () => unknown)(), (error) => {
    return error instanceof CapabilityError;
  });
});
