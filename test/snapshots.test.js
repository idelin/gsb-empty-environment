"use strict";
const assert = require("assert");
const { test } = require("./harness");
const { FlagService, createUser } = require("../dist");

test("config update keeps old snapshots evaluable", () => {
  const svc = new FlagService([
    { id: "f", defaultValue: "on", fallbackValue: "off", rollout: { basisPoints: 10000 } },
  ]);
  const user = createUser("u1");
  assert.strictEqual(svc.evaluate("f", user).value, "on");

  svc.updateConfig([
    { id: "f", defaultValue: "on", fallbackValue: "off", rollout: { basisPoints: 0 } },
  ]);

  assert.deepStrictEqual(svc.listVersions(), [1, 2]);
  assert.strictEqual(svc.evaluate("f", user).value, "off", "current snapshot is v2");
  assert.strictEqual(svc.evaluate("f", user, { version: 1 }).value, "on", "v1 retained");
  assert.strictEqual(svc.getSnapshot(1).version, 1);
});

test("bucketing parameter changes only affect the new snapshot", () => {
  const flags = (salt, basisPoints) => [
    { id: "f", defaultValue: "on", fallbackValue: "off", rollout: { basisPoints, salt } },
  ];
  const svc = new FlagService(flags("salt-a", 5000));
  const users = Array.from({ length: 50 }, (_, i) => createUser(`u${i}`));
  const before = users.map((u) => svc.evaluate("f", u).value);

  svc.updateConfig(flags("salt-b", 5000)); // re-salted: different buckets
  const afterV2 = users.map((u) => svc.evaluate("f", u).value);
  const afterV1 = users.map((u) => svc.evaluate("f", u, { version: 1 }).value);

  assert.deepStrictEqual(afterV1, before, "old snapshot results are frozen");
  assert.notDeepStrictEqual(afterV2, before, "new salt re-buckets users");
});

test("snapshots are deeply immutable", () => {
  const svc = new FlagService([
    { id: "f", defaultValue: "on", groups: [{ id: "g", priority: 1, conditions: [], value: "x" }] },
  ]);
  const snapshot = svc.getSnapshot(1);
  assert.ok(Object.isFrozen(snapshot.flags));
  assert.ok(Object.isFrozen(snapshot.flags.f));
  assert.ok(Object.isFrozen(snapshot.flags.f.groups));
  assert.throws(() => {
    snapshot.flags.f.defaultValue = "hacked";
  }, TypeError);
});

test("dependencies resolve inside the same snapshot version", () => {
  const svc = new FlagService([
    { id: "base", defaultValue: false },
    { id: "child", defaultValue: "on", fallbackValue: "off", dependsOn: [{ flagId: "base", equals: true }] },
  ]);
  const user = createUser("u1");
  assert.strictEqual(svc.evaluate("child", user).value, "off");

  svc.updateConfig([
    { id: "base", defaultValue: true },
    { id: "child", defaultValue: "on", fallbackValue: "off", dependsOn: [{ flagId: "base", equals: true }] },
  ]);
  assert.strictEqual(svc.evaluate("child", user).value, "on", "met in v2");
  assert.strictEqual(
    svc.evaluate("child", user, { version: 1 }).value,
    "off",
    "v1 still evaluates base from v1"
  );
});
