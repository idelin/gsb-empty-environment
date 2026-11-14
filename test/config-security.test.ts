import assert from "node:assert/strict";
import test from "node:test";

import {
  ConfigMergeError,
  DEFAULT_TENANT_CONFIG,
  TenantConfigStore,
  createTenantConfigStore,
  mergeTenantConfig,
} from "../index.ts";

const defaults = {
  authorization: {
    defaultRole: "user",
    allowedRoles: ["user", "support"],
  },
  features: {
    auditLog: true,
    betaFeatures: false,
    tags: ["base"],
    rules: [{ name: "base", enabled: true }],
  },
  limits: {
    maxItems: 100,
  },
};

function roleOf(config: { authorization: { defaultRole: unknown } }): unknown {
  return config.authorization.defaultRole;
}

function assertNoPrototypePollution(): void {
  const polluted = {} as {
    defaultRole?: unknown;
    authorization?: unknown;
    isAdmin?: unknown;
    polluted?: unknown;
  };
  const prototype = Object.prototype as Record<string, unknown>;

  assert.equal(polluted.defaultRole, undefined);
  assert.equal(polluted.authorization, undefined);
  assert.equal(polluted.isAdmin, undefined);
  assert.equal(polluted.polluted, undefined);
  assert.equal(prototype.defaultRole, undefined);
  assert.equal(prototype.isAdmin, undefined);
}

test("__proto__ payloads are rejected and do not pollute Object.prototype", () => {
  assert.equal(roleOf(defaults), "user");

  const payload = JSON.parse(
    '{"__proto__":{"authorization":{"defaultRole":"admin"},"isAdmin":true}}',
  );

  assert.throws(
    () => mergeTenantConfig(defaults, payload),
    (error: unknown) =>
      error instanceof ConfigMergeError &&
      error.code === "FORBIDDEN_KEY" &&
      error.path === "$.__proto__",
  );
  assert.equal(roleOf(defaults), "user");
  assertNoPrototypePollution();
});

test("nested __proto__ keys are rejected at any depth", () => {
  const payload = JSON.parse(
    '{"features":{"rules":[{"__proto__":{"isAdmin":true}}]}}',
  );

  assert.throws(
    () => mergeTenantConfig(defaults, payload),
    (error: unknown) =>
      error instanceof ConfigMergeError &&
      error.code === "FORBIDDEN_KEY" &&
      error.path === "$.features.rules[0].__proto__",
  );
  assertNoPrototypePollution();
});

test("constructor and prototype chains cannot reach Object.prototype", () => {
  const payload = JSON.parse(
    '{"constructor":{"prototype":{"defaultRole":"admin","isAdmin":true}}}',
  );

  assert.throws(
    () => mergeTenantConfig(defaults, payload),
    (error: unknown) =>
      error instanceof ConfigMergeError &&
      error.code === "FORBIDDEN_KEY" &&
      error.path === "$.constructor",
  );
  assertNoPrototypePollution();

  const nested = JSON.parse(
    '{"limits":{"constructor":{"prototype":{"maxItems":99999}}}}',
  );
  assert.throws(
    () => mergeTenantConfig(defaults, nested),
    (error: unknown) =>
      error instanceof ConfigMergeError &&
      error.code === "FORBIDDEN_KEY" &&
      error.path === "$.limits.constructor",
  );
  assertNoPrototypePollution();
});

test("dot-path and unknown keys are rejected by the whitelist", () => {
  assert.throws(
    () =>
      mergeTenantConfig(defaults, {
        "authorization.defaultRole": "admin",
      }),
    (error: unknown) =>
      error instanceof ConfigMergeError &&
      error.code === "FORBIDDEN_KEY" &&
      error.path === "$.authorization.defaultRole",
  );

  assert.throws(
    () =>
      mergeTenantConfig(defaults, {
        unknownSection: { defaultRole: "admin" },
      }),
    (error: unknown) =>
      error instanceof ConfigMergeError &&
      error.code === "UNKNOWN_KEY" &&
      error.path === "$.unknownSection",
  );
});

