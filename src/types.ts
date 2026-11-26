import type { Rational, RationalInput, ExtendedRational } from './rational.ts';

export type SecretId = string;
export type ClassId = string;
export type ModuleId = string;
export type CandidateId = string;
export type ResourceId = string;
export type EventId = string;
export type SampleId = string;
export type ProjectionId = string;
export type GroupId = string;

export const EMPTY_CANDIDATE = '';
export const EMPTY_PUBLIC_VALUE = '∅';

export type GenerationInput = bigint | number | string;

export interface SignatureVerifyContext {
  readonly module: SignedWorkModule;
  readonly canonicalPayload: string;
  readonly signature: string;
  readonly expectedPolicyGeneration: bigint;
}

export type SignatureVerifier = (context: SignatureVerifyContext) => boolean;

export interface SignedWorkModule {
  readonly id: ModuleId;
  readonly policyGeneration: GenerationInput;
  readonly signature: string;
  readonly signerKeyId?: string;
  readonly codeHash?: string;
  readonly resourceOf?: Readonly<Record<CandidateId, ResourceId>>;
  readonly tables: Readonly<Record<SecretId, Readonly<Record<CandidateId, RationalInput>>>>;
}

export interface NormalizedWorkModule {
  readonly id: ModuleId;
  readonly policyGeneration: bigint;
  readonly signature: string;
  readonly signerKeyId?: string;
  readonly codeHash?: string;
  readonly resourceOf: Readonly<Record<CandidateId, ResourceId>>;
  readonly tables: Readonly<Record<SecretId, Readonly<Record<CandidateId, Rational>>>>;
  readonly publicOutcomes: readonly ResourceId[];
}

export interface EquivalenceClassInput {
  readonly id: ClassId;
  readonly secrets: readonly SecretId[];
}

export type EquivalenceClassesInput =
  | readonly EquivalenceClassInput[]
  | Readonly<Record<ClassId, readonly SecretId[]>>;

export interface PrivacyPolicyInput {
  readonly secrets: readonly SecretId[];
  readonly priors: Readonly<Record<SecretId, RationalInput>>;
  readonly ratioCap: RationalInput;
  readonly policyGeneration: GenerationInput;
  readonly equivalenceClasses?: EquivalenceClassesInput;
  readonly protectedClassPairs?: readonly (readonly [ClassId, ClassId])[] | 'all';
  readonly posteriorMultiplier?: RationalInput;
  readonly normalizePriors?: boolean;
}

export interface ResolvedPrivacyPolicy {
  readonly secrets: readonly SecretId[];
  readonly priors: Readonly<Record<SecretId, Rational>>;
  readonly ratioCap: Rational;
  readonly policyGeneration: bigint;
  readonly classes: ReadonlyMap<ClassId, readonly SecretId[]>;
  readonly classOfSecret: Readonly<Record<SecretId, ClassId>>;
  readonly protectedSecretPairs: readonly (readonly [SecretId, SecretId, ClassId, ClassId])[];
  readonly posteriorMultiplier: Rational;
}

export interface ObservationInput {
  readonly eventId: EventId;
  readonly sampleId?: SampleId;
  readonly observerId?: GroupId;
  readonly moduleId?: ModuleId;
  readonly projection?: ProjectionId;
  readonly candidateId?: CandidateId | number | null;
  readonly resourceId?: ResourceId | null;
  readonly empty?: boolean;
  readonly branchId?: string;
  readonly jointTableId?: string;
}

export interface NormalizedObservation {
  readonly eventId: EventId;
  readonly sampleId: SampleId;
  readonly observerId?: GroupId;
  readonly moduleId?: ModuleId;
  readonly projection: ProjectionId;
  readonly candidateId: CandidateId | null;
  readonly requestedResourceId?: ResourceId | null;
  readonly empty: boolean;
  readonly branchId?: string;
  readonly jointTableId?: string;
  readonly order: number;
}

export interface JointAtomInput {
  readonly outputs: Readonly<Record<ProjectionId, CandidateId | number | null>>;
  readonly probability: Readonly<Record<SecretId, RationalInput>>;
}

export interface JointPreimageTableInput {
  readonly id: string;
  readonly projections?: readonly ProjectionId[];
  readonly atoms: readonly JointAtomInput[];
}

