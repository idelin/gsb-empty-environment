"use strict";
const assert = require("assert");
const { test } = require("./harness");
const {
  FlagService,
  createUser,
  bucketKey,
  bucketOf,
} = require("../dist");

test("explicit user override beats group rules", () => {
  const svc = new FlagService([
    {
      id: "theme",
      defaultValue: "off",
      overrides: { u1: "custom" },
      groups: [
        {
          id: "g1",
          priority: 1,
          conditions: [{ attr: "tier", op: "eq", value: "gold" }],
          value: "beta",
        },
      ],
    },
  ]);
  const overridden = svc.evaluate("theme", createUser("u1", { tier: "gold" }));
  assert.strictEqual(overridden.value, "custom");
  assert.strictEqual(overridden.reason, "override");

  const grouped = svc.evaluate("theme", createUser("u2", { tier: "gold" }));
  assert.strictEqual(grouped.value, "beta");
  assert.strictEqual(grouped.reason, "group");
  assert.strictEqual(grouped.groupId, "g1");

  const plain = svc.evaluate("theme", createUser("u3", { tier: "free" }));
  assert.strictEqual(plain.value, "off");
  assert.strictEqual(plain.reason, "default");
});

test("groups resolve by priority, then by ascending id", () => {
  const svc = new FlagService([
    {
      id: "f",
      defaultValue: "default",
      groups: [
        { id: "low", priority: 1, conditions: [], value: "low-value" },
        { id: "high", priority: 10, conditions: [], value: "high-value" },
      ],
    },
    {
      id: "tie",
      defaultValue: "default",
      groups: [
        { id: "g-b", priority: 5, conditions: [], value: "b-value" },
        { id: "g-a", priority: 5, conditions: [], value: "a-value" },
      ],
    },
  ]);
  const user = createUser("u");
  assert.strictEqual(svc.evaluate("f", user).value, "high-value");
  const tied = svc.evaluate("tie", user);
  assert.strictEqual(tied.value, "a-value");
  assert.strictEqual(tied.groupId, "g-a");
});

test("missing attribute is not the empty string", () => {
  const svc = new FlagService([
    {
      id: "f",
      defaultValue: "default",
      groups: [
        {
          id: "empty-tier",
          priority: 1,
          conditions: [{ attr: "tier", op: "eq", value: "" }],
          value: "matched",
        },
      ],
    },
    {
      id: "neq",
      defaultValue: "default",
      groups: [
        {
          id: "nonempty-tier",
          priority: 1,
          conditions: [{ attr: "tier", op: "neq", value: "" }],
          value: "matched",
        },
      ],
    },
  ]);
  const missing = createUser("u1", {}); // no "tier" at all
  const empty = createUser("u2", { tier: "" });
  const filled = createUser("u3", { tier: "gold" });

  assert.strictEqual(svc.evaluate("f", missing).value, "default");
  assert.strictEqual(svc.evaluate("f", empty).value, "matched");
  assert.strictEqual(svc.evaluate("f", filled).value, "default");

  assert.strictEqual(svc.evaluate("neq", missing).value, "default");
  assert.strictEqual(svc.evaluate("neq", empty).value, "default");
  assert.strictEqual(svc.evaluate("neq", filled).value, "matched");
});

test("exists / notExists look only at presence", () => {
  const svc = new FlagService([
    {
      id: "f",
      defaultValue: "default",
      groups: [
        {
          id: "has-tier",
          priority: 2,
          conditions: [{ attr: "tier", op: "exists" }],
          value: "present",
        },
        {
          id: "no-tier",
          priority: 1,
          conditions: [{ attr: "tier", op: "notExists" }],
          value: "absent",
        },
      ],
    },
  ]);
  assert.strictEqual(svc.evaluate("f", createUser("u1", {})).value, "absent");
  assert.strictEqual(svc.evaluate("f", createUser("u2", { tier: "" })).value, "present");
});

test("rollout threshold boundary: bucket < basisPoints", () => {
  const salt = "s1";
  const flagId = "gate";
  const userId = "user-42";
  const bucket = bucketOf(bucketKey([salt, flagId, userId]));

  const make = (basisPoints) =>
    new FlagService([
      {
        id: flagId,
        defaultValue: "on",
        fallbackValue: "off",
        rollout: { basisPoints, salt },
      },
    ]);
  const user = createUser(userId);

  const excluded = make(bucket).evaluate(flagId, user);
  assert.strictEqual(excluded.value, "off", "bucket == threshold is excluded");
  assert.strictEqual(excluded.reason, "fallback");
  assert.strictEqual(excluded.bucket, bucket);

  const included = make(bucket + 1).evaluate(flagId, user);
  assert.strictEqual(included.value, "on", "bucket == threshold - 1 is included");
  assert.strictEqual(included.reason, "default");
  assert.strictEqual(included.bucket, bucket);
});

test("rollout 0bp serves nobody, 10000bp serves everybody", () => {
  const make = (basisPoints) =>
    new FlagService([
      { id: "f", defaultValue: "on", fallbackValue: "off", rollout: { basisPoints } },
    ]);
  const nobody = make(0);
  const everybody = make(10000);
  for (let i = 0; i < 200; i++) {
    const user = createUser(`u${i}`);
    assert.strictEqual(nobody.evaluate("f", user).value, "off");
    assert.strictEqual(everybody.evaluate("f", user).value, "on");
  }
});

test("group-level rollout gates the group only", () => {
  const salt = "gs";
  const userId = "user-7";
  const bucket = bucketOf(bucketKey([salt, "f", "g1", userId]));
  const svc = new FlagService([
    {
      id: "f",
      defaultValue: "default",
      groups: [
        {
          id: "g1",
          priority: 1,
          conditions: [],
          value: "group-value",
          rollout: { basisPoints: bucket, salt }, // excludes exactly this user
        },
      ],
    },
  ]);
  const result = svc.evaluate("f", createUser(userId));
  assert.strictEqual(result.value, "default");
  assert.strictEqual(result.reason, "default");
});

test("condition operators", () => {
  const attrs = { age: 30, plan: "pro", tags: "a,b,c" };
  const { matchesCondition } = require("../dist");
  assert.strictEqual(matchesCondition({ attr: "age", op: "gt", value: 18 }, attrs), true);
  assert.strictEqual(matchesCondition({ attr: "age", op: "lt", value: 18 }, attrs), false);
  assert.strictEqual(matchesCondition({ attr: "age", op: "gte", value: 30 }, attrs), true);
  assert.strictEqual(matchesCondition({ attr: "age", op: "lte", value: 30 }, attrs), true);
  assert.strictEqual(matchesCondition({ attr: "plan", op: "in", value: ["pro", "team"] }, attrs), true);
  assert.strictEqual(matchesCondition({ attr: "plan", op: "in", value: ["free"] }, attrs), false);
  assert.strictEqual(matchesCondition({ attr: "tags", op: "contains", value: "b" }, attrs), true);
  // type mismatches never match
  assert.strictEqual(matchesCondition({ attr: "plan", op: "gt", value: 1 }, attrs), false);
});
