"use strict";
const assert = require("assert");
const { test } = require("./harness");
const {
  FlagService,
  createUser,
  withAttributes,
  closureDigest,
  dependencyClosure,
} = require("../dist");

const FLAGS_V1 = [
  {
    id: "checkout",
    defaultValue: true,
    fallbackValue: false,
    rollout: { basisPoints: 6000, salt: "checkout-salt" },
  },
  {
    id: "recommend",
    defaultValue: "algo-v2",
    fallbackValue: "algo-stable",
    dependsOn: [{ flagId: "checkout", equals: true }],
    rollout: { basisPoints: 5000, salt: "recommend-salt" },
  },
  { id: "banner", defaultValue: "none" },
];

test("cache key embeds attributes version and dependency-closure digest", () => {
  const svc = new FlagService(FLAGS_V1);
  const user = createUser("u1", { tier: "gold" }, 3);
  const key = svc.cacheKeyFor("recommend", user);
  const parts = key.split("|");
  assert.strictEqual(parts[0], "v1");
  assert.strictEqual(parts[1], "recommend");
  assert.strictEqual(parts[2], "u1");
  assert.strictEqual(parts[3], "attrs@3");
  assert.strictEqual(parts[4], closureDigest(svc.getSnapshot(), "recommend"));
  assert.ok(/^[0-9a-f]{8}$/.test(parts[4]));
});

test("closure digest covers the transitive dependency subgraph only", () => {
  const svc = new FlagService(FLAGS_V1);
  const snap1 = svc.getSnapshot(1);
  assert.deepStrictEqual(dependencyClosure(snap1, "recommend"), ["checkout", "recommend"]);
  assert.deepStrictEqual(dependencyClosure(snap1, "checkout"), ["checkout"]);
  const digestBefore = closureDigest(snap1, "recommend");

  // Unrelated flag changes do not touch the digest.
  svc.updateConfig([...FLAGS_V1.slice(0, 2), { id: "banner", defaultValue: "promo" }]);
  assert.strictEqual(
    closureDigest(svc.getSnapshot(2), "recommend"),
    digestBefore,
    "unrelated flag change leaves the closure digest alone"
  );

  // A dependency's config change does.
  svc.updateConfig([
    { ...FLAGS_V1[0], rollout: { basisPoints: 100, salt: "checkout-salt" } },
    FLAGS_V1[1],
    FLAGS_V1[2],
  ]);
  assert.notStrictEqual(
    closureDigest(svc.getSnapshot(3), "recommend"),
    digestBefore,
    "dependency config change moves the digest"
  );
});

test("cached results follow the attributes version contract", () => {
  const svc = new FlagService([
    {
      id: "tier-flag",
      defaultValue: "default",
      groups: [
        {
          id: "g",
          priority: 1,
          conditions: [{ attr: "tier", op: "eq", value: "gold" }],
          value: "gold-value",
        },
      ],
    },
  ]);
  const user = createUser("u1", { tier: "free" });
  assert.strictEqual(svc.evaluate("tier-flag", user).value, "default");

  // Attributes changed but version not bumped -> cached (stale) result.
  user.attributes = { tier: "gold" };
  assert.strictEqual(svc.evaluate("tier-flag", user).value, "default");

  // Bump the version -> fresh evaluation.
  const bumped = withAttributes(createUser("u1", { tier: "free" }), { tier: "gold" });
  assert.strictEqual(svc.evaluate("tier-flag", bumped).value, "gold-value");
});

test("hot-switching rollout config: cached evaluation matches full evaluation", () => {
  const users = Array.from({ length: 200 }, (_, i) =>
    createUser(`user-${i}`, { region: i % 2 ? "us" : "eu" })
  );
  const svc = new FlagService(FLAGS_V1);

  // Warm the cache on v1.
  const before = users.map((u) => svc.evaluateAll(u));
  assert.ok(svc.cacheSize > 0);

  // Hot-switch: tighten checkout, widen recommend. Old snapshot stays put.
  svc.updateConfig([
    { ...FLAGS_V1[0], rollout: { basisPoints: 500, salt: "checkout-salt" } },
    { ...FLAGS_V1[1], rollout: { basisPoints: 9000, salt: "recommend-salt" } },
    FLAGS_V1[2],
  ]);
  assert.deepStrictEqual(svc.listVersions(), [1, 2]);

  // Full (uncached) evaluation is the ground truth for the new snapshot.
  for (let i = 0; i < users.length; i++) {
    const cached = svc.evaluateAll(users[i]);
    const full = svc.evaluateAll(users[i], { useCache: false });
    assert.deepStrictEqual(cached, full, `cached vs full mismatch for ${users[i].id}`);
    // Old snapshot is retained and still answers as before.
    const oldSnapshot = svc.evaluateAll(users[i], { version: 1 });
    assert.deepStrictEqual(oldSnapshot, before[i], `v1 changed for ${users[i].id}`);
  }

  // The switch actually took effect for somebody.
  const changed = users.filter(
    (u, i) => svc.evaluate("checkout", u).value !== before[i].checkout.value
  );
  assert.ok(changed.length > 0, "expected the tighter rollout to flip some users");
});

test("cache is bypassed with useCache:false and never poisoned", () => {
  const svc = new FlagService([{ id: "f", defaultValue: "a" }]);
  const user = createUser("u1");
  svc.evaluate("f", user, { useCache: false });
  assert.strictEqual(svc.cacheSize, 0, "uncached evaluation writes nothing");
  svc.evaluate("f", user);
  assert.strictEqual(svc.cacheSize, 1);
  svc.clearCache();
  assert.strictEqual(svc.cacheSize, 0);
});
