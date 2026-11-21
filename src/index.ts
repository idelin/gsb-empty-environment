export {
  DEFAULT_REIDENTIFICATION_THRESHOLD,
  assessReidentification,
} from "./assess.ts";
export {
  canonicalize,
  canonicalizeQiValue,
  isMissing,
  signatureForColumns,
  signatureForValues,
  valuesForColumns,
} from "./canonical.ts";
export {
  assessQuasiIdentifierCombinations,
  assessQuasiIdentifiers,
  median,
  summarizeClassSizes,
} from "./equivalence.ts";
export { assessCrossDatasetJoin } from "./join.ts";
export { assessCumulativeReleases } from "./cumulative.ts";
export {
  planSingleReleaseGeneralization,
  recommendCumulativeGeneralization,
} from "./generalize.ts";
export type {
  AssessmentOptions,
  ConflictPolicy,
  CrossDatasetJoinAssessment,
  CumulativeAssessment,
  CumulativeAssessmentOptions,
  CumulativeConflict,
  CumulativeEquivalenceClass,
  CumulativeIndividual,
  CumulativeReleaseAssessment,
  CumulativeRiskSummary,
  Dataset,
  EquivalenceClass,
  GeneralizationAction,
  GeneralizationHierarchy,
  GeneralizationLevel,
  GeneralizationProjection,
  JoinMatch,
  JoinOptions,
  JoinRiskSummary,
  MissingValuePolicy,
  PublicationRecommendation,
  QIAssessment,
  QuasiIdentifierReport,
  RecordData,
  ReidentificationAssessment,
  ReidentificationAssessmentInput,
  Release,
  ReleaseDelta,
  RiskSummary,
} from "./types.ts";
