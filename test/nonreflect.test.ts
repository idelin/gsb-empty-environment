import { test } from "node:test";
import assert from "node:assert/strict";

import {
  InvalidWriteError,
  InvalidDescriptionError,
  PolicyError,
  SymbolicOperationError,
  UnsafeDescriptionError,
  createHost,
} from "../src/index.ts";

test("description graphs reject carried functions without executing them", () => {
  let executed = false;
  const hostile = () => {
    executed = true;
    return 1;
  };
  const graph = {
    nodes: {
      root: {
        properties: {
          value: { value: 1 },
          fn: { value: hostile },
        },
      },
    },
  };
  assert.throws(() => createHost({ graph }), (error) => {
    return error instanceof UnsafeDescriptionError;
  });
  assert.equal(executed, false);
});

test("accessors are rejected without invoking their getter or setter", () => {
  let touched = false;
  const properties: Record<string, unknown> = {};
  Object.defineProperty(properties, "secret", {
    enumerable: true,
    configurable: true,
    get() {
      touched = true;
      return "secret";
    },
  });
  const graph = {
    nodes: {
      root: { properties },
    },
  };
  assert.throws(() => createHost({ graph }), (error) => {
    return error instanceof UnsafeDescriptionError;
  });
  assert.equal(touched, false);
});

test("prototype cycles are rejected rather than producing reflective loops", () => {
  assert.throws(
    () =>
      createHost({
        graph: {
          nodes: {
            root: { prototype: "root", properties: { x: { value: 1 } } },
          },
        },
      }),
    (error) => error instanceof InvalidDescriptionError,
  );
});

test("function descriptors are symbolic data, not executable callbacks", () => {
  let executed = false;
  const graph = {
    nodes: {
      root: { kind: "object" },
      result: { properties: { ok: { value: true } } },
      fn: {
        kind: "function",
        invoke: { ref: "result" },
        construct: { ref: "result" },
      },
      unsafe: {
        kind: "function",
        invoke: () => {
          executed = true;
          return 1;
        },
      },
    },
  };
  assert.throws(() => createHost({ graph }), (error) => {
    return error instanceof UnsafeDescriptionError;
  });
  assert.equal(executed, false);

  const safeGraph = {
    nodes: {
      root: { kind: "object" },
      result: { properties: { ok: { value: true } } },
      fn: {
        kind: "function",
        invoke: { ref: "result" },
        construct: { ref: "result" },
      },
    },
  };
  const host = createHost({ graph: safeGraph });
  const fn = host.view("fn") as () => { ok: boolean };
  assert.equal(fn().ok, true);
  assert.equal(new (fn as unknown as new () => { ok: boolean })().ok, true);
  assert.equal((host.invoke(host.view("fn")) as { ok: boolean }).ok, true);
  assert.equal(
    (host.construct(host.view("fn")) as { ok: boolean }).ok,
    true,
  );
  assert.equal(executed, false);
});

test("proxy reflection does not expose a raw target or host functions", async () => {
  const graph = {
    nodes: {
      root: {
        prototype: "base",
        properties: {
          name: { value: "root" },
          child: { ref: "child" },
        },
      },
      base: { properties: { inherited: { value: 1 } } },
      child: { properties: { x: { value: 2 } } },
    },
  };
  const host = createHost({ graph });
  const view = host.view("root") as Record<string, unknown>;

  assert.deepEqual(Reflect.ownKeys(view), ["child", "name"]);
  assert.deepEqual(Object.getOwnPropertySymbols(view), []);
  assert.equal(view.constructor, undefined);
  assert.equal(view.__proto__, undefined);
  assert.equal(view.then, undefined);
  assert.equal(view[Symbol.toPrimitive], undefined);
  assert.equal(view[Symbol.iterator], undefined);
  assert.equal((view as { target?: unknown }).target, undefined);

  const proto = Object.getPrototypeOf(view);
  assert.ok(proto);
  assert.notEqual(proto, view);
  assert.equal((proto as Record<string, unknown>).inherited, 1);

  assert.equal(await view, view);
  const descriptor = Object.getOwnPropertyDescriptor(view, "child");
  assert.ok(descriptor);
  assert.notEqual(descriptor.value, undefined);
  assert.equal(host.nodeIdOf(descriptor.value), "child");
});

test("wrapper layering preserves lease enforcement instead of widening a view", () => {
  const graph = {
    nodes: {
      root: { properties: { name: { value: "root" } } },
    },
  };
  const host = createHost({ graph });
  const view = host.view("root");
  const readOnly = host.attenuate(view, ["read"]);
  assert.equal(host.capabilitiesOf(readOnly).has("write"), false);

  const outer = new Proxy(readOnly, {});
  assert.equal(host.isView(outer), true);
  assert.equal(host.isSameIdentity(outer, readOnly), true);
  const rewrapped = host.rewrap(outer, {
    capabilities: ["read", "write", "enumerate"],
  });
  assert.equal(host.capabilitiesOf(rewrapped).has("write"), false);
  assert.equal(host.isSameIdentity(rewrapped, readOnly), true);

  assert.equal(host.isView(new Proxy({}, {})), false);
  assert.throws(() => host.pass(outer, "missing"), (error) => {
    return error instanceof PolicyError;
  });
  assert.throws(() => host.pass(new Proxy({}, {}), "missing"), (error) => {
    return error instanceof PolicyError;
  });
});

test("raw executable values cannot be written through a proxy", () => {
  const graph = {
    nodes: {
      root: { properties: { name: { value: "root" } } },
    },
  };
  const host = createHost({ graph });
  const view = host.view("root");
  assert.throws(() => {
    (view as { cb?: unknown }).cb = () => 1;
  }, (error) => error instanceof InvalidWriteError);
  assert.throws(() => {
    (view as { obj?: unknown }).obj = { hostile: true };
  }, (error) => error instanceof InvalidWriteError);
  assert.equal("cb" in view, false);
});

test("missing symbolic operation descriptors fail without falling back to code", () => {
  const host = createHost({
    graph: {
      nodes: {
        fn: { kind: "function" },
      },
    },
  });
  const fn = host.view("fn") as () => unknown;
  assert.throws(() => fn(), (error) => error instanceof SymbolicOperationError);
  assert.throws(() => new (fn as unknown as new () => unknown)(), (error) => {
    return error instanceof SymbolicOperationError;
  });
});
