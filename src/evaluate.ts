import { moduleSigningPayload } from './canonical.ts';
import { FIXED_REJECTION } from './fixed.ts';
import {
  PrivacyConfigurationError,
  normalizeWorkModule,
  resolvePolicy,
} from './policy.ts';
import { ExtendedRational, ONE, Rational, ZERO } from './rational.ts';
import type {
  JointPreimageTableInput,
  LrWitness,
  ModuleMap,
  NormalizedObservation,
  NormalizedWorkModule,
  ObservationInput,
  PosteriorWitness,
  PrivacyIssue,
  PrivacyPolicyInput,
  PrivacyReport,
  ReleaseResult,
  ResolvedJointTable,
  ResolvedPrivacyPolicy,
  SecretId,
  SignatureVerifier,
  SignedWorkModule,
  TraceReport,
  WitnessStep,
} from './types.ts';
import { EMPTY_CANDIDATE, EMPTY_PUBLIC_VALUE } from './types.ts';

export interface EvaluationContext {
  readonly policy: ResolvedPrivacyPolicy;
  readonly modules: ReadonlyMap<string, NormalizedWorkModule>;
  readonly jointTables: ReadonlyMap<string, ResolvedJointTable>;
  readonly verifier?: SignatureVerifier;
  readonly issues: readonly PrivacyIssue[];
  readonly trusted: boolean;
}

export interface CreateContextInput {
  readonly policy?: ResolvedPrivacyPolicy;
  readonly policyInput?: ConstructorParameters<typeof resolvePolicy>[0];
  readonly modules?: ModuleMap;
  readonly jointTables?:
    | Readonly<Record<string, JointPreimageTableInput>>
    | readonly JointPreimageTableInput[];
  readonly signatureVerifier?: SignatureVerifier;
}

function toArray<T>(value: readonly T[] | Readonly<Record<string, T>> | undefined): T[] {
  if (!value) return [];
  if (Array.isArray(value)) return [...value];
  return Object.values(value);
}

function normalizeJointTables(
  tables: CreateContextInput['jointTables'],
  policy: ResolvedPrivacyPolicy,
): Map<string, ResolvedJointTable> {
  const map = new Map<string, ResolvedJointTable>();
  if (!tables) return map;
  const list = Array.isArray(tables) ? tables : Object.values(tables);
  for (const table of list) {
    if (map.has(table.id)) throw new PrivacyConfigurationError(`Duplicate joint table ${table.id}`);
    const projectionSet = new Set<string>(table.projections ?? []);
    const atoms = table.atoms.map((atom) => {
      const outputs: Record<string, string | null> = {};
      for (const [projection, rawCandidate] of Object.entries(atom.outputs)) {
        projectionSet.add(projection);
        if (rawCandidate === null || rawCandidate === EMPTY_CANDIDATE) {
          outputs[projection] = null;
        } else {
          outputs[projection] = String(rawCandidate);
        }
      }
      const probability: Record<SecretId, Rational> = {};
      for (const secret of policy.secrets) {
        const value = atom.probability[secret];
        if (value === undefined) {
          throw new PrivacyConfigurationError(`Joint table ${table.id} misses probability for ${secret}`);
        }
        probability[secret] = Rational.from(value);
      }
      return { outputs: Object.freeze(outputs), probability: Object.freeze(probability) };
    });

    for (const secret of policy.secrets) {
      const total = atoms.reduce((sum, atom) => sum.add(atom.probability[secret]), ZERO);
      if (!total.eq(1n)) {
        throw new PrivacyConfigurationError(`Joint table ${table.id} for ${secret} sums to ${total}, expected 1`);
      }
    }

    map.set(
      table.id,
      Object.freeze({
        id: table.id,
        projections: Object.freeze([...projectionSet]),
        atoms: Object.freeze(atoms),
      }),
    );
  }
  return map;
}

