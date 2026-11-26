import { ModRing, type Matrix } from './ring.ts';
import { normalizeMatrix, smithNormalForm } from './smith.ts';

export function assertSameWidth(rows: readonly number[][], width: number): void {
  rows.forEach((row, index) => {
    if (row.length !== width) {
      throw new RangeError(`row ${index} has width ${row.length}, expected ${width}`);
    }
  });
}

export function conditionalImageSize(
  observationForms: readonly number[][],
  protectedForms: Matrix,
  ring: ModRing | number | ConstructorParameters<typeof ModRing>[0],
): bigint {
  const modRing = ring instanceof ModRing ? ring : new ModRing(ring);
  const protectedRows = normalizeMatrix(protectedForms, modRing);
  const width = protectedRows[0]?.length ?? observationForms[0]?.length ?? 0;
  const observations = normalizeMatrix(observationForms, modRing);
  assertSameWidth(observations, width);
  assertSameWidth(protectedRows, width);

  const observationKernel = smithNormalForm(observations, modRing).kernelSize;
  const stacked = normalizeMatrix([...observations, ...protectedRows], modRing);
  const stackedKernel = smithNormalForm(stacked, modRing).kernelSize;
  return observationKernel / stackedKernel;
}

export function conditionalSupportSize(
  observationForms: readonly number[][],
  protectedForm: readonly number[],
  ring: ModRing | number | ConstructorParameters<typeof ModRing>[0],
): bigint {
  return conditionalImageSize(observationForms, [protectedForm.slice()], ring);
}

export function allCoefficientVectors(dimension: number, ring: ModRing): number[][] {
  if (!Number.isInteger(dimension) || dimension < 0) {
    throw new RangeError(`dimension must be a non-negative integer`);
  }
  const total = ring.q ** dimension;
  if (total > 20_000_000) {
    throw new RangeError(`refusing to enumerate ${total} coefficient vectors`);
  }
  const result: number[][] = [];
  for (let flat = 0; flat < total; flat += 1) {
    let remaining = flat;
    const vector = Array.from({ length: dimension }, () => {
      const value = remaining % ring.q;
      remaining = Math.floor(remaining / ring.q);
      return value;
    });
    result.push(vector);
  }
  return result;
}

export function enumerateRingVectors(dimension: number, ring: ModRing): number[][] {
  return allCoefficientVectors(dimension, ring);
}

export interface ExhaustiveSupportResult {
  assignments: number;
  observationGroups: number;
  supportCardinalities: number[];
  minSupport: number;
  maxSupport: number;
  uniform: boolean;
  formulaSize?: bigint;
  formulaMatches?: boolean;
  counterexample?: {
    observation: number[];
    values: number[][];
  };
}

export function exhaustiveConditionalImageSize(input: {
  variableCount: number;
  observationForms: readonly number[][];
  protectedForms: Matrix;
  ring: ModRing | ConstructorParameters<typeof ModRing>[0];
  maxAssignments?: number;
}): ExhaustiveSupportResult {
  const ring = input.ring instanceof ModRing ? input.ring : new ModRing(input.ring);
  const forms = normalizeMatrix(input.observationForms, ring);
  const protectedForms = normalizeMatrix(input.protectedForms, ring);
  const width = input.variableCount;
  assertSameWidth(forms, width);
  assertSameWidth(protectedForms, width);

  const assignments = ring.q ** width;
  const maxAssignments = input.maxAssignments ?? 1_000_000;
  if (assignments > maxAssignments) {
    throw new RangeError(`refusing ${assignments} exhaustive assignments (limit ${maxAssignments})`);
  }

  const groups = new Map<string, Set<string>>();
  const observationValues = new Map<string, number[]>();
  for (const values of enumerateRingVectors(width, ring)) {
    const observation = forms.map((form) => ring.dot(form, values));
    const protectedValue = protectedForms.map((form) => ring.dot(form, values));
    const key = observation.join('|');
    let group = groups.get(key);
    if (!group) {
      group = new Set<string>();
      groups.set(key, group);
      observationValues.set(key, observation);
    }
    group.add(protectedValue.join('|'));
  }

  const supportCardinalities = [...groups.values()].map((values) => values.size);
  const minSupport = Math.min(...supportCardinalities);
  const maxSupport = Math.max(...supportCardinalities);
  const formulaSize = conditionalImageSize(forms, protectedForms, ring);
  const formulaMatches = supportCardinalities.every((size) => BigInt(size) === formulaSize);
  let counterexample: ExhaustiveSupportResult['counterexample'];
  if (!formulaMatches) {
    for (const [key, values] of groups) {
      if (BigInt(values.size) !== formulaSize) {
        counterexample = {
          observation: observationValues.get(key) ?? key.split('|').map(Number),
          values: [...values].map((value) => value.split('|').map(Number)),
        };
        break;
      }
    }
  }

  return {
    assignments,
    observationGroups: groups.size,
    supportCardinalities,
    minSupport,
    maxSupport,
    uniform: minSupport === maxSupport,
    formulaSize,
    formulaMatches,
    counterexample,
  };
}

export function exhaustiveConditionalSupportSize(input: {
  variableCount: number;
  observationForms: readonly number[][];
  protectedForm: readonly number[];
  ring: ModRing | ConstructorParameters<typeof ModRing>[0];
  maxAssignments?: number;
}): ExhaustiveSupportResult {
  return exhaustiveConditionalImageSize({
    variableCount: input.variableCount,
    observationForms: input.observationForms,
    protectedForms: [input.protectedForm.slice()],
    ring: input.ring,
    maxAssignments: input.maxAssignments,
  });
}
