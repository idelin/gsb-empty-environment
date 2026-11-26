import {
  ModRing,
  type DomainId,
  type GenerationId,
  type Matrix,
  type RingDescriptor,
} from './ring.ts';
import {
  conditionalImageSize,
  exhaustiveConditionalImageSize,
  allCoefficientVectors,
} from './linear.ts';
import { isConsistentSystem, smithNormalForm } from './smith.ts';
import {
  detectEquivocation,
  formsAreEquivalent,
  linearCommitmentPayload,
  type CommitmentConflict,
  type CommitmentEvidence,
  type SignedLinearCommitment,
} from './commitments.ts';
import {
  ed25519PublicKeyVerifier,
  signCanonical,
  type SignatureEnvelope,
  type SignatureVerifier,
} from './signing.ts';
import {
  findMaskCancellationWitnesses,
  MaskGenerationLedger,
  validateMaskSpecs,
  type MaskCancellationWitness,
  type MaskGenerationViolation,
} from './masks.ts';
import type { MaskSpec } from './types.ts';

export interface AdmissionIssue {
  code: string;
  severity: 'error' | 'warning';
  message: string;
  details?: Record<string, unknown>;
}

export interface RepairStep {
  id: string;
  generation: GenerationId;
  round: number;
  sender: DomainId;
  receiver: DomainId;
  localInputIndex: number;
  coefficients: number[];
  value?: number;
  nonce?: string;
  dependsOn: string[];
  priorTranscriptHash?: string;
  commitmentId?: string;
  signature?: SignatureEnvelope;
}

export interface RawRepairStep {
  id?: string;
  stepId?: string;
  generation?: GenerationId;
  epoch?: GenerationId;
  round?: number;
  sender: DomainId;
  receiver: DomainId;
  localInputIndex?: number;
  inputIndex?: number;
  coefficients?: readonly number[];
  coeffs?: readonly number[];
  value?: number;
  nonce?: string;
  dependsOn?: readonly string[];
  priorTranscriptHash?: string;
  commitmentId?: string;
  signature?: SignatureEnvelope;
}

export interface CoalitionSpec {
  label?: string;
  domains: DomainId[];
  knownMaskIds?: string[];
  protectedForms?: Matrix | 'all-secret-forms';
}

export type CoalitionInput = DomainId[] | CoalitionSpec;
export type SignaturePolicy = 'required' | 'if-present' | 'off';
export type SlotConsistencyPolicy = 'commitment-id' | 'sender-round-input' | 'off';
export type ZeroSumPolicy = 'leak-only' | 'forbid-all-derived' | 'off';

export interface AdmissionOptions {
  ring?: number | RingDescriptor | ModRing;
  p?: number;
  e?: number;
  modulus?: number;
  q?: number;
  secretCount?: number;
  masks?: number | readonly (MaskSpec | string)[];
  domains?: DomainId[];
  shareMatrix?: Matrix;
  shareRows?: Record<DomainId, readonly number[]>;
  missingDomains?: DomainId[];
  target?: DomainId;
  targetForms?: Matrix;
  targetKnownForms?: Matrix;
  protectedForms?: Matrix | 'all-secret-forms';
  coalitions?: CoalitionInput[];
  steps?: RawRepairStep[];
  commitments?: SignedLinearCommitment[];
  publicKeys?: Record<DomainId, string>;
  verifier?: SignatureVerifier;
  admissibleInputMap?: Matrix;
  rejectEquivalentRingRepresentations?: boolean;
  signatureVerification?: SignaturePolicy;
  slotConsistency?: SlotConsistencyPolicy;
  zeroSumPolicy?: ZeroSumPolicy;
  coalitionsKnowOwnedMasks?: boolean;
  coalitionVisibility?: 'both-endpoints' | 'received-by-coalition';
  exhaustive?: boolean;
  exhaustiveMaxAssignments?: number;
  maxLinearForms?: number;
  checkTranscriptValues?: boolean;
  originalShareMatrix?: Matrix;
  generationMatrix?: Matrix;
  repairSteps?: RawRepairStep[];
  signedSteps?: RawRepairStep[];
  colludingSets?: CoalitionInput[];
  collusions?: CoalitionInput[];
  protectedCoefficients?: Matrix | 'all-secret-forms';
  protectedLinearForms?: Matrix | 'all-secret-forms';
  targetDomain?: DomainId;
  domainPublicKeys?: Record<DomainId, string>;
}

