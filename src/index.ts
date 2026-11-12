export {
  RegistrationSecurityError,
  type SecurityErrorCode,
  type SecurityErrorOptions,
} from './errors.ts';
export {
  SECURE_SCHEMES,
  type NormalizedUrl,
  type ScopeMatch,
  type ScopeRelation,
  compareScopes,
  isPathPrefix,
  normalizeUrl,
  sameOrigin,
  scopeContains,
  scopeFromUrl,
  scriptDirectory,
} from './url.ts';
export {
  planRegistration,
  type PlanRegistrationInput,
  type RegistrationPlan,
} from './scope.ts';
export {
  RegistrationPlanner,
  type OverlapReport,
  type PlannedRegistration,
  type RegistrationDecision,
} from './planner.ts';
export {
  RegistrationModel,
  ServiceWorkerVersion,
  type ActivateResult,
  type ClientSnapshot,
  type InstallHandle,
  type VersionState,
} from './lifecycle.ts';
