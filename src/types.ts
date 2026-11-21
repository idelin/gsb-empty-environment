export type RecordData = Readonly<Record<string, unknown>>;

export interface Dataset {
  readonly name?: string;
  readonly recordIdField?: string;
  readonly records: readonly RecordData[];
}

export type MissingValuePolicy = "as-value" | "exclude";

export interface AssessmentOptions {
  readonly threshold: number;
  readonly missingValuePolicy?: MissingValuePolicy;
  readonly recordIdField?: string;
}

export interface EquivalenceClass {
  /** Stable canonical signature of the quasi-identifier values. */
  readonly signature: string;
  /** Raw values from the first record in the class. */
  readonly values: Readonly<Record<string, unknown>>;
  readonly size: number;
  readonly recordIndexes: readonly number[];
  readonly recordIds: readonly string[];
  readonly unique: boolean;
  readonly belowThreshold: boolean;
  /** Probability that a randomly selected member of this class is the target. */
  readonly reidentificationProbability: number;
}

export interface RiskSummary {
  readonly recordCount: number;
  readonly equivalenceClassCount: number;
  readonly classSizes: readonly number[];
  readonly minClassSize: number;
  readonly medianClassSize: number;
  readonly meanClassSize: number;
  readonly maxClassSize: number;
  readonly uniqueClassCount: number;
  readonly uniqueRecordCount: number;
  readonly belowThresholdClassCount: number;
  readonly belowThresholdRecordCount: number;
  readonly atRiskRecordRatio: number;
  readonly maxReidentificationProbability: number;
  readonly flagged: boolean;
}

export interface QIAssessment extends RiskSummary {
  readonly qis: readonly string[];
  readonly threshold: number;
  readonly inputRecordCount: number;
  readonly excludedRecordCount: number;
  readonly classes: readonly EquivalenceClass[];
  readonly flaggedClasses: readonly EquivalenceClass[];
}

export interface QuasiIdentifierReport {
  readonly threshold: number;
  readonly combinations: readonly QIAssessment[];
  readonly flaggedCombinations: readonly QIAssessment[];
  readonly overallFlagged: boolean;
}

export interface JoinOptions {
  /** Quasi-identifier columns in the target dataset. */
  readonly qis: readonly string[];
  /** Aligned quasi-identifier columns in the auxiliary dataset. Defaults to `qis`. */
  readonly auxiliaryQis?: readonly string[];
  readonly targetRecordIdField?: string;
  readonly auxiliaryRecordIdField?: string;
  readonly threshold?: number;
}

export interface JoinMatch {
  readonly targetRecordIndex: number;
  readonly targetRecordId: string;
  readonly signature: string;
  readonly targetClassSize: number;
  readonly auxiliaryCandidateCount: number;
  readonly auxiliaryIdentities: readonly string[];
  readonly matched: boolean;
  /** The auxiliary QI bucket resolves to exactly one distinct identity. */
  readonly uniqueAuxiliaryCandidate: boolean;
  /** True only when both the target class and the auxiliary candidate set are singletons. */
  readonly uniquelyReidentified: boolean;
  /** The join narrows the auxiliary identity to one, but the target row is still in a larger class. */
  readonly narrowedToSingleIdentity: boolean;
  /** Candidate target-row/identity pairs implied by the join. */
  readonly candidatePairs: number;
}

export interface JoinRiskSummary {
  readonly targetRecordCount: number;
  readonly matchedTargetRecordCount: number;
  readonly unmatchedTargetRecordCount: number;
  readonly ambiguousTargetRecordCount: number;
  readonly uniqueAuxiliaryCandidateCount: number;
  readonly uniqueAuxiliaryCandidateRatio: number;
  readonly uniquelyReidentifiedRecordCount: number;
  readonly uniquelyReidentifiedRatio: number;
  readonly narrowedToSingleIdentityCount: number;
  readonly baselineUniqueTargetRecordCount: number;
  readonly maxCandidateCount: number;
  readonly minCandidateCount: number;
  readonly medianCandidateCount: number;
  readonly riskLevel: "low" | "medium" | "high";
}

export interface CrossDatasetJoinAssessment extends JoinRiskSummary {
  readonly qis: readonly string[];
  readonly auxiliaryQis: readonly string[];
  readonly targetDatasetName: string;
  readonly auxiliaryDatasetName: string;
  readonly auxiliaryRecordCount: number;
  readonly auxiliaryIdentityCount: number;
  readonly targetClasses: QIAssessment;
  readonly matches: readonly JoinMatch[];
}

export interface Release extends Dataset {
  readonly id: string;
  readonly qis: readonly string[];
  readonly publishedAt?: string;
}

export type ConflictPolicy = "flag" | "reject";

export interface CumulativeAssessmentOptions {
  readonly threshold: number;
  readonly recordIdField?: string;
  readonly conflictPolicy?: ConflictPolicy;
  readonly generalizationHierarchies?: readonly GeneralizationHierarchy[];
}