export interface FormCardinality {
  form: number[];
  beforeSize: bigint;
  afterSize: bigint;
  shrinks: boolean;
}

export interface CoalitionAdmission {
  label: string;
  domains: DomainId[];
  beforeForms: number[][];
  afterForms: number[][];
  observationFormCount: number;
  beforeFormCount: number;
  forms: FormCardinality[];
  leaks: FormCardinality[];
  cancellationWitnesses: MaskCancellationWitness[];
  issues: AdmissionIssue[];
  exhaustive?: unknown;
}

export interface TargetAdmission {
  forms: number[][];
  observationForms: number[][];
  supportSizes: bigint[];
  finalRound: number;
  roundSupport: Array<{
    round: number;
    observationFormCount: number;
    supportSizes: bigint[];
    reconstructible: boolean;
  }>;
  reconstructible: boolean;
  issues: AdmissionIssue[];
  exhaustive?: unknown;
}

export interface AdmissionResult {
  admitted: boolean;
  valid: boolean;
  ok: boolean;
  accepted: boolean;
  allowed: boolean;
  passed: boolean;
  issues: AdmissionIssue[];
  errors: AdmissionIssue[];
  warnings: AdmissionIssue[];
  ring: { p: number; e: number; q: number };
  steps: RepairStep[];
  target?: TargetAdmission;
  coalitions: CoalitionAdmission[];
  equivocations: CommitmentConflict[];
  maskViolations: MaskGenerationViolation[];
}

export class RepairAdmissionError extends Error {
  readonly result: AdmissionResult;
  constructor(result: AdmissionResult) {
    super(
      `modular repair admission rejected with ${result.errors.length} error(s): ` +
        result.errors.map((issue) => `[${issue.code}] ${issue.message}`).join('; '),
    );
    this.name = 'RepairAdmissionError';
    this.result = result;
  }
}

export function normalizeRepairStep(
  raw: RawRepairStep,
  index: number,
  ring: ModRing,
): RepairStep {
  const generation = raw.generation ?? raw.epoch;
  if (generation === undefined) {
    throw new RangeError(`step at index ${index} is missing generation/epoch`);
  }
  const coefficients = raw.coefficients ?? raw.coeffs;
  if (!coefficients) {
    throw new RangeError(`step at index ${index} is missing coefficients/coeffs`);
  }
  const round = raw.round ?? 0;
  if (!Number.isInteger(round) || round < 0) {
    throw new RangeError(`step ${raw.id ?? index} has invalid round ${round}`);
  }
  return {
    id: raw.id ?? raw.stepId ?? `step-${index}`,
    generation,
    round,
    sender: raw.sender,
    receiver: raw.receiver,
    localInputIndex: raw.localInputIndex ?? raw.inputIndex ?? 0,
    coefficients: ring.normalize(coefficients),
    value: raw.value === undefined ? undefined : ring.norm(raw.value),
    nonce: raw.nonce,
    dependsOn: [...(raw.dependsOn ?? [])],
    priorTranscriptHash: raw.priorTranscriptHash,
    commitmentId: raw.commitmentId,
    signature: raw.signature,
  };
}

export function repairStepSigningPayload(step: RepairStep | RawRepairStep, ring: ModRing) {
  const normalized = normalizeRepairStep(step as RawRepairStep, 0, ring);
  return {
    kind: 'modring-repair-step/v1',
    p: ring.p,
    e: ring.e,
    modulus: ring.q,
    id: normalized.id,
    generation: normalized.generation,
    round: normalized.round,
    sender: normalized.sender,
    receiver: normalized.receiver,
    localInputIndex: normalized.localInputIndex,
    coefficients: normalized.coefficients,
    value: normalized.value ?? null,
    nonce: normalized.nonce ?? null,
    dependsOn: [...normalized.dependsOn].sort(),
    priorTranscriptHash: normalized.priorTranscriptHash ?? null,
    commitmentId: normalized.commitmentId ?? null,
  };
}

export function signRepairStep(
  ring: ModRing,
  step: RawRepairStep,
  privateKeyPem: string,
  keyId?: string,
  publicKeyPem?: string,
): RepairStep {
  const normalized = normalizeRepairStep(step, 0, ring);
  const signature = signCanonical(
    repairStepSigningPayload(normalized, ring),
    privateKeyPem,
    keyId,
    publicKeyPem,
  );
  return { ...normalized, signature };
}

