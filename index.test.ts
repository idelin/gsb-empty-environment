import assert from "node:assert/strict";
import { test } from "node:test";

import {
  getRegisteredMutableRecords,
  lockBuiltins,
  registerMutableRecord
} from "./index.ts";

test("freezes the standard built-in graph and reports covered paths", () => {
  const coverage = lockBuiltins();

  assert.ok(Array.isArray(coverage));
  assert.equal(coverage.coveredPaths, coverage);
  assert.equal(coverage.complete, true);
  assert.equal(coverage.failures.length, 0);

  for (const path of [
    "globalThis.Object",
    "globalThis.Object.prototype",
    "globalThis.Object.prototype.__proto__",
    "globalThis.Array",
    "globalThis.Array.prototype",
    "globalThis.Array.prototype[Symbol.iterator]",
    "globalThis.Array.prototype.constructor.prototype",
    "globalThis.Function",
    "globalThis.Function.prototype",
    "globalThis.String.prototype[Symbol.iterator]"
  ]) {
    assert.equal(coverage.includes(path), true, path);
  }

  assert.equal(Object.isFrozen(Object.prototype), true);
  assert.equal(Object.isFrozen(Array.prototype), true);
  assert.equal(Object.isFrozen(Function.prototype), true);

  const descriptor = Object.getOwnPropertyDescriptor(Object.prototype, "__proto__");
  assert.ok(descriptor);
  assert.equal(Object.isFrozen(descriptor.get), true);
  assert.equal(Object.isFrozen(descriptor.set), true);
});

test("strict-mode writes cannot add default properties", () => {
  lockBuiltins();

  for (const target of [Object.prototype, Array.prototype, Function.prototype]) {
    assert.throws(
      () => {
        (target as Record<string, unknown>).prototypeLockProbe = true;
      },
      TypeError
    );
    assert.equal("prototypeLockProbe" in target, false);
  }

  assert.throws(
    () => {
      (Array.prototype as unknown as Record<PropertyKey, unknown>)[Symbol.for("prototypeLockProbe")] = true;
    },
    TypeError
  );
});

test("setPrototypeOf cannot replace built-in prototype links", () => {
  lockBuiltins();

  for (const target of [Object.prototype, Array.prototype, Function.prototype]) {
    const original = Object.getPrototypeOf(target);
    assert.throws(() => Object.setPrototypeOf(target, { polluted: true }), TypeError);
    assert.equal(Object.getPrototypeOf(target), original);
  }
});

test("JSON __proto__ merge cannot mutate built-in prototypes", () => {
  lockBuiltins();
  const malicious = JSON.parse('{"__proto__":{"jsonLockProbe":true}}');

  for (const target of [Object.prototype, Array.prototype, Function.prototype]) {
    const original = Object.getPrototypeOf(target);
    assert.throws(() => Object.assign(target, malicious), TypeError);
    assert.equal(Object.getPrototypeOf(target), original);
    assert.equal((Object.prototype as Record<string, unknown>).jsonLockProbe, undefined);
    assert.equal((Array.prototype as Record<string, unknown>).jsonLockProbe, undefined);
    assert.equal((Function.prototype as Record<string, unknown>).jsonLockProbe, undefined);
  }
});

test("frozen Error prototypes still permit instance-owned names", () => {
  lockBuiltins();

  assert.throws(() => {
    Error.prototype.name = "ChangedError";
  }, TypeError);
  assert.equal(Error.prototype.name, "Error");

  class HostError extends Error {
    constructor(message: string) {
      super(message);
      this.name = "HostError";
    }
  }

  const error = new HostError("host failure");
  assert.equal(error.name, "HostError");
  assert.equal(Object.hasOwn(error, "name"), true);
});

test("host mutable records and paths remain mutable exceptions", () => {
  const record = registerMutableRecord({ count: 0, nested: { values: [] as number[] } });
  const pathRecord = { count: 0 };
  (globalThis as Record<string, unknown>).pathRecord = pathRecord;

  const coverage = lockBuiltins({
    roots: [record, "globalThis.pathRecord"],
    exceptionPaths: ["globalThis.<root>", "globalThis.pathRecord"]
  });

  record.count += 1;
  record.nested.values.push(1);
  pathRecord.count += 1;
  (pathRecord as Record<string, unknown>).extra = "mutable";

  assert.equal(Object.isFrozen(record), false);
  assert.equal(Object.isFrozen(record.nested), false);
  assert.equal(Object.isFrozen(pathRecord), false);
  assert.equal(coverage.skippedPaths.includes("globalThis.<root>"), true);
  assert.equal(coverage.skippedPaths.includes("globalThis.pathRecord"), true);
  assert.equal(getRegisteredMutableRecords().includes(record), true);
});

test("global host records are reported as skipped exceptions", () => {
  const record = { count: 0 };
  (globalThis as Record<string, unknown>).globalHostRecord = record;
  const coverage = lockBuiltins({ mutableRecords: [record] });

  record.count += 1;
  assert.equal(Object.isFrozen(record), false);
  assert.equal(coverage.skippedPaths.includes("globalThis.globalHostRecord"), true);

  delete (globalThis as Record<string, unknown>).globalHostRecord;
});

test("custom roots include symbols, accessors, and function prototypes", () => {
  const symbolKey = Symbol.for("lockTest.symbol");
  let getterCalls = 0;
  const nested = { value: 1 };
  const root = {};

  Object.defineProperty(root, "nested", {
    enumerable: true,
    configurable: true,
    get() {
      getterCalls += 1;
      return nested;
    }
  });
  Object.defineProperty(root, symbolKey, {
    enumerable: true,
    configurable: true,
    value: function LockTestConstructor() {},
    writable: true
  });

  const coverage = lockBuiltins({ roots: [root] });
  const symbolPath = 'globalThis.<root>[Symbol.for("lockTest.symbol")]';

  assert.equal(coverage.includes("globalThis.<root>.nested"), true);
  assert.equal(coverage.includes("globalThis.<root>.nested.<get>"), true);
  assert.equal(coverage.includes(symbolPath), true);
  assert.equal(coverage.includes(`${symbolPath}.prototype`), true);
  assert.equal(getterCalls, 0);
  assert.equal(Object.isFrozen(nested), false);
});