export interface CumulativeEquivalenceClass {
  readonly signature: string;
  readonly size: number;
  readonly recordIds: readonly string[];
  /** Raw QI values grouped by release. */
  readonly releaseValues: Readonly<Record<string, Readonly<Record<string, unknown>>>>;
  readonly unique: boolean;
  readonly belowThreshold: boolean;
  readonly reidentificationProbability: number;
}

export interface CumulativeIndividual {
  readonly recordId: string;
  readonly presentReleaseIds: readonly string[];
  readonly releaseValues: Readonly<Record<string, Readonly<Record<string, unknown>>>>;
  readonly conflictingColumns: readonly string[];
  readonly signature: string;
  readonly classSize: number;
  /** Smallest class size this individual had in any single release. */
  readonly baselineMinimumClassSize: number;
  readonly uniqueBefore: boolean;
  readonly uniqueAfter: boolean;
  readonly amplifiedByIntersection: boolean;
}

export interface ReleaseDelta {
  readonly releaseId: string;
  /** Records that were not present in any earlier release. */
  readonly addedRecordCount: number;
  /** Records that also appeared in at least one earlier release. */
  readonly retainedRecordCount: number;
  /** Records in the union of earlier releases that are absent from this release snapshot. */
  readonly droppedRecordCount: number;
  readonly addedRecordIds: readonly string[];
  readonly retainedRecordIds: readonly string[];
  readonly droppedRecordIds: readonly string[];
}

export interface CumulativeReleaseAssessment {
  readonly releaseId: string;
  readonly qis: readonly string[];
  readonly recordCount: number;
  readonly assessment: QIAssessment;
}

export interface CumulativeRiskSummary extends RiskSummary {
  readonly totalReleasedRecordCount: number;
  readonly cumulativeDistinctRecordCount: number;
  readonly overlapRecordCount: number;
  readonly averageReleasesPerIndividual: number;
  readonly uniqueBeforeCount: number;
  readonly uniqueAfterCount: number;
  readonly newlyUniqueRecordCount: number;
  readonly amplifiedRecordCount: number;
  readonly newlyUniqueRecordIds: readonly string[];
  readonly amplifiedRecordIds: readonly string[];
}

export interface GeneralizationLevel {
  readonly label: string;
  readonly transform: (value: unknown) => unknown;
}

export interface GeneralizationHierarchy {
  readonly column: string;
  /** Ordered from identity (index 0) to more generalized values. */
  readonly levels: readonly GeneralizationLevel[];
}

export interface GeneralizationAction {
  readonly column: string;
  readonly level: number;
  readonly label: string;
  readonly kind: "generalize" | "suppress";
  /** Existing releases containing the column; empty for a single-release plan. */
  readonly releaseIds: readonly string[];
}

export interface GeneralizationProjection {
  readonly threshold: number;
  readonly achievable: boolean;
  readonly actions: readonly GeneralizationAction[];
  readonly before: RiskSummary;
  readonly projected: RiskSummary;
  readonly explanation: string;
}

export interface PublicationRecommendation extends GeneralizationProjection {
  readonly required: boolean;
  readonly urgency: "none" | "corrective" | "blocking";
  readonly scope: "none" | "supplemental-generalized-release" | "future-releases-only";
  readonly rationale: string;
  readonly notes: readonly string[];
}

export interface CumulativeAssessment extends CumulativeRiskSummary {
  readonly threshold: number;
  readonly releaseCount: number;
  readonly releases: readonly CumulativeReleaseAssessment[];
  readonly releaseDeltas: readonly ReleaseDelta[];
  readonly qis: readonly string[];
  readonly classes: readonly CumulativeEquivalenceClass[];
  readonly flaggedClasses: readonly CumulativeEquivalenceClass[];
  readonly individuals: readonly CumulativeIndividual[];
  readonly conflicts: readonly CumulativeConflict[];
  readonly recommendation: PublicationRecommendation;
}

export interface CumulativeConflict {
  readonly recordId: string;
  readonly column: string;
  readonly releaseValues: Readonly<Record<string, unknown>>;
}

export interface ReidentificationAssessmentInput {
  readonly dataset: Dataset;
  readonly quasiIdentifierCombinations: readonly (readonly string[])[];
  readonly threshold?: number;
  readonly missingValuePolicy?: MissingValuePolicy;
  readonly recordIdField?: string;
  readonly auxiliary?: {
    readonly dataset: Dataset;
    readonly qis?: readonly string[];
    readonly recordIdField?: string;
  };
}

export interface ReidentificationAssessment {
  readonly threshold: number;
  readonly combinations: readonly QIAssessment[];
  readonly flaggedCombinations: readonly QIAssessment[];
  readonly overallFlagged: boolean;
  readonly joins: readonly CrossDatasetJoinAssessment[];
}