function addIssue(
  issues: AdmissionIssue[],
  code: string,
  message: string,
  severity: AdmissionIssue['severity'] = 'error',
  details?: Record<string, unknown>,
): void {
  issues.push({ code, message, severity, details });
}

function asRing(options: AdmissionOptions): ModRing {
  if (options.ring instanceof ModRing) return options.ring;
  if (options.ring !== undefined) return new ModRing(options.ring);
  return new ModRing({
    p: options.p,
    e: options.e,
    modulus: options.modulus ?? options.q,
  });
}

function expandSecretForm(
  form: readonly number[],
  secretCount: number,
  variableCount: number,
  ring: ModRing,
): number[] {
  if (form.length === variableCount) return ring.normalize(form);
  if (form.length === secretCount) {
    return ring.normalize([...form, ...new Array<number>(variableCount - secretCount).fill(0)]);
  }
  throw new RangeError(
    `linear form width ${form.length} must be either secretCount=${secretCount} or variableCount=${variableCount}`,
  );
}

function domainShareForm(
  domain: DomainId,
  options: AdmissionOptions,
  domainIndex: Map<DomainId, number>,
  variableCount: number,
  ring: ModRing,
): number[] {
  const secretCount = options.secretCount ?? 0;
  if (options.shareRows?.[domain]) {
    return expandSecretForm(options.shareRows[domain]!, secretCount, variableCount, ring);
  }
  const index = domainIndex.get(domain);
  if (index === undefined || !options.shareMatrix?.[index]) {
    throw new RangeError(`no original share row found for domain ${domain}`);
  }
  return expandSecretForm(
    options.shareMatrix[index]!,
    secretCount,
    variableCount,
    ring,
  );
}

function normalizeCoalition(input: CoalitionInput, index: number): CoalitionSpec {
  if (Array.isArray(input)) return { label: `coalition-${index + 1}`, domains: input };
  return { ...input, label: input.label ?? `coalition-${index + 1}` };
}

function commitmentEvidenceFromSteps(
  steps: readonly RepairStep[],
  admissibleInputMap?: Matrix,
): CommitmentEvidence[] {
  return steps
    .filter((step) => step.commitmentId !== undefined)
    .map((step) => ({
      commitmentId: step.commitmentId!,
      source: 'step' as const,
      sourceId: step.id,
      sender: step.sender,
      receiver: step.receiver,
      generation: step.generation,
      round: step.round,
      localInputIndex: step.localInputIndex,
      coefficients: step.coefficients,
      commitmentValue: step.value,
      signature: step.signature,
      admissibleInputMap,
    }));
}

function commitmentEvidenceFromSigned(
  commitments: readonly SignedLinearCommitment[],
): CommitmentEvidence[] {
  return commitments.map((commitment, index) => ({
    commitmentId: commitment.commitmentId,
    source: 'commitment',
    sourceId: `commitment-${index}-${commitment.commitmentId}`,
    sender: commitment.sender,
    generation: commitment.generation,
    round: commitment.round,
    localInputIndex: commitment.localInputIndex,
    coefficients: commitment.coefficients,
    commitmentValue: commitment.commitmentValue,
    signature: commitment.signature,
    admissibleInputMap: commitment.admissibleInputMap,
  }));
}

function checkDependencies(steps: readonly RepairStep[], issues: AdmissionIssue[]): void {
  const byId = new Map(steps.map((step) => [step.id, step]));
  for (const step of steps) {
    for (const dependency of step.dependsOn) {
      const prerequisite = byId.get(dependency);
      if (!prerequisite) {
        addIssue(issues, 'unknown-step-dependency', `step ${step.id} depends on missing ${dependency}`, 'error', {
          stepId: step.id,
          dependency,
        });
      } else if (prerequisite.round >= step.round) {
        addIssue(
          issues,
          'invalid-adaptive-round',
          `step ${step.id} in round ${step.round} depends on ${dependency} in round ${prerequisite.round}`,
          'error',
        );
      }
    }
  }
}