function verifyAndNormalizeModule(
  rawModule: SignedWorkModule,
  policy: ResolvedPrivacyPolicy,
  verifier: SignatureVerifier | undefined,
): { module: NormalizedWorkModule | null; issues: PrivacyIssue[] } {
  const issues: PrivacyIssue[] = [];
  if (typeof rawModule.signature !== 'string' || rawModule.signature.length === 0) {
    issues.push({ code: 'MISSING_SIGNATURE', message: `Module ${rawModule.id} has no signature` });
  }

  const payload = moduleSigningPayload(rawModule);
  let signatureValid = false;
  if (issues.length === 0) {
    if (!verifier) {
      issues.push({ code: 'NO_SIGNATURE_VERIFIER', message: 'No signature verifier configured; default deny' });
    } else {
      try {
        signatureValid = verifier({
          module: rawModule,
          canonicalPayload: payload,
          signature: rawModule.signature,
          expectedPolicyGeneration: policy.policyGeneration,
        });
      } catch (error) {
        issues.push({ code: 'SIGNATURE_VERIFIER_ERROR', message: (error as Error).message });
      }
      if (!signatureValid) issues.push({ code: 'INVALID_SIGNATURE', message: `Signature rejected for ${rawModule.id}` });
    }
  }

  const normalized = normalizeWorkModule(rawModule, policy);
  issues.push(...normalized.issues);

  const blocking = issues.some((issue) =>
    [
      'MISSING_SIGNATURE',
      'NO_SIGNATURE_VERIFIER',
      'INVALID_SIGNATURE',
      'SIGNATURE_VERIFIER_ERROR',
      'POLICY_GENERATION_MISMATCH',
      'INVALID_GENERATION',
      'MISSING_SECRET_TABLE',
      'TABLE_NOT_NORMALIZED',
      'NEGATIVE_PROBABILITY',
      'INVALID_PROBABILITY',
      'UNRESOLVED_CANDIDATE',
    ].includes(issue.code),
  );

  return { module: blocking ? null : normalized.module, issues };
}

export function createEvaluationContext(input: CreateContextInput): EvaluationContext {
  if (!input.policy && !input.policyInput) {
    throw new PrivacyConfigurationError('Either resolved policy or policyInput is required');
  }
  const policy = input.policy ?? resolvePolicy(input.policyInput as PrivacyPolicyInput);
  const modules = new Map<string, NormalizedWorkModule>();
  const issues: PrivacyIssue[] = [];

  for (const rawModule of toArray<SignedWorkModule>(input.modules)) {
    const result = verifyAndNormalizeModule(rawModule, policy, input.signatureVerifier);
    issues.push(...result.issues);
    if (result.module) {
      if (modules.has(result.module.id)) {
        issues.push({ code: 'DUPLICATE_MODULE', message: `Duplicate module id ${result.module.id}` });
      } else {
        modules.set(result.module.id, result.module);
      }
    }
  }

  const jointTables = normalizeJointTables(input.jointTables, policy);
  const trusted = issues.length === 0;
  return Object.freeze({ policy, modules, jointTables, verifier: input.signatureVerifier, issues: Object.freeze(issues), trusted });
}

function normalizeObservation(input: ObservationInput, order: number): NormalizedObservation {
  const empty = input.empty === true || input.candidateId === EMPTY_CANDIDATE;
  const candidateId =
    input.candidateId === undefined || input.candidateId === null
      ? null
      : String(input.candidateId);
  return Object.freeze({
    eventId: input.eventId,
    sampleId: input.sampleId ?? input.eventId,
    ...(input.observerId ? { observerId: input.observerId } : {}),
    ...(input.moduleId ? { moduleId: input.moduleId } : {}),
    projection: input.projection ?? input.moduleId ?? input.eventId,
    candidateId: empty ? EMPTY_CANDIDATE : candidateId,
    requestedResourceId: input.resourceId ?? null,
    empty,
    ...(input.branchId ? { branchId: input.branchId } : {}),
    ...(input.jointTableId ? { jointTableId: input.jointTableId } : {}),
    order,
  });
}

