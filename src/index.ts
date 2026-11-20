export type {
  AdmissionInput,
  AdmissionResult,
  BagCounterexample,
  CertificateCheckResult,
  ColumnSchema,
  Database,
  DatabaseSchema,
  ExecutionCredential,
  Expr,
  JoinType,
  PartitionCertificate,
  Plan,
  Projection,
  RepresentativeClass,
  RepresentativeColumnDomain,
  RepresentativeDatabase,
  RepresentativeDomain,
  RepresentativeTableDomain,
  RowValues,
  SqlValue,
  TableSchema,
  TenantPolicyRule,
  TenantReadPolicy,
} from "./types.ts";

export {
  allOf,
  and,
  anyOf,
  as,
  col,
  column,
  constant,
  enumConst,
  enumConstant,
  eq,
  equals,
  filter,
  fromTable,
  idConst,
  idConstant,
  innerJoin,
  isNull,
  isNullExpr,
  join,
  leftJoin,
  literal,
  normalizeExpr,
  normalizePlan,
  not,
  nullConstant,
  or,
  planDigest,
  project,
  projection,
  ref,
  scan,
  select,
  table,
  union,
  unionAll,
  validateExpr,
  validatePlan,
  where,
  PlanSyntaxError,
} from "./ast.ts";

export {
  evaluatePlan,
  evaluatePlanWithOptions,
  planOutputSpec,
} from "./eval.ts";
export type { ColumnSpec, EvaluationOptions } from "./eval.ts";

export {
  NULL,
  enumValue,
  equalValues,
  idValue,
  isNullValue,
  normalizeValue,
  nullValue,
  valueForDisplay,
  valueFromKey,
  valueKey,
} from "./value.ts";

export {
  normalizePolicy,
  policyTenantLabels,
} from "./policy.ts";
export type { NormalizedPolicy } from "./policy.ts";

export {
  analyzePlans,
} from "./analysis.ts";
export type { PlanAnalysis } from "./analysis.ts";

export {
  buildRepresentativeDatabases,
  buildPartitionCertificate,
  canonicalizeDatabase,
  certificateDigest,
  constructRepresentativeDatabases,
  databaseClassKey,
  DEFAULT_MAX_CLASSES,
  deriveRepresentativeDomain,
  domainsEqual,
  enumerateDomainClasses,
  RepresentativeLimitError,
} from "./representatives.ts";
export type { EnumeratedRepresentatives } from "./representatives.ts";

export {
  canonicalCertificateJson,
  certificateCoversAllClasses,
  certificateSignature,
  checkPartitionCertificate,
  checkRepresentativeCoverage,
  isPartitionCertificateComplete,
  verifyCertificateCoverage,
  verifyPartitionCertificate,
} from "./certificate.ts";
export type { CertificateCheckContext } from "./certificate.ts";

export {
  admit,
  admitPlan,
  admitQueryPlan,
  authorizePlan,
  checkPlanAdmission,
  checkPlanEquivalence,
  plansEquivalent,
  verifyExecutionCredential,
} from "./admission.ts";
