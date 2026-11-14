import {
  ConfigMergeError,
  createConfigSnapshot,
  mergeTenantConfig,
} from "./config-merge.ts";
import type { ConfigObject, MergeOptions } from "./config-merge.ts";

export const DEFAULT_TENANT_CONFIG: ConfigObject = createConfigSnapshot({
  authorization: {
    defaultRole: "user",
    allowedRoles: ["user", "support"],
  },
  features: {
    auditLog: true,
    betaFeatures: false,
  },
  limits: {
    maxItems: 100,
  },
});

/**
 * Holds one immutable merged snapshot per tenant.
 *
 * Reads never consult or merge against the mutable global default. A snapshot
 * is fully built and frozen before it replaces the previous tenant snapshot,
 * so a rejected patch cannot leave a partially applied configuration behind.
 */
export class TenantConfigStore {
  readonly #defaults: ConfigObject;
  readonly #options: MergeOptions;
  readonly #snapshots = new Map<string, ConfigObject>();

  constructor(
    defaults: unknown = DEFAULT_TENANT_CONFIG,
    options: MergeOptions = {},
  ) {
    this.#defaults = createConfigSnapshot(defaults, options);
    this.#options = options;
  }

  get defaults(): ConfigObject {
    return this.#defaults;
  }

  apply(tenantId: string, patch: unknown): ConfigObject {
    assertTenantId(tenantId);

    const snapshot = mergeTenantConfig(this.#defaults, patch, this.#options);
    this.#snapshots.set(tenantId, snapshot);
    return snapshot;
  }

  setTenantConfig(tenantId: string, patch: unknown): ConfigObject {
    return this.apply(tenantId, patch);
  }

  get(tenantId: string): ConfigObject {
    assertTenantId(tenantId);
    return this.#snapshots.get(tenantId) ?? this.#defaults;
  }

  getTenantConfig(tenantId: string): ConfigObject {
    return this.get(tenantId);
  }

  snapshot(tenantId: string): ConfigObject {
    return this.get(tenantId);
  }

  has(tenantId: string): boolean {
    assertTenantId(tenantId);
    return this.#snapshots.has(tenantId);
  }

  reset(tenantId: string): boolean {
    assertTenantId(tenantId);
    return this.#snapshots.delete(tenantId);
  }

  tenantIds(): readonly string[] {
    return Object.freeze([...this.#snapshots.keys()]);
  }
}

export function createTenantConfigStore(
  defaults: unknown = DEFAULT_TENANT_CONFIG,
  options: MergeOptions = {},
): TenantConfigStore {
  return new TenantConfigStore(defaults, options);
}

export function createTenantConfigService(
  defaults: unknown = DEFAULT_TENANT_CONFIG,
  options: MergeOptions = {},
): TenantConfigStore {
  return new TenantConfigStore(defaults, options);
}

function assertTenantId(tenantId: string): void {
  if (
    typeof tenantId !== "string" ||
    tenantId.length === 0 ||
    tenantId.length > 128 ||
    /[\u0000-\u001f\u007f]/.test(tenantId)
  ) {
    throw new ConfigMergeError(
      "INVALID_INPUT",
      "$tenantId",
      "Tenant id must be a non-empty string without control characters",
    );
  }
}