function fingerprintObservation(observation: NormalizedObservation): string {
  return JSON.stringify([
    observation.sampleId,
    observation.moduleId ?? null,
    observation.projection,
    observation.candidateId,
    observation.requestedResourceId ?? null,
    observation.empty,
    observation.jointTableId ?? null,
  ]);
}

interface SampleGroup {
  readonly sampleId: string;
  readonly records: readonly NormalizedObservation[];
  readonly order: number;
}

function dedupeAndGroup(
  context: EvaluationContext,
  observations: readonly ObservationInput[],
  issues: PrivacyIssue[],
): { sequence: readonly WitnessStep[]; groups: readonly SampleGroup[] } {
  const byEvent = new Map<string, NormalizedObservation>();
  for (const [index, input] of observations.entries()) {
    const observation = normalizeObservation(input, index);
    const existing = byEvent.get(observation.eventId);
    if (existing) {
      const rawSame = fingerprintObservation(existing) === fingerprintObservation(observation);
      const semanticSame = semanticFingerprint(existing, context) === semanticFingerprint(observation, context);
      if (!rawSame && !semanticSame) {
        issues.push({
          code: 'EVENT_ID_CONFLICT',
          message: `Shared event id ${observation.eventId} has conflicting projections`,
          eventId: observation.eventId,
        });
      }
      continue;
    }
    byEvent.set(observation.eventId, observation);
  }

  const groupsMap = new Map<string, NormalizedObservation[]>();
  for (const observation of byEvent.values()) {
    const group = groupsMap.get(observation.sampleId) ?? [];
    group.push(observation);
    groupsMap.set(observation.sampleId, group);
  }

  const groups = [...groupsMap.entries()]
    .map(([sampleId, records]) => ({
      sampleId,
      records: records.sort((a, b) => a.order - b.order),
      order: Math.min(...records.map((record) => record.order)),
    }))
    .sort((a, b) => a.order - b.order);

  return { sequence: [], groups };
}

function publicValueOf(observation: NormalizedObservation, module: NormalizedWorkModule | undefined): string | null {
  if (observation.empty) return EMPTY_PUBLIC_VALUE;
  if (observation.requestedResourceId !== null && observation.requestedResourceId !== undefined) {
    return observation.requestedResourceId;
  }
  if (observation.candidateId !== null) {
    return module?.resourceOf[observation.candidateId] ?? observation.candidateId;
  }
  return null;
}

function semanticFingerprint(observation: NormalizedObservation, context: EvaluationContext): string {
  const module = observation.moduleId ? context.modules.get(observation.moduleId) : undefined;
  return JSON.stringify([
    observation.sampleId,
    observation.moduleId ?? null,
    observation.projection,
    observation.empty,
    publicValueOf(observation, module),
    observation.jointTableId ?? null,
  ]);
}

function moduleOutcomeVector(
  module: NormalizedWorkModule,
  publicValue: string,
  policy: ResolvedPrivacyPolicy,
): Record<SecretId, Rational> {
  const result = {} as Record<SecretId, Rational>;
  for (const secret of policy.secrets) {
    let total = ZERO;
    for (const [candidate, probability] of Object.entries(module.tables[secret] ?? {})) {
      const resolved = candidate === EMPTY_CANDIDATE ? EMPTY_PUBLIC_VALUE : module.resourceOf[candidate] ?? candidate;
      if (resolved === publicValue) total = total.add(probability);
    }
    result[secret] = total;
  }
  return result;
}

function findJointTable(
  context: EvaluationContext,
  records: readonly NormalizedObservation[],
  explicitId?: string,
): ResolvedJointTable | null {
  if (explicitId) return context.jointTables.get(explicitId) ?? null;
  const candidates = [...context.jointTables.values()].filter((table) =>
    records.every((record) => table.projections.includes(record.projection) || (record.moduleId && table.projections.includes(record.moduleId))),
  );
  return candidates.length === 1 ? candidates[0] : null;
}