test("deeply nested same-name keys stay in their own path", () => {
  const repository = {
    tenant: {
      authorization: { defaultRole: "user" },
    },
    admin: {
      authorization: { defaultRole: "user" },
    },
  };

  const merged = mergeTenantConfig(repository, {
    tenant: {
      authorization: { defaultRole: "tenant-operator" },
    },
  });
  const tenant = merged.tenant as {
    authorization: { defaultRole: string };
  };
  const admin = merged.admin as {
    authorization: { defaultRole: string };
  };

  assert.equal(tenant.authorization.defaultRole, "tenant-operator");
  assert.equal(admin.authorization.defaultRole, "user");
  assert.equal(repository.admin.authorization.defaultRole, "user");
});

test("arrays replace atomically instead of mixing recursively", () => {
  const merged = mergeTenantConfig(defaults, {
    authorization: { allowedRoles: ["user", "tenant-admin"] },
    features: {
      tags: ["tenant"],
      rules: [{ name: "tenant", enabled: false }],
    },
  });
  const authorization = merged.authorization as {
    allowedRoles: string[];
  };
  const features = merged.features as {
    tags: string[];
    rules: Array<{ name: string; enabled: boolean }>;
  };

  assert.deepEqual(authorization.allowedRoles, ["user", "tenant-admin"]);
  assert.deepEqual(features.tags, ["tenant"]);
  assert.deepEqual(
    features.rules.map((rule) => ({ ...rule })),
    [{ name: "tenant", enabled: false }],
  );
  assert.deepEqual(Object.keys(features.rules[0]), ["name", "enabled"]);
  assert.equal(Object.getPrototypeOf(features.rules[0]), null);
});

test("legitimate overrides affect one result without changing defaults", () => {
  const merged = mergeTenantConfig(defaults, {
    authorization: { defaultRole: "support" },
    features: { betaFeatures: true },
    limits: { maxItems: 250 },
  });
  const features = merged.features as { betaFeatures: boolean };
  const limits = merged.limits as { maxItems: number };

  assert.equal(roleOf(merged), "support");
  assert.equal(features.betaFeatures, true);
  assert.equal(limits.maxItems, 250);
  assert.equal(roleOf(defaults), "user");
  assert.equal(defaults.features.betaFeatures, false);
  assert.equal(defaults.limits.maxItems, 100);
});

test("results are recursively frozen null-prototype objects", () => {
  const merged = mergeTenantConfig(defaults, {
    features: { tags: ["one", "two"] },
  });
  const authorization = merged.authorization as { defaultRole: string };
  const features = merged.features as {
    tags: string[];
    rules: Array<Record<string, unknown>>;
  };

  assert.equal(Object.getPrototypeOf(merged), null);
  assert.equal(Object.isFrozen(merged), true);
  assert.equal(Object.getPrototypeOf(authorization), null);
  assert.equal(Object.isFrozen(authorization), true);
  assert.equal(Object.getPrototypeOf(features.rules), Array.prototype);
  assert.equal(Object.isFrozen(features.rules), true);
  assert.equal(Object.getPrototypeOf(features.rules[0]), null);
  assert.equal(Object.isFrozen(features.rules[0]), true);
  assert.equal(Object.isFrozen(features.tags), true);

  assert.throws(() => {
    authorization.defaultRole = "admin";
  }, TypeError);
  assert.equal(roleOf(merged), "user");
});

test("depth, node, and size limits reject oversized input", () => {
  assert.throws(
    () =>
      mergeTenantConfig(defaults, {}, { limits: { maxDepth: 1 } }),
    (error: unknown) =>
      error instanceof ConfigMergeError && error.code === "DEPTH_LIMIT",
  );

  assert.throws(
    () => mergeTenantConfig(defaults, {}, { maxNodes: 3 }),
    (error: unknown) =>
      error instanceof ConfigMergeError && error.code === "NODE_LIMIT",
  );

  assert.throws(
    () => mergeTenantConfig(defaults, {}, { maxBytes: 20 }),
    (error: unknown) =>
      error instanceof ConfigMergeError && error.code === "SIZE_LIMIT",
  );
});