export interface ResolvedJointAtom {
  readonly outputs: Readonly<Record<ProjectionId, CandidateId | null>>;
  readonly probability: Readonly<Record<SecretId, Rational>>;
}

export interface ResolvedJointTable {
  readonly id: string;
  readonly projections: readonly ProjectionId[];
  readonly atoms: readonly ResolvedJointAtom[];
}

export type JointTableMap = Readonly<Record<string, JointPreimageTableInput>> | readonly JointPreimageTableInput[];
export type ModuleMap = Readonly<Record<string, SignedWorkModule>> | readonly SignedWorkModule[];

export interface WitnessStep {
  readonly order: number;
  readonly eventId: EventId;
  readonly sampleId: SampleId;
  readonly projection: ProjectionId;
  readonly moduleId?: ModuleId;
  readonly publicValue: ResourceId | typeof EMPTY_PUBLIC_VALUE;
  readonly candidateId?: CandidateId | null;
  readonly projections?: readonly {
    readonly projection: ProjectionId;
    readonly moduleId?: ModuleId;
    readonly publicValue: ResourceId | typeof EMPTY_PUBLIC_VALUE;
    readonly candidateId?: CandidateId | null;
  }[];
  readonly dedupedEventIds?: readonly EventId[];
  readonly eventIds?: readonly EventId[];
  readonly billingKind: 'module-outcome' | 'joint-preimage' | 'empty-backoff';
  readonly likelihoodFactor: Readonly<Record<SecretId, string>>;
}

export interface PosteriorWitness {
  readonly secret: SecretId;
  readonly posterior: Rational;
  readonly prior: Rational;
  readonly threshold: Rational;
  readonly multiple: Rational;
  readonly sequence: readonly WitnessStep[];
}

export interface LrWitness {
  readonly numeratorSecret: SecretId;
  readonly denominatorSecret: SecretId;
  readonly numeratorClass: ClassId;
  readonly denominatorClass: ClassId;
  readonly ratio: ExtendedRational;
  readonly cap: Rational;
  readonly numeratorLikelihood: Rational;
  readonly denominatorLikelihood: Rational;
  readonly sequence: readonly WitnessStep[];
}

export interface PrivacyIssue {
  readonly code: string;
  readonly message: string;
  readonly eventId?: EventId;
  readonly sampleId?: SampleId;
}

export interface TraceReport {
  readonly traceId: string;
  readonly evaluable: boolean;
  readonly compliant: boolean;
  readonly likelihood: Readonly<Record<SecretId, Rational>>;
  readonly posterior: Readonly<Record<SecretId, Rational>>;
  readonly posteriorMultiple: Readonly<Record<SecretId, Rational>>;
  readonly sequence: readonly WitnessStep[];
  readonly issues: readonly PrivacyIssue[];
  readonly violatingSecrets: readonly SecretId[];
  readonly violatingPairs: readonly (readonly [SecretId, SecretId])[];
}

export interface PrivacyReport {
  readonly allowed: boolean;
  readonly policyGeneration: bigint;
  readonly ratioCap: Rational;
  readonly posteriorMultiplier: Rational;
  readonly traceCount: number;
  readonly traces: readonly TraceReport[];
  readonly worstPosterior: Rational | null;
  readonly worstPosteriorWitness: PosteriorWitness | null;
  readonly worstPosteriorMultiple: Rational | null;
  readonly worstPosteriorMultipleWitness: PosteriorWitness | null;
  readonly posteriorViolationWitness: PosteriorWitness | null;
  readonly maxLikelihoodRatio: ExtendedRational | null;
  readonly maxLrWitness: LrWitness | null;
  readonly witnessSequence: readonly WitnessStep[] | null;
  readonly issues: readonly PrivacyIssue[];
}

export interface FixedRejection {
  readonly ok: false;
  readonly allowed: false;
  readonly decision: 'reject';
  readonly code: 'RELEASE_REJECTED';
}

export interface AllowedRelease<T = undefined> {
  readonly ok: true;
  readonly allowed: true;
  readonly decision: 'allow';
  readonly code: 'RELEASE_ALLOWED';
  readonly data?: T;
}

export type ReleaseResult<T = undefined> = AllowedRelease<T> | FixedRejection;