function jointOutcomeVector(
  context: EvaluationContext,
  table: ResolvedJointTable,
  records: readonly NormalizedObservation[],
  publicValues: readonly (string | null)[],
): Record<SecretId, Rational> {
  const result = Object.fromEntries(context.policy.secrets.map((secret) => [secret, ZERO])) as Record<SecretId, Rational>;

  for (const atom of table.atoms) {
    let matches = true;
    records.forEach((record, index) => {
      if (!matches) return;
      const module = record.moduleId ? context.modules.get(record.moduleId) : undefined;
      const raw =
        atom.outputs[record.projection] ??
        (record.moduleId ? atom.outputs[record.moduleId] : undefined);
      if (raw === undefined) {
        matches = false;
        return;
      }
      const atomPublic = raw === null ? EMPTY_PUBLIC_VALUE : module?.resourceOf[raw] ?? raw;
      if (atomPublic !== publicValues[index]) matches = false;
    });
    if (!matches) continue;
    for (const secret of context.policy.secrets) {
      result[secret] = result[secret].add(atom.probability[secret] ?? ZERO);
    }
  }
  return result;
}

export function evaluateTraceWithContext(
  context: EvaluationContext,
  observations: readonly ObservationInput[],
  traceId = 'trace',
): TraceReport {
  const issues: PrivacyIssue[] = [...context.issues];
  const { groups } = dedupeAndGroup(context, observations, issues);
  const policy = context.policy;
  const likelihood = Object.fromEntries(policy.secrets.map((secret) => [secret, ONE])) as Record<SecretId, Rational>;
  const sequence: WitnessStep[] = [];

  for (const group of groups) {
    const first = group.records[0];
    const module = first.moduleId ? context.modules.get(first.moduleId) : undefined;
    if (first.moduleId && !module) {
      issues.push({
        code: 'UNKNOWN_OR_UNTRUSTED_MODULE',
        message: `Observation uses unavailable module ${first.moduleId}`,
        eventId: first.eventId,
        sampleId: group.sampleId,
      });
    }

    const publicValues = group.records.map((record) => {
      const recordModule = record.moduleId ? context.modules.get(record.moduleId) : undefined;
      const value = publicValueOf(record, recordModule);
      if (value === null) {
        issues.push({
          code: 'UNRESOLVED_OBSERVATION',
          message: 'Observation has neither candidate, resource, nor empty marker',
          eventId: record.eventId,
          sampleId: group.sampleId,
        });
      }
      return value;
    });

    let factor: Record<SecretId, Rational> | null = null;
    let billingKind: WitnessStep['billingKind'] = 'module-outcome';
    let table: ResolvedJointTable | null = null;

    if (group.records.length > 1 || first.jointTableId) {
      billingKind = 'joint-preimage';
      table = findJointTable(context, group.records, first.jointTableId);
      if (!table) {
        issues.push({
          code: 'JOINT_PREIMAGE_TABLE_REQUIRED',
          message: `Same sample ${group.sampleId} has multiple projections without a unique joint table`,
          eventId: first.eventId,
          sampleId: group.sampleId,
        });
      } else {
        factor = jointOutcomeVector(context, table, group.records, publicValues);
      }
    } else if (first.empty) {
      billingKind = 'empty-backoff';
    }

    if (!factor) {
      if (!module || publicValues[0] === null) {
        factor = Object.fromEntries(policy.secrets.map((secret) => [secret, ZERO])) as Record<SecretId, Rational>;
      } else {
        factor = moduleOutcomeVector(module, publicValues[0], policy);
      }
    }

    const allZero = policy.secrets.every((secret) => factor?.[secret]?.isZero() ?? true);
    if (allZero && !issues.some((issue) => issue.sampleId === group.sampleId)) {
      issues.push({
        code: 'OBSERVATION_NOT_IN_TABLE',
        message: `Observation for sample ${group.sampleId} has zero joint preimage under every secret`,
        eventId: first.eventId,
        sampleId: group.sampleId,
      });
    }

    for (const secret of policy.secrets) likelihood[secret] = likelihood[secret].mul(factor[secret] ?? ZERO);

    const primaryPublicValue = group.records.length === 1 ? (publicValues[0] ?? EMPTY_PUBLIC_VALUE) : JSON.stringify(
      Object.fromEntries(group.records.map((record, index) => [record.projection, publicValues[index]])),
    );

    sequence.push({
      order: sequence.length,
      eventId: first.eventId,
      sampleId: group.sampleId,
      projection: group.records.length === 1 ? first.projection : '*',
      ...(first.moduleId ? { moduleId: first.moduleId } : {}),
      publicValue: primaryPublicValue,
      ...(first.candidateId !== null ? { candidateId: first.candidateId } : {}),
      ...(group.records.length > 1
        ? {
            projections: group.records.map((record, index) => ({
              projection: record.projection,
              ...(record.moduleId ? { moduleId: record.moduleId } : {}),
              publicValue: publicValues[index] ?? EMPTY_PUBLIC_VALUE,
              ...(record.candidateId !== null ? { candidateId: record.candidateId } : {}),
            })),
          }
        : {}),
      eventIds: group.records.map((record) => record.eventId),
      dedupedEventIds: [...new Set(group.records.map((record) => record.eventId))],
      billingKind,
      likelihoodFactor: Object.fromEntries(
        policy.secrets.map((secret) => [secret, (factor[secret] ?? ZERO).toString()]),
      ),
    } as WitnessStep);
  }

  const evidence = policy.secrets.reduce((sum, secret) => sum.add(policy.priors[secret].mul(likelihood[secret])), ZERO);
  const blockingIssue = issues.some((issue) => issue.code !== 'OBSERVATION_NOT_IN_TABLE' || evidence.isZero());
  const evaluable = !blockingIssue && evidence.gt(0n);
  if (!evidence.gt(0n) && !issues.some((issue) => issue.code === 'IMPOSSIBLE_OBSERVATION')) {
    issues.push({ code: 'IMPOSSIBLE_OBSERVATION', message: 'Merged observation has zero probability under every secret' });
  }

  const posterior = {} as Record<SecretId, Rational>;
  const posteriorMultiple = {} as Record<SecretId, Rational>;
  const violatingSecrets: SecretId[] = [];
  if (evaluable) {
    for (const secret of policy.secrets) {
      posterior[secret] = policy.priors[secret].mul(likelihood[secret]).div(evidence);
      posteriorMultiple[secret] = posterior[secret].div(policy.priors[secret]);
      if (posterior[secret].gt(policy.posteriorMultiplier.mul(policy.priors[secret]))) {
        violatingSecrets.push(secret);
      }
    }
  }

  const violatingPairs: [SecretId, SecretId][] = [];
  if (evaluable) {
    for (const [numeratorSecret, denominatorSecret] of policy.protectedSecretPairs) {
      const numerator = likelihood[numeratorSecret];
      const denominator = likelihood[denominatorSecret];
      const ratio = denominator.isZero()
        ? numerator.isZero()
          ? ExtendedRational.finite(ZERO)
          : ExtendedRational.infinite()
        : ExtendedRational.finite(numerator.div(denominator));
      if (ratio.exceeds(policy.ratioCap)) violatingPairs.push([numeratorSecret, denominatorSecret]);
    }
  }

  const compliant =
    context.trusted && evaluable && violatingSecrets.length === 0 && violatingPairs.length === 0;
  return Object.freeze({
    traceId,
    evaluable,
    compliant,
    likelihood: Object.freeze(likelihood),
    posterior: Object.freeze(posterior),
    posteriorMultiple: Object.freeze(posteriorMultiple),
    sequence: Object.freeze(sequence),
    issues: Object.freeze(issues),
    violatingSecrets: Object.freeze(violatingSecrets),
    violatingPairs: Object.freeze(violatingPairs),
  });
}

