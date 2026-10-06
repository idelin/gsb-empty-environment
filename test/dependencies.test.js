"use strict";
const assert = require("assert");
const { test } = require("./harness");
const { FlagService, ConfigError, createUser } = require("../dist");

test("dependency met -> normal evaluation; unmet -> fallback", () => {
  const svc = new FlagService([
    { id: "base", defaultValue: true },
    {
      id: "child",
      defaultValue: "on",
      fallbackValue: "off",
      overrides: { vip: "override-on" },
      dependsOn: [{ flagId: "base", equals: true }],
    },
    {
      id: "child-no-fallback",
      defaultValue: "on",
      dependsOn: [{ flagId: "base", equals: "nope" }],
    },
  ]);
  const user = createUser("u1");
  const met = svc.evaluate("child", user);
  assert.strictEqual(met.value, "on");
  assert.strictEqual(met.reason, "default");

  // override still applies once the dependency is met
  assert.strictEqual(svc.evaluate("child", createUser("vip")).value, "override-on");

  const unmet = svc.evaluate("child-no-fallback", user);
  assert.strictEqual(unmet.value, false, "fallback defaults to false");
  assert.strictEqual(unmet.reason, "dependency-unmet");
});

test("transitive dependencies chain within one snapshot", () => {
  const svc = new FlagService([
    { id: "a", defaultValue: false },
    { id: "b", defaultValue: true, fallbackValue: false, dependsOn: [{ flagId: "a", equals: true }] },
    { id: "c", defaultValue: "on", fallbackValue: "off", dependsOn: [{ flagId: "b", equals: true }] },
  ]);
  const user = createUser("u1");
  assert.strictEqual(svc.evaluate("b", user).value, false, "a is false -> b unmet");
  assert.strictEqual(svc.evaluate("c", user).value, "off", "b is false -> c unmet");

  svc.updateConfig([
    { id: "a", defaultValue: true },
    { id: "b", defaultValue: true, fallbackValue: false, dependsOn: [{ flagId: "a", equals: true }] },
    { id: "c", defaultValue: "on", fallbackValue: "off", dependsOn: [{ flagId: "b", equals: true }] },
  ]);
  assert.strictEqual(svc.evaluate("c", user).value, "on");
});

test("dependency cycle rejects the whole update atomically", () => {
  const svc = new FlagService([{ id: "ok", defaultValue: "v1" }]);
  assert.throws(
    () =>
      svc.updateConfig([
        { id: "a", defaultValue: 1, dependsOn: [{ flagId: "b", equals: 1 }] },
        { id: "b", defaultValue: 1, dependsOn: [{ flagId: "a", equals: 1 }] },
      ]),
    (err) => err instanceof ConfigError && /cycle/.test(err.message)
  );
  assert.deepStrictEqual(svc.listVersions(), [1], "failed update publishes nothing");
  assert.strictEqual(svc.evaluate("ok", createUser("u")).value, "v1");
});

test("self-dependency is a cycle", () => {
  const svc = new FlagService();
  assert.throws(
    () =>
      svc.updateConfig([
        { id: "a", defaultValue: 1, dependsOn: [{ flagId: "a", equals: 1 }] },
      ]),
    ConfigError
  );
  assert.deepStrictEqual(svc.listVersions(), []);
});

test("longer cycle is detected and reported", () => {
  const svc = new FlagService();
  assert.throws(
    () =>
      svc.updateConfig([
        { id: "a", defaultValue: 1, dependsOn: [{ flagId: "b", equals: 1 }] },
        { id: "b", defaultValue: 1, dependsOn: [{ flagId: "c", equals: 1 }] },
        { id: "c", defaultValue: 1, dependsOn: [{ flagId: "a", equals: 1 }] },
      ]),
    /dependency cycle detected: a -> b -> c -> a/
  );
});

test("dependency on an unknown flag rejects the update", () => {
  const svc = new FlagService([{ id: "ok", defaultValue: 1 }]);
  assert.throws(
    () =>
      svc.updateConfig([
        { id: "a", defaultValue: 1, dependsOn: [{ flagId: "ghost", equals: 1 }] },
      ]),
    (err) => err instanceof ConfigError && /unknown flag/.test(err.message)
  );
  assert.deepStrictEqual(svc.listVersions(), [1]);
});

test("invalid rollout parameters reject the update", () => {
  const svc = new FlagService();
  assert.throws(
    () => svc.updateConfig([{ id: "f", defaultValue: 1, rollout: { basisPoints: 10001 } }]),
    ConfigError
  );
  assert.throws(
    () => svc.updateConfig([{ id: "f", defaultValue: 1, rollout: { basisPoints: -1 } }]),
    ConfigError
  );
  assert.throws(
    () => svc.updateConfig([{ id: "f", defaultValue: 1, rollout: { basisPoints: 50.5 } }]),
    ConfigError
  );
  assert.deepStrictEqual(svc.listVersions(), []);
});

test("duplicate flag ids reject the update", () => {
  const svc = new FlagService();
  assert.throws(
    () =>
      svc.updateConfig([
        { id: "f", defaultValue: 1 },
        { id: "f", defaultValue: 2 },
      ]),
    ConfigError
  );
});
