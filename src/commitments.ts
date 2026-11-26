import { createHash } from 'node:crypto';
import { ModRing, type DomainId, type GenerationId, type Matrix } from './ring.ts';
import {
  canonicalBytes,
  signCanonical,
  type SignatureEnvelope,
} from './signing.ts';

export interface SignedLinearCommitment {
  commitmentId: string;
  generation: GenerationId;
  sender: DomainId;
  round?: number;
  localInputIndex?: number;
  coefficients: number[];
  commitmentValue?: number;
  admissibleInputMap?: Matrix;
  signature: SignatureEnvelope;
}

export interface UnsignedLinearCommitment extends Omit<SignedLinearCommitment, 'signature'> {
  signature?: SignatureEnvelope;
}

export interface CommitmentConflict {
  commitmentId: string;
  sender: DomainId;
  generation: GenerationId;
  first: CommitmentEvidence;
  second: CommitmentEvidence;
  kind: 'coefficients' | 'value';
  equivalentOnAdmissibleInputs: boolean;
  message: string;
}

export interface CommitmentEvidence {
  commitmentId: string;
  source: 'commitment' | 'step';
  sourceId: string;
  sender: DomainId;
  receiver?: DomainId;
  generation: GenerationId;
  round?: number;
  localInputIndex?: number;
  coefficients: number[];
  commitmentValue?: number;
  signature?: SignatureEnvelope;
  admissibleInputMap?: Matrix;
}

export function linearCommitmentPayload(
  commitment: UnsignedLinearCommitment | CommitmentEvidence,
  ring: ModRing,
): Record<string, unknown> {
  return {
    kind: 'modring-linear-commitment/v1',
    p: ring.p,
    e: ring.e,
    modulus: ring.q,
    commitmentId: commitment.commitmentId,
    generation: commitment.generation,
    sender: commitment.sender,
    round: commitment.round ?? null,
    localInputIndex: commitment.localInputIndex ?? null,
    coefficients: ring.normalize(commitment.coefficients),
    commitmentValue:
      commitment.commitmentValue === undefined ? null : ring.norm(commitment.commitmentValue),
  };
}

export function signLinearCommitment(
  ring: ModRing,
  commitment: Omit<UnsignedLinearCommitment, 'signature'>,
  privateKeyPem: string,
  keyId?: string,
  publicKeyPem?: string,
): SignedLinearCommitment {
  const signature = signCanonical(
    linearCommitmentPayload(commitment as UnsignedLinearCommitment, ring),
    privateKeyPem,
    keyId,
    publicKeyPem,
  );
  return { ...commitment, coefficients: ring.normalize(commitment.coefficients), signature };
}

export const signCommitmentForRing = signLinearCommitment;

export function normalizedCoefficients(coefficients: readonly number[], ring: ModRing): number[] {
  return ring.normalize(coefficients);
}

export function effectiveCoefficientsOnInputMap(
  coefficients: readonly number[],
  inputMap: Matrix | undefined,
  ring: ModRing,
): number[] {
  if (!inputMap || inputMap.length === 0) return ring.normalize(coefficients);
  if (coefficients.length !== inputMap.length) {
    throw new RangeError(
      `coefficient/inputMap length mismatch: ${coefficients.length} vs ${inputMap.length}`,
    );
  }
  const outputWidth = inputMap[0]?.length ?? 0;
  return Array.from({ length: outputWidth }, (_, column) => {
    let sum = 0;
    for (let input = 0; input < inputMap.length; input += 1) {
      sum = ring.add(sum, ring.mul(coefficients[input]!, inputMap[input]![column]!));
    }
    return sum;
  });
}

export function formsAreEquivalent(
  first: CommitmentEvidence,
  second: CommitmentEvidence,
  ring: ModRing,
): boolean {
  const firstMap = first.admissibleInputMap ?? second.admissibleInputMap;
  const firstEffective = effectiveCoefficientsOnInputMap(first.coefficients, firstMap, ring);
  const secondEffective = effectiveCoefficientsOnInputMap(second.coefficients, firstMap, ring);
  if (firstEffective.length !== secondEffective.length) return false;
  return firstEffective.every((value, index) => value === secondEffective[index]);
}

export function evidenceFingerprint(evidence: CommitmentEvidence, ring: ModRing): string {
  return JSON.stringify({
    id: evidence.commitmentId,
    coefficients: ring.normalize(evidence.coefficients),
    value:
      evidence.commitmentValue === undefined ? null : ring.norm(evidence.commitmentValue),
  });
}

export function detectEquivocation(
  evidenceList: readonly CommitmentEvidence[],
  ring: ModRing,
): CommitmentConflict[] {
  const groups = new Map<string, CommitmentEvidence[]>();
  for (const evidence of evidenceList) {
    const group = groups.get(evidence.commitmentId) ?? [];
    group.push(evidence);
    groups.set(evidence.commitmentId, group);
  }

  const conflicts: CommitmentConflict[] = [];
  for (const group of groups.values()) {
    for (let leftIndex = 0; leftIndex < group.length; leftIndex += 1) {
      for (let rightIndex = leftIndex + 1; rightIndex < group.length; rightIndex += 1) {
        const first = group[leftIndex]!;
        const second = group[rightIndex]!;
        const sameCoefficients =
          first.coefficients.length === second.coefficients.length &&
          ring.normalize(first.coefficients).every(
            (value, index) => value === ring.norm(second.coefficients[index]!),
          );
        const equivalent = sameCoefficients || formsAreEquivalent(first, second, ring);
        const sameValue =
          first.commitmentValue === undefined ||
          second.commitmentValue === undefined ||
          ring.norm(first.commitmentValue) === ring.norm(second.commitmentValue);

        if (!sameCoefficients && !equivalent) {
          conflicts.push({
            commitmentId: first.commitmentId,
            sender: first.sender,
            generation: first.generation,
            first,
            second,
            kind: 'coefficients',
            equivalentOnAdmissibleInputs: false,
            message:
              `sender ${first.sender} signed contradictory linear combinations for commitment ` +
              `${first.commitmentId} to ${first.receiver ?? '?'} and ${second.receiver ?? '?'}`,
          });
        } else if (!sameValue) {
          conflicts.push({
            commitmentId: first.commitmentId,
            sender: first.sender,
            generation: first.generation,
            first,
            second,
            kind: 'value',
            equivalentOnAdmissibleInputs: equivalent && !sameCoefficients,
            message:
              `sender ${first.sender} signed two unequal opening values for commitment ` +
              `${first.commitmentId}`,
          });
        }
      }
    }
  }
  return conflicts;
}

export function hashCanonical(value: unknown): string {
  return createHash('sha256').update(canonicalBytes(value) as unknown as Uint8Array).digest('hex');
}