function makePosteriorWitness(trace: TraceReport, policy: ResolvedPrivacyPolicy): PosteriorWitness | null {
  let secret: SecretId | null = null;
  let worst: Rational | null = null;
  for (const candidate of policy.secrets) {
    const value = trace.posterior[candidate];
    if (value && (worst === null || value.gt(worst))) {
      worst = value;
      secret = candidate;
    }
  }
  if (!secret || worst === null) return null;
  const prior = policy.priors[secret];
  return Object.freeze({
    secret,
    posterior: worst,
    prior,
    threshold: policy.posteriorMultiplier.mul(prior),
    multiple: trace.posteriorMultiple[secret],
    sequence: trace.sequence,
  });
}

function makePosteriorMultipleWitness(
  trace: TraceReport,
  policy: ResolvedPrivacyPolicy,
): PosteriorWitness | null {
  let secret: SecretId | null = null;
  let worstMultiple: Rational | null = null;
  for (const candidate of policy.secrets) {
    const value = trace.posteriorMultiple[candidate];
    if (value && (worstMultiple === null || value.gt(worstMultiple))) {
      worstMultiple = value;
      secret = candidate;
    }
  }
  if (!secret || worstMultiple === null) return null;
  const prior = policy.priors[secret];
  const posterior = trace.posterior[secret];
  return Object.freeze({
    secret,
    posterior,
    prior,
    threshold: policy.posteriorMultiplier.mul(prior),
    multiple: worstMultiple,
    sequence: trace.sequence,
  });
}

