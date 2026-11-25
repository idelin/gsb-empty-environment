export { RULE_VERSION } from "./types.ts";
export type {
  AdmissionRequest,
  AdmissionResult,
  BindingTable,
  CanonicalGrammar,
  CanonicalProduction,
  ConstructorDescriptor,
  DeploymentConfig,
  GrammarDocument,
  InclusionCertificate,
  InclusionObligation,
  ParamPermutation,
  RejectionLocation,
  RenameRule,
  TypeGraph,
  TypeNode,
  Variance,
  VarianceMark,
  Verdict,
  VersionRange,
  VersionRangeRule,
  WideningWitness,
} from "./types.ts";

export {
  arrayDescriptor,
  canonicalJson,
  ctorId,
  ctorKey,
  sha256Hex,
} from "./canonical.ts";
export { canonicalizeGrammar, GrammarError } from "./grammar.ts";
export { normalizeGraph } from "./graph.ts";
export {
  assertRangesBackedByOldTable,
  DeploymentError,
  mapNewTable,
  parseVersionRange,
  rangeContains,
  validateDeployment,
} from "./deployment.ts";
export { decideInclusion } from "./inclusion.ts";
export {
  issueCertificate,
  sealCertificate,
  verifyCertificate,
} from "./certificate.ts";
export type {
  CertifiedRuleBundle,
} from "./runtime.ts";
export { admit } from "./runtime.ts";
export {
  enumerateGrammarTerms,
  termKey,
  unfoldGraph,
} from "./unfold.ts";
export type { GroundTerm } from "./unfold.ts";