function checkSlotConsistency(
  steps: readonly RepairStep[],
  policy: SlotConsistencyPolicy,
  ring: ModRing,
  admissibleInputMap: Matrix | undefined,
  rejectEquivalentRepresentations: boolean,
  issues: AdmissionIssue[],
): void {
  if (policy === 'off') return;
  const groups = new Map<string, RepairStep[]>();
  for (const step of steps) {
    if (policy === 'commitment-id' && !step.commitmentId) continue;
    const key =
      policy === 'commitment-id'
        ? `commitment:${step.commitmentId}`
        : `slot:${String(step.generation)}:${step.sender}:${step.round}:${step.localInputIndex}`;
    const group = groups.get(key) ?? [];
    group.push(step);
    groups.set(key, group);
  }
  for (const group of groups.values()) {
    for (let i = 1; i < group.length; i += 1) {
      const first = group[0]!;
      const second = group[i]!;
      const same =
        first.coefficients.length === second.coefficients.length &&
        first.coefficients.every((value, index) => value === second.coefficients[index]);
      if (!same) {
        const firstEvidence: CommitmentEvidence = {
          commitmentId: `slot:${first.id}`,
          source: 'step',
          sourceId: first.id,
          sender: first.sender,
          receiver: first.receiver,
          generation: first.generation,
          round: first.round,
          localInputIndex: first.localInputIndex,
          coefficients: first.coefficients,
          admissibleInputMap,
        };
        const secondEvidence: CommitmentEvidence = {
          ...firstEvidence,
          sourceId: second.id,
          receiver: second.receiver,
          coefficients: second.coefficients,
        };
        const equivalent = formsAreEquivalent(firstEvidence, secondEvidence, ring);
        if (equivalent && !rejectEquivalentRepresentations) {
          issues.push({
            code: 'ambiguous-equivalent-ring-form',
            severity: 'warning',
            message:
              `sender ${first.sender} used coefficient representations that differ but are equivalent ` +
              `on the admissible modular input module`,
            details: {
              firstStepId: first.id,
              secondStepId: second.id,
              firstCoefficients: first.coefficients,
              secondCoefficients: second.coefficients,
            },
          });
        } else {
          issues.push({
            code: equivalent
              ? 'rejected-equivalent-ring-representation'
              : 'equivocating-slot-combination',
            severity: 'error',
            message:
              `sender ${first.sender} used inconsistent modular linear combinations in the same ` +
              `bound slot: ${first.id} -> ${first.receiver} vs ${second.id} -> ${second.receiver}`,
            details: {
            firstStepId: first.id,
            secondStepId: second.id,
            firstReceiver: first.receiver,
            secondReceiver: second.receiver,
            firstCoefficients: first.coefficients,
            secondCoefficients: second.coefficients,
              equivalentOnAdmissibleInputs: equivalent,
            },
          });
        }
      }
    }
  }
}

function formsToCheck(
  requested: Matrix | 'all-secret-forms' | undefined,
  fallback: Matrix | 'all-secret-forms' | undefined,
  secretCount: number,
  ring: ModRing,
  maxLinearForms: number,
): number[][] {
  const source = requested ?? fallback ?? 'all-secret-forms';
  if (source === 'all-secret-forms') {
    const forms = allCoefficientVectors(secretCount, ring);
    if (forms.length > maxLinearForms) {
      throw new RangeError(
        `all-secret-forms produced ${forms.length} forms; increase maxLinearForms or provide protectedForms`,
      );
    }
    return forms;
  }
  return source.map((form) => ring.normalize(form));
}