function makeLrWitness(trace: TraceReport, policy: ResolvedPrivacyPolicy): LrWitness | null {
  let witness: LrWitness | null = null;
  let maxRatio: ExtendedRational | null = null;

  for (const [numeratorSecret, denominatorSecret, numeratorClass, denominatorClass] of policy.protectedSecretPairs) {
    const numeratorLikelihood = trace.likelihood[numeratorSecret];
    const denominatorLikelihood = trace.likelihood[denominatorSecret];
    if (numeratorLikelihood === undefined || denominatorLikelihood === undefined) continue;
    const ratio = denominatorLikelihood.isZero()
      ? numeratorLikelihood.isZero()
        ? ExtendedRational.finite(ZERO)
        : ExtendedRational.infinite()
      : ExtendedRational.finite(numeratorLikelihood.div(denominatorLikelihood));
    if (maxRatio === null || ratio.cmp(maxRatio) > 0) {
      maxRatio = ratio;
      witness = Object.freeze({
        numeratorSecret,
        denominatorSecret,
        numeratorClass,
        denominatorClass,
        ratio,
        cap: policy.ratioCap,
        numeratorLikelihood,
        denominatorLikelihood,
        sequence: trace.sequence,
      });
    }
  }
  return witness;
}

export function buildPrivacyReport(
  context: EvaluationContext,
  traces: readonly TraceReport[],
  extraIssues: readonly PrivacyIssue[] = [],
): PrivacyReport {
  let worstPosterior: Rational | null = null;
  let worstPosteriorWitness: PosteriorWitness | null = null;
  let worstPosteriorMultiple: Rational | null = null;
  let worstPosteriorMultipleWitness: PosteriorWitness | null = null;
  let maxLikelihoodRatio: ExtendedRational | null = null;
  let maxLrWitness: LrWitness | null = null;

  for (const trace of traces) {
    const posteriorWitness = makePosteriorWitness(trace, context.policy);
    if (posteriorWitness && (worstPosterior === null || posteriorWitness.posterior.gt(worstPosterior))) {
      worstPosterior = posteriorWitness.posterior;
      worstPosteriorWitness = posteriorWitness;
    }
    const multipleWitness = makePosteriorMultipleWitness(trace, context.policy);
    if (
      multipleWitness &&
      (worstPosteriorMultiple === null || multipleWitness.multiple.gt(worstPosteriorMultiple))
    ) {
      worstPosteriorMultiple = multipleWitness.multiple;
      worstPosteriorMultipleWitness = multipleWitness;
    }
    const lrWitness = makeLrWitness(trace, context.policy);
    if (lrWitness && (maxLikelihoodRatio === null || lrWitness.ratio.cmp(maxLikelihoodRatio) > 0)) {
      maxLikelihoodRatio = lrWitness.ratio;
      maxLrWitness = lrWitness;
    }
  }
  const posteriorViolationWitness =
    worstPosteriorMultipleWitness &&
    worstPosteriorMultipleWitness.multiple.gt(context.policy.posteriorMultiplier)
      ? worstPosteriorMultipleWitness
      : null;
  const witnessSequence =
    maxLrWitness?.sequence ??
    posteriorViolationWitness?.sequence ??
    worstPosteriorWitness?.sequence ??
    null;

  const issues = [...context.issues, ...extraIssues, ...traces.flatMap((trace) => trace.issues)];
  const allowed = context.trusted && extraIssues.length === 0 && traces.length > 0 && traces.every((trace) => trace.compliant);
  return Object.freeze({
    allowed,
    policyGeneration: context.policy.policyGeneration,
    ratioCap: context.policy.ratioCap,
    posteriorMultiplier: context.policy.posteriorMultiplier,
    traceCount: traces.length,
    traces: Object.freeze(traces),
    worstPosterior,
    worstPosteriorWitness,
    worstPosteriorMultiple,
    worstPosteriorMultipleWitness,
    posteriorViolationWitness,
    maxLikelihoodRatio,
    maxLrWitness,
    witnessSequence,
    issues: Object.freeze(issues),
  });
}

