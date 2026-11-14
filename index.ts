export {
  ConfigMergeError,
  DEFAULT_MERGE_LIMITS,
  assertSafeKey,
  createConfigSnapshot,
  mergeConfig,
  mergeTenantConfig,
} from "./src/config-merge.ts";

export type {
  ConfigErrorCode,
  ConfigObject,
  ConfigPrimitive,
  ConfigValue,
  KeyAllowlist,
  MergeLimits,
  MergeOptions,
} from "./src/config-merge.ts";

export {
  DEFAULT_TENANT_CONFIG,
  TenantConfigStore,
  createTenantConfigStore,
  createTenantConfigService,
} from "./src/tenant-config-store.ts";
