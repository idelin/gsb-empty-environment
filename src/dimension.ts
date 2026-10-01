/**
 * Dimension vectors over the seven SI base dimensions.
 * Exponents are small integers; a Number is fine here because an exponent
 * is a shape descriptor, never a measured magnitude.
 */

export const BASE_DIMENSIONS = ['L', 'M', 'T', 'I', 'Θ', 'N', 'J'] as const;
export type BaseDimension = (typeof BASE_DIMENSIONS)[number];

export type DimVector = readonly number[];

export const DIMENSIONLESS: DimVector = Object.freeze([0, 0, 0, 0, 0, 0, 0]);

export function dims(
  partial: Partial<Record<BaseDimension, number>> = {},
): DimVector {
  return Object.freeze(
    BASE_DIMENSIONS.map((name) => partial[name] ?? 0),
  );
}

export function dimsEqual(a: DimVector, b: DimVector): boolean {
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

export function dimsMul(a: DimVector, b: DimVector): DimVector {
  return Object.freeze(a.map((v, i) => v + b[i]));
}

export function dimsDiv(a: DimVector, b: DimVector): DimVector {
  return Object.freeze(a.map((v, i) => v - b[i]));
}

export function dimsPow(a: DimVector, n: number): DimVector {
  return Object.freeze(a.map((v) => v * n));
}

export function isDimensionless(a: DimVector): boolean {
  return a.every((v) => v === 0);
}

export function dimsToString(a: DimVector): string {
  const parts: string[] = [];
  a.forEach((exp, i) => {
    if (exp !== 0) parts.push(exp === 1 ? BASE_DIMENSIONS[i] : `${BASE_DIMENSIONS[i]}^${exp}`);
  });
  return parts.length === 0 ? '1' : parts.join('·');
}