export interface EvaluatePrivacyInput extends CreateContextInput {
  readonly observations?: readonly ObservationInput[];
  readonly traces?: readonly (readonly ObservationInput[])[];
}

export function evaluatePrivacy(input: EvaluatePrivacyInput): PrivacyReport {
  const context = createEvaluationContext(input);
  const traceInputs = input.traces ?? [input.observations ?? []];
  const traces = traceInputs.map((observations, index) =>
    evaluateTraceWithContext(context, observations, `trace-${index + 1}`),
  );
  return buildPrivacyReport(context, traces);
}

export function releasePrivacy(input: EvaluatePrivacyInput): ReleaseResult<PrivacyReport> {
  const report = evaluatePrivacy(input);
  if (!report.allowed) return FIXED_REJECTION;
  return Object.freeze({
    ok: true,
    allowed: true,
    decision: 'allow',
    code: 'RELEASE_ALLOWED',
    data: report,
  });
}

export interface PublicTraceItem {
  readonly eventId: string;
  readonly sampleId: string;
  readonly moduleId: string;
  readonly projection: string;
  readonly publicValue: string;
  readonly empty: boolean;
}

export interface StrategyCall {
  readonly moduleId: string;
  readonly eventId: string;
  readonly sampleId: string;
  readonly projection?: string;
  readonly jointTableId?: string;
}

export type AdaptiveScheduler = (trace: readonly PublicTraceItem[]) => StrategyCall | null;

export interface StrategyReviewInput extends EvaluatePrivacyInput {
  readonly strategy: AdaptiveScheduler;
  readonly maxDepth?: number;
  readonly maxTraces?: number;
}

function modulePublicOutcomes(module: NormalizedWorkModule, policy: ResolvedPrivacyPolicy): string[] {
  const values = new Set<string>();
  for (const secret of policy.secrets) {
    for (const [candidate, probability] of Object.entries(module.tables[secret] ?? {})) {
      if (probability.isZero()) continue;
      values.add(candidate === EMPTY_CANDIDATE ? EMPTY_PUBLIC_VALUE : module.resourceOf[candidate] ?? candidate);
    }
  }
  return [...values].sort();
}

