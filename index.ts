export {
  FORBIDDEN_PACKAGE_PREFIXES,
  isForbiddenClass,
  isHostClassAllowed,
  matchesAllowList,
  resolveHostClassName,
  validateAllowListEntry,
} from './src/names.ts';

export {
  assertContextAllowed,
  checkHostClassQuery,
  PermissionDeniedError,
  validateContextPermissions,
  type ContextOptions,
  type PermissionDecision,
  type PreflightResult,
  type ResourceLimits,
} from './src/context.ts';