test("only own data properties are read", () => {
  let getterRead = false;
  const accessorPatch = Object.defineProperty({}, "authorization", {
    enumerable: true,
    get() {
      getterRead = true;
      return { defaultRole: "admin" };
    },
  });

  assert.throws(
    () => mergeTenantConfig(defaults, accessorPatch),
    (error: unknown) =>
      error instanceof ConfigMergeError && error.code === "INVALID_VALUE",
  );
  assert.equal(getterRead, false);

  const inherited = Object.create(defaults);
  inherited.features = { betaFeatures: true };
  const merged = mergeTenantConfig(defaults, inherited);

  assert.equal(
    (merged.authorization as { defaultRole: string }).defaultRole,
    "user",
  );
  assert.equal(
    (merged.features as { betaFeatures: boolean }).betaFeatures,
    true,
  );
});

test("tenant reads use immutable tenant snapshots, not the global default", () => {
  const store = createTenantConfigStore(defaults);

  const tenantSnapshot = store.setTenantConfig("tenant-a", {
    authorization: { defaultRole: "tenant-operator" },
  });
  store.setTenantConfig("tenant-a", {
    authorization: { defaultRole: "tenant-auditor" },
  });
  const tenantAuthorization = tenantSnapshot.authorization as {
    defaultRole: string;
  };
  const tenantA = store.getTenantConfig("tenant-a").authorization as {
    defaultRole: string;
  };
  const tenantB = store.getTenantConfig("tenant-b").authorization as {
    defaultRole: string;
  };
  const globalAuthorization = DEFAULT_TENANT_CONFIG.authorization as {
    defaultRole: string;
  };

  assert.equal(tenantAuthorization.defaultRole, "tenant-operator");
  assert.equal(tenantA.defaultRole, "tenant-auditor");
  assert.equal(tenantB.defaultRole, "user");
  assert.equal(globalAuthorization.defaultRole, "user");

  assert.throws(() => {
    tenantB.defaultRole = "admin";
  }, TypeError);
  assert.equal(
    (
      store.getTenantConfig("tenant-b").authorization as {
        defaultRole: string;
      }
    ).defaultRole,
    "user",
  );
});

test("failed updates preserve the previous tenant snapshot", () => {
  const store = new TenantConfigStore(defaults);
  store.setTenantConfig("tenant-a", {
    authorization: { defaultRole: "support" },
  });

  assert.throws(
    () =>
      store.setTenantConfig(
        "tenant-a",
        JSON.parse('{"__proto__":{"authorization":{"defaultRole":"admin"}}}'),
      ),
    ConfigMergeError,
  );

  assert.equal(
    (
      store.getTenantConfig("tenant-a").authorization as {
        defaultRole: string;
      }
    ).defaultRole,
    "support",
  );
  assertNoPrototypePollution();
});

test("concurrent tenant updates produce isolated consistent snapshots", async () => {
  const store = createTenantConfigStore(defaults);
  const tenantCount = 40;

  const snapshots = await Promise.all(
    Array.from({ length: tenantCount }, async (_, index) => {
      const tenantId = `tenant-${index}`;
      const role = `role-${index}`;
      store.setTenantConfig(tenantId, {
        authorization: { defaultRole: role },
        features: { tags: [role] },
      });
      await Promise.resolve();
      return { tenantId, role, snapshot: store.getTenantConfig(tenantId) };
    }),
  );

  for (const { tenantId, role, snapshot } of snapshots) {
    const authorization = snapshot.authorization as { defaultRole: string };
    const tags = (
      store.getTenantConfig(tenantId).features as { tags: string[] }
    ).tags;

    assert.equal(authorization.defaultRole, role);
    assert.equal(tags[0], role);
  }

  assert.equal(
    (store.defaults.authorization as { defaultRole: string }).defaultRole,
    "user",
  );
  assert.equal(store.has("tenant-0"), true);
  assert.equal(store.has("missing"), false);
  assertNoPrototypePollution();
});