export function reviewAdaptiveStrategy(input: StrategyReviewInput): PrivacyReport {
  const context = createEvaluationContext(input);
  const maxDepth = input.maxDepth ?? 64;
  const maxTraces = input.maxTraces ?? 10_000;
  const traces: TraceReport[] = [];
  const enumerationIssues: PrivacyIssue[] = [];
  let leafCount = 0;
  let visitedNodes = 0;
  let budgetReported = false;

  const walk = (observations: ObservationInput[], publicTrace: PublicTraceItem[], depth: number): void => {
    if (visitedNodes >= maxTraces) {
      if (!budgetReported) {
        enumerationIssues.push({
          code: 'TRACE_LIMIT_EXCEEDED',
          message: `Exhaustive event-tree node limit ${maxTraces} exceeded`,
        });
        budgetReported = true;
      }
      return;
    }
    visitedNodes += 1;
    if (leafCount >= maxTraces) {
      if (!budgetReported) {
        enumerationIssues.push({ code: 'TRACE_LIMIT_EXCEEDED', message: `Exhaustive trace limit ${maxTraces} exceeded` });
        budgetReported = true;
      }
      return;
    }
    if (depth > maxDepth) {
      enumerationIssues.push({ code: 'DEPTH_LIMIT_EXCEEDED', message: `Adaptive tree exceeds depth ${maxDepth}` });
      return;
    }

    let call: StrategyCall | null;
    try {
      call = input.strategy(publicTrace);
    } catch (error) {
      enumerationIssues.push({ code: 'SCHEDULER_ERROR', message: (error as Error).message });
      return;
    }

    if (call === null) {
      traces.push(evaluateTraceWithContext(context, observations, `event-tree-${leafCount + 1}`));
      leafCount += 1;
      return;
    }

    const module = context.modules.get(call.moduleId);
    if (!module) {
      enumerationIssues.push({
        code: 'UNKNOWN_OR_UNTRUSTED_MODULE',
        message: `Scheduler selected unavailable module ${call.moduleId}`,
        eventId: call.eventId,
        sampleId: call.sampleId,
      });
      traces.push(evaluateTraceWithContext(context, observations, `event-tree-error-${leafCount + 1}`));
      leafCount += 1;
      return;
    }

    const projection = call.projection ?? call.moduleId;
    for (const publicValue of modulePublicOutcomes(module, context.policy)) {
      const empty = publicValue === EMPTY_PUBLIC_VALUE;
      const observation: ObservationInput = {
        eventId: call.eventId,
        sampleId: call.sampleId,
        moduleId: call.moduleId,
        projection,
        empty,
        ...(empty ? {} : { resourceId: publicValue }),
        ...(call.jointTableId ? { jointTableId: call.jointTableId } : {}),
      };
      const nextObservations = [...observations, observation];
      const nextPublicTrace: PublicTraceItem[] = [
        ...publicTrace,
        {
          eventId: call.eventId,
          sampleId: call.sampleId,
          moduleId: call.moduleId,
          projection,
          publicValue,
          empty,
        },
      ];
      walk(nextObservations, nextPublicTrace, depth + 1);
    }
  };

  walk(input.observations ?? [], [], 0);
  if (traces.length === 0 && enumerationIssues.length === 0) {
    traces.push(evaluateTraceWithContext(context, input.observations ?? [], 'event-tree-empty'));
  }
  return buildPrivacyReport(context, traces, enumerationIssues);
}

export function releaseAdaptiveStrategy(
  input: StrategyReviewInput,
): ReleaseResult<PrivacyReport> {
  const report = reviewAdaptiveStrategy(input);
  if (!report.allowed) return FIXED_REJECTION;
  return Object.freeze({
    ok: true,
    allowed: true,
    decision: 'allow',
    code: 'RELEASE_ALLOWED',
    data: report,
  });
}