export function admitRepairPlan(options: AdmissionOptions): AdmissionResult {
  options.shareMatrix ??= options.originalShareMatrix ?? options.generationMatrix;
  options.steps ??= options.repairSteps ?? options.signedSteps;
  options.coalitions ??= options.colludingSets ?? options.collusions;
  options.protectedForms ??= options.protectedCoefficients ?? options.protectedLinearForms;
  options.target ??= options.targetDomain;
  options.publicKeys ??= options.domainPublicKeys;

  const ring = asRing(options);
  const issues: AdmissionIssue[] = [];
  if (options.secretCount === undefined) {
    const firstShareRow =
      options.shareMatrix?.[0] ?? Object.values(options.shareRows ?? {})[0];
    (options as { secretCount?: number }).secretCount = firstShareRow?.length;
  }
  const secretCount = options.secretCount ?? 0;
  const maskSpecs: MaskSpec[] =
    typeof options.masks === 'number'
      ? Array.from({ length: options.masks }, (_unused, index) => ({ id: `mask-${index}` }))
      : (options.masks ?? []).map((mask, index) =>
          typeof mask === 'string' ? { id: mask } : { ...mask, id: mask.id ?? `mask-${index}` },
        );
  const variableCount = secretCount + maskSpecs.length;
  const signaturePolicy = options.signatureVerification ?? 'required';
  const slotPolicy = options.slotConsistency ?? 'commitment-id';
  const zeroSumPolicy = options.zeroSumPolicy ?? 'leak-only';
  const maxLinearForms = options.maxLinearForms ?? 20_000;

  if (!Number.isInteger(secretCount) || secretCount < 1) {
    addIssue(
      issues,
      'invalid-secret-count',
      'secretCount must be a positive integer or inferable from shareMatrix',
    );
  }

  for (const mask of maskSpecs) {
    if (mask.independent === false) {
      addIssue(issues, 'mask-not-independent', `mask ${mask.id} is explicitly marked non-independent`);
    }
  }

  const ledger = new MaskGenerationLedger();
  const maskViolations = validateMaskSpecs(maskSpecs, ledger);
  for (const violation of maskViolations) {
    addIssue(issues, violation.code, violation.message, 'error', violation as unknown as Record<string, unknown>);
  }

  let steps: RepairStep[] = [];
  try {
    steps = (options.steps ?? []).map((raw, index) => {
      try {
        const normalized = normalizeRepairStep(raw, index, ring);
        if (normalized.coefficients.length !== variableCount) {
          throw new RangeError(
            `step ${normalized.id} coefficient width ${normalized.coefficients.length} does not match ` +
              `secretCount + masks = ${variableCount}`,
          );
        }
        return normalized;
      } catch (error) {
        addIssue(issues, 'invalid-step', (error as Error).message, 'error', { index });
        return null as unknown as RepairStep;
      }
    }).filter((step): step is RepairStep => step !== null);
  } catch (error) {
    addIssue(issues, 'invalid-step-input', (error as Error).message);
  }

  checkDependencies(steps, issues);

  const verifier: SignatureVerifier | null =
    options.verifier ??
    (options.publicKeys ? ed25519PublicKeyVerifier(options.publicKeys) : null);

  if (signaturePolicy !== 'off') {
    if (!verifier) {
      addIssue(
        issues,
        'no-signature-verifier',
        'signature verification is enabled but neither publicKeys nor a verifier was provided',
      );
    } else {
      for (const step of steps) {
        if (!step.signature) {
          if (signaturePolicy === 'required') {
            addIssue(issues, 'missing-step-signature', `repair step ${step.id} is not signed`);
          }
          continue;
        }
        const payload = repairStepSigningPayload(step, ring);
        let valid = false;
        try {
          valid = verifier({ domain: step.sender, payload, envelope: step.signature });
        } catch (error) {
          addIssue(issues, 'signature-verifier-error', (error as Error).message, 'error', {
            stepId: step.id,
          });
        }
        if (!valid) {
          addIssue(issues, 'invalid-step-signature', `invalid signature for step ${step.id} from ${step.sender}`);
        }
      }

      for (const commitment of options.commitments ?? []) {
        if (!commitment.signature) {
          if (signaturePolicy === 'required') {
            addIssue(
              issues,
              'missing-commitment-signature',
              `linear commitment ${commitment.commitmentId} is not signed`,
            );
          }
          continue;
        }
        const payload = linearCommitmentPayload(commitment, ring);
        const valid = verifier({
          domain: commitment.sender,
          payload,
          envelope: commitment.signature,
        });
        if (!valid) {
          addIssue(
            issues,
            'invalid-commitment-signature',
            `invalid signature for commitment ${commitment.commitmentId} from ${commitment.sender}`,
          );
        }
      }
    }
  }

  for (const step of steps) {
    for (let maskIndex = 0; maskIndex < maskSpecs.length; maskIndex += 1) {
      const coordinate = secretCount + maskIndex;
      if (step.coefficients[coordinate] !== 0) {
        const mask = maskSpecs[maskIndex]!;
        const violation = ledger.observe(mask.id, step.generation, step.id);
        if (violation) {
          maskViolations.push(violation);
          addIssue(issues, violation.code, violation.message, 'error', {
            maskId: mask.id,
            stepId: step.id,
          });
        }
      }
    }
  }

  checkSlotConsistency(
    steps,
    slotPolicy,
    ring,
    options.admissibleInputMap,
    options.rejectEquivalentRingRepresentations ?? false,
    issues,
  );

  const evidence = [
    ...commitmentEvidenceFromSigned(options.commitments ?? []),
    ...commitmentEvidenceFromSteps(steps, options.admissibleInputMap),
  ];
  const equivocations = detectEquivocation(evidence, ring);
  for (const conflict of equivocations) {
    addIssue(issues, 'equivocating-linear-commitment', conflict.message, 'error', {
      commitmentId: conflict.commitmentId,
      firstSource: conflict.first.sourceId,
      secondSource: conflict.second.sourceId,
      kind: conflict.kind,
    });
  }

  const domainIndex = new Map((options.domains ?? []).map((domain, index) => [domain, index]));
  const missingDomains = new Set(options.missingDomains ?? (options.target ? [options.target] : []));

  if (options.checkTranscriptValues !== false) {
    const valuedSteps = steps.filter((step) => step.value !== undefined);
    if (valuedSteps.length > 0) {
      const consistent = isConsistentSystem(
        valuedSteps.map((step) => step.coefficients),
        valuedSteps.map((step) => step.value!),
        ring,
      );
      if (!consistent) {
        addIssue(
          issues,
          'inconsistent-signed-values',
          'signed repair step values are not jointly consistent over the modular ring',
          'error',
          { stepIds: valuedSteps.map((step) => step.id) },
        );
      }
    }
  }
  if (options.shareMatrix && options.domains && options.shareMatrix.length !== options.domains.length) {
    addIssue(
      issues,
      'share-matrix-domain-mismatch',
      `shareMatrix has ${options.shareMatrix.length} rows but domains has ${options.domains.length}`,
    );
  }

  let target: TargetAdmission | undefined;
  if (options.target || options.targetForms) {
    const targetIssues: AdmissionIssue[] = [];
    let targetForms: number[][] = [];
    try {
      if (options.targetForms) {
        targetForms = options.targetForms.map((form) =>
          expandSecretForm(form, secretCount, variableCount, ring),
        );
      } else if (options.target) {
        targetForms = [
          domainShareForm(options.target, options, domainIndex, variableCount, ring),
        ];
      }
      } catch (error) {
        addIssue(targetIssues, 'target-form-error', (error as Error).message);
      }
      if (targetForms.length === 0) {
        addIssue(targetIssues, 'target-form-missing', 'no target share linear form was provided or resolved');
      }

    const targetKnownForms = (options.targetKnownForms ?? []).map((form) =>
      expandSecretForm(form, secretCount, variableCount, ring),
    );
    const targetSteps = steps.filter((step) => step.receiver === options.target);
    const targetObservationForms = [
      ...targetKnownForms,
      ...targetSteps.map((step) => step.coefficients),
    ];

    const supportSizes: bigint[] = [];
    for (const form of targetForms) {
      try {
        const size = conditionalImageSize(targetObservationForms, [form], ring);
        supportSizes.push(size);
        if (size !== 1n) {
          addIssue(
            targetIssues,
            'target-not-uniquely-reconstructible',
            `target ${options.target ?? '(forms)'} has conditional support cardinality ${size.toString()} instead of 1`,
            'error',
            { supportSize: size.toString() },
          );
        }
      } catch (error) {
        addIssue(targetIssues, 'target-rank-error', (error as Error).message);
      }
    }

    const rounds = [...new Set(targetSteps.map((step) => step.round))].sort((a, b) => a - b);
    const effectiveRounds = rounds.length > 0 ? rounds : [0];
    const roundSupport = effectiveRounds.map((round) => {
      const observationForms = [
        ...targetKnownForms,
        ...targetSteps.filter((step) => step.round <= round).map((step) => step.coefficients),
      ];
      const sizes = targetForms.map((form) =>
        conditionalImageSize(observationForms, [form], ring),
      );
      return {
        round,
        observationFormCount: observationForms.length,
        supportSizes: sizes,
        reconstructible: sizes.length > 0 && sizes.every((size) => size === 1n),
      };
    });
    const finalRound = rounds.at(-1) ?? 0;

    let exhaustive: unknown;
    if (options.exhaustive && targetForms.length > 0) {
      try {
        exhaustive = exhaustiveConditionalImageSize({
          variableCount,
          observationForms: targetObservationForms,
          protectedForms: targetForms,
          ring,
          maxAssignments: options.exhaustiveMaxAssignments ?? 2_000_000,
        });
        const result = exhaustive as { minSupport: number; maxSupport: number; formulaMatches?: boolean };
        if (result.minSupport !== 1 || result.maxSupport !== 1) {
          addIssue(targetIssues, 'exhaustive-target-ambiguity', 'exhaustive enumeration found non-unique target reconstruction');
        }
        if (result.formulaMatches === false) {
          addIssue(targetIssues, 'exhaustive-formula-mismatch', 'Smith cardinality does not match exhaustive target enumeration');
        }
      } catch (error) {
        addIssue(targetIssues, 'exhaustive-check-failed', (error as Error).message);
      }
    }

    target = {
      forms: targetForms,
      observationForms: targetObservationForms,
      supportSizes,
      finalRound,
      roundSupport,
      reconstructible: supportSizes.length > 0 && supportSizes.every((size) => size === 1n),
      issues: targetIssues,
      exhaustive,
    };
    issues.push(...targetIssues);
  }

  const coalitions: CoalitionAdmission[] = [];
  for (let coalitionIndex = 0; coalitionIndex < (options.coalitions ?? []).length; coalitionIndex += 1) {
    const spec = normalizeCoalition(options.coalitions![coalitionIndex]!, coalitionIndex);
    const coalitionIssues: AdmissionIssue[] = [];
    const beforeForms: number[][] = [];

    for (const domain of spec.domains) {
      if (missingDomains.has(domain)) continue;
      try {
        beforeForms.push(domainShareForm(domain, options, domainIndex, variableCount, ring));
      } catch (error) {
        addIssue(coalitionIssues, 'coalition-share-error', (error as Error).message, 'error', {
          domain,
        });
      }
    }

    const knownMaskIds = new Set(spec.knownMaskIds ?? []);
    if (options.coalitionsKnowOwnedMasks !== false) {
      for (const mask of maskSpecs) {
        if (mask.owner && spec.domains.includes(mask.owner)) knownMaskIds.add(mask.id);
      }
    }
    for (const maskId of knownMaskIds) {
      const coordinate = maskSpecs.findIndex((mask) => mask.id === maskId);
      if (coordinate < 0) {
        addIssue(coalitionIssues, 'unknown-known-mask', `coalition references unknown mask ${maskId}`);
        continue;
      }
      const form = new Array<number>(variableCount).fill(0);
      form[secretCount + coordinate] = 1;
      beforeForms.push(form);
    }

    const visibleSteps = steps.filter((step) => {
      if (spec.domains.includes(step.receiver)) return true;
      if (options.coalitionVisibility === 'received-by-coalition') return false;
      return spec.domains.includes(step.sender);
    });
    const afterForms = [...beforeForms, ...visibleSteps.map((step) => step.coefficients)];

    let protectedForms: number[][];
    try {
      protectedForms = formsToCheck(
        spec.protectedForms,
        options.protectedForms,
        secretCount,
        ring,
        maxLinearForms,
      ).map((form) => expandSecretForm(form, secretCount, variableCount, ring));
    } catch (error) {
      addIssue(coalitionIssues, 'protected-form-error', (error as Error).message);
      protectedForms = [];
    }

    const forms: FormCardinality[] = [];
    const leaks: FormCardinality[] = [];
    const beforeKernelSize = smithNormalForm(beforeForms, ring).kernelSize;
    const afterKernelSize = smithNormalForm(afterForms, ring).kernelSize;
    for (const protectedForm of protectedForms) {
      try {
        const beforeStackedKernel = smithNormalForm(
          [...beforeForms, protectedForm],
          ring,
        ).kernelSize;
        const afterStackedKernel = smithNormalForm(
          [...afterForms, protectedForm],
          ring,
        ).kernelSize;
        const beforeSize = beforeKernelSize / beforeStackedKernel;
        const afterSize = afterKernelSize / afterStackedKernel;
        const cardinality: FormCardinality = {
          form: protectedForm,
          beforeSize,
          afterSize,
          shrinks: afterSize < beforeSize,
        };
        forms.push(cardinality);
        if (cardinality.shrinks) {
          leaks.push(cardinality);
          addIssue(
            coalitionIssues,
            'protected-linear-distribution-shrinks',
            `coalition ${spec.label} shrinks conditional support for protected form ${JSON.stringify(
              protectedForm.slice(0, secretCount),
            )} from ${beforeSize.toString()} to ${afterSize.toString()}`,
            'error',
            {
              beforeSize: beforeSize.toString(),
              afterSize: afterSize.toString(),
              form: protectedForm,
            },
          );
        }
      } catch (error) {
        addIssue(coalitionIssues, 'conditional-cardinality-error', (error as Error).message);
      }
    }

    let cancellationWitnesses: MaskCancellationWitness[] = [];
    if (zeroSumPolicy !== 'off' && maskSpecs.length > 0) {
      cancellationWitnesses = findMaskCancellationWitnesses({
        forms: afterForms,
        secretCount,
        maskCount: maskSpecs.length,
        ring,
      }).filter((witness) => !witness.trivialRowDependency);

      for (const witness of cancellationWitnesses) {
        if (witness.secretForm.every((value) => value === 0)) continue;
        const fullSecretForm = expandSecretForm(
          witness.secretForm,
          secretCount,
          variableCount,
          ring,
        );
        const beforeSize = conditionalImageSize(beforeForms, [fullSecretForm], ring);
        const afterSize = conditionalImageSize(afterForms, [fullSecretForm], ring);
        const shrinks = afterSize < beforeSize;
        if (shrinks || zeroSumPolicy === 'forbid-all-derived') {
          addIssue(
            coalitionIssues,
            'zero-sum-mask-cancellation',
            shrinks
              ? `coalition ${spec.label} can zero-sum cancel masks and shrink protected support`
              : `coalition ${spec.label} can derive a mask-free linear form by zero-sum cancellation`,
            'error',
            {
              rowCombination: witness.rowCombination,
              effectiveForm: witness.effectiveForm,
              beforeSize: beforeSize.toString(),
              afterSize: afterSize.toString(),
            },
          );
        }
      }
    }

    let exhaustive: unknown;
    if (options.exhaustive) {
      const exhaustiveResults: unknown[] = [];
      for (const protectedForm of protectedForms.slice(0, 32)) {
        try {
          const result = exhaustiveConditionalImageSize({
            variableCount,
            observationForms: afterForms,
            protectedForms: [protectedForm],
            ring,
            maxAssignments: options.exhaustiveMaxAssignments ?? 2_000_000,
          });
          exhaustiveResults.push(result);
          if (result.formulaMatches === false) {
            addIssue(coalitionIssues, 'exhaustive-formula-mismatch', 'exhaustive coalition enumeration disagrees with Smith cardinality');
          }
        } catch (error) {
          addIssue(coalitionIssues, 'exhaustive-check-failed', (error as Error).message);
        }
      }
      exhaustive = exhaustiveResults;
    }

    const coalition: CoalitionAdmission = {
      label: spec.label ?? `coalition-${coalitionIndex + 1}`,
      domains: spec.domains,
      beforeForms,
      afterForms,
      beforeFormCount: beforeForms.length,
      observationFormCount: afterForms.length,
      forms,
      leaks,
      cancellationWitnesses,
      issues: coalitionIssues,
      exhaustive,
    };
    coalitions.push(coalition);
    issues.push(...coalitionIssues);
  }

  const errors = issues.filter((issue) => issue.severity === 'error');
  const warnings = issues.filter((issue) => issue.severity === 'warning');
  return {
    admitted: errors.length === 0,
    valid: errors.length === 0,
    ok: errors.length === 0,
    accepted: errors.length === 0,
    allowed: errors.length === 0,
    passed: errors.length === 0,
    issues,
    errors,
    warnings,
    ring: { p: ring.p, e: ring.e, q: ring.q },
    steps,
    target,
    coalitions,
    equivocations,
    maskViolations,
  };
}

export function assertAdmitted(result: AdmissionResult): void {
  if (!result.admitted) throw new RepairAdmissionError(result);
}

export function admitOrThrow(options: AdmissionOptions): AdmissionResult {
  const result = admitRepairPlan(options);
  assertAdmitted(result);
  return result;
}
