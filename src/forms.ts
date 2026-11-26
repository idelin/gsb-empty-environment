import { ModRing, type Matrix } from './ring.ts';
import { maskCoordinate, type VariableLayout } from './types.ts';

export interface FormParts {
  secrets?: readonly number[] | Record<number, number>;
  masks?: Record<string, number>;
}

export function createLinearForm(
  layout: VariableLayout,
  parts: FormParts = {},
  ring?: ModRing,
): number[] {
  const form = new Array<number>(layout.secretCount + layout.masks.length).fill(0);
  if (Array.isArray(parts.secrets)) {
    if (parts.secrets.length !== layout.secretCount) {
      throw new RangeError(
        `secret coefficient length ${parts.secrets.length} does not match secretCount ${layout.secretCount}`,
      );
    }
    for (let index = 0; index < layout.secretCount; index += 1) {
      form[index] = parts.secrets[index]!;
    }
  } else if (parts.secrets) {
    for (const [index, coefficient] of Object.entries(parts.secrets)) {
      form[Number(index)] = coefficient!;
    }
  }
  if (parts.masks) {
    for (const [maskId, coefficient] of Object.entries(parts.masks)) {
      form[maskCoordinate(layout, maskId)] = coefficient;
    }
  }
  return ring ? ring.normalize(form) : form;
}

export function evaluateForm(
  form: readonly number[],
  values: readonly number[],
  ring: ModRing,
): number {
  if (form.length !== values.length) {
    throw new RangeError(`form/value length mismatch: ${form.length} vs ${values.length}`);
  }
  return ring.dot(form, values);
}

export function evaluateShareMatrix(
  shareMatrix: readonly number[][],
  secrets: readonly number[],
  ring: ModRing,
): number[] {
  return shareMatrix.map((row) => ring.dot(row, secrets));
}

export function composeLinearCombination(
  localInputForms: Matrix,
  coefficients: readonly number[],
  ring: ModRing,
): number[] {
  if (localInputForms.length !== coefficients.length) {
    throw new RangeError(
      `local form/coefficient length mismatch: ${localInputForms.length} vs ${coefficients.length}`,
    );
  }
  const width = localInputForms[0]?.length ?? 0;
  const result = new Array<number>(width).fill(0);
  for (let index = 0; index < localInputForms.length; index += 1) {
    const form = localInputForms[index]!;
    if (form.length !== width) {
      throw new RangeError(`local input form ${index} has inconsistent width`);
    }
    const coefficient = coefficients[index]!;
    for (let column = 0; column < width; column += 1) {
      result[column] = ring.add(result[column]!, ring.mul(coefficient, form[column]!));
    }
  }
  return result;
}
