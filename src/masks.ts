import { randomInt } from 'node:crypto';
import { ModRing, type DomainId, type GenerationId } from './ring.ts';
import { smithNormalForm } from './smith.ts';
import type { MaskSpec } from './types.ts';

export interface GeneratedMask extends MaskSpec {
  id: string;
  generation: GenerationId;
  owner?: DomainId;
  value: number;
  independent: true;
}

export function randomRingScalar(ring: ModRing): number {
  return randomInt(0, ring.q);
}

export function generateIndependentMasks(input: {
  ring: ModRing;
  count: number;
  generation: GenerationId;
  idPrefix?: string;
  owner?: DomainId;
}): GeneratedMask[] {
  if (!Number.isInteger(input.count) || input.count < 0) {
    throw new RangeError('mask count must be a non-negative integer');
  }
  const prefix = input.idPrefix ?? 'mask';
  return Array.from({ length: input.count }, (_unused, index) => ({
    id: `${prefix}-${index}`,
    generation: input.generation,
    owner: input.owner,
    value: randomRingScalar(input.ring),
    independent: true as const,
  }));
}

export interface MaskGenerationViolation {
  code: 'cross-generation-mask-replay' | 'duplicate-mask-generation-conflict';
  maskId: string;
  firstGeneration?: GenerationId;
  secondGeneration?: GenerationId;
  stepId?: string;
  message: string;
}

export class MaskGenerationLedger {
  private readonly generations = new Map<string, GenerationId>();

  bind(mask: MaskSpec): MaskGenerationViolation | null {
    if (mask.generation === undefined) return null;
    const existing = this.generations.get(mask.id);
    if (existing !== undefined && existing !== mask.generation) {
      return {
        code: 'duplicate-mask-generation-conflict',
        maskId: mask.id,
        firstGeneration: existing,
        secondGeneration: mask.generation,
        message: `mask ${mask.id} is bound to generations ${String(existing)} and ${String(mask.generation)}`,
      };
    }
    this.generations.set(mask.id, mask.generation);
    return null;
  }

  observe(
    maskId: string,
    generation: GenerationId,
    stepId?: string,
  ): MaskGenerationViolation | null {
    const existing = this.generations.get(maskId);
    if (existing !== undefined && existing !== generation) {
      return {
        code: 'cross-generation-mask-replay',
        maskId,
        firstGeneration: existing,
        secondGeneration: generation,
        stepId,
        message:
          `mask ${maskId} from generation ${String(existing)} was replayed in generation ` +
          `${String(generation)}${stepId ? ` at step ${stepId}` : ''}`,
      };
    }
    this.generations.set(maskId, generation);
    return null;
  }
}

export function validateMaskSpecs(
  masks: readonly MaskSpec[],
  ledger: MaskGenerationLedger = new MaskGenerationLedger(),
): MaskGenerationViolation[] {
  const violations: MaskGenerationViolation[] = [];
  const seen = new Map<string, MaskSpec>();
  for (const mask of masks) {
    const previous = seen.get(mask.id);
    if (previous && previous.generation !== mask.generation) {
      violations.push({
        code: 'duplicate-mask-generation-conflict',
        maskId: mask.id,
        firstGeneration: previous.generation,
        secondGeneration: mask.generation,
        message: `mask id ${mask.id} declared with conflicting generations`,
      });
    }
    seen.set(mask.id, mask);
    const violation = ledger.bind(mask);
    if (violation) violations.push(violation);
  }
  return violations;
}

export interface MaskCancellationWitness {
  rowCombination: number[];
  effectiveForm: number[];
  secretForm: number[];
  trivialRowDependency: boolean;
}

function transpose(matrix: readonly number[][]): number[][] {
  const rows = matrix.length;
  const columns = matrix[0]?.length ?? 0;
  return Array.from({ length: columns }, (_, column) =>
    Array.from({ length: rows }, (_, row) => matrix[row]![column]!),
  );
}

export function findMaskCancellationWitnesses(input: {
  forms: readonly number[][];
  secretCount: number;
  maskCount: number;
  ring: ModRing;
}): MaskCancellationWitness[] {
  const { forms, secretCount, maskCount, ring } = input;
  if (forms.length === 0) return [];
  const width = secretCount + maskCount;
  forms.forEach((form, index) => {
    if (form.length !== width) {
      throw new RangeError(`cancellation form ${index} has width ${form.length}, expected ${width}`);
    }
  });

  const maskColumns = forms.map((form) => form.slice(secretCount));
  const maskTranspose = transpose(maskColumns);
  const leftKernel = smithNormalForm(maskTranspose, ring, forms.length);
  const witnesses: MaskCancellationWitness[] = [];

  for (const rowCombination of leftKernel.kernelGenerators) {
    const normalizedCombination = ring.normalize(rowCombination);
    if (normalizedCombination.every((value) => value === 0)) continue;
    const effectiveForm = new Array<number>(width).fill(0);
    for (let row = 0; row < forms.length; row += 1) {
      const coefficient = normalizedCombination[row]!;
      if (coefficient === 0) continue;
      for (let column = 0; column < width; column += 1) {
        effectiveForm[column] = ring.add(
          effectiveForm[column]!,
          ring.mul(coefficient, forms[row]![column]!),
        );
      }
    }
    const normalizedForm = ring.normalize(effectiveForm);
    const secretForm = normalizedForm.slice(0, secretCount);
    const trivialRowDependency = normalizedForm.every((value) => value === 0);
    witnesses.push({
      rowCombination: normalizedCombination,
      effectiveForm: normalizedForm,
      secretForm,
      trivialRowDependency,
    });
  }

  return witnesses;
}
