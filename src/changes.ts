/**
 * Change metrics between two ratio values.
 *
 * Two different units exist and must never be conflated:
 *  - 'pp'      : percentage-POINT change, (next - prev) * 100.
 *  - 'percent' : RELATIVE change, (next - prev) / prev * 100.
 *
 * A relative change against a base of exactly zero is undefined
 * (growth from an old value of 0 cannot be expressed as a percentage).
 * A percentage-point change from a zero base remains well defined.
 */

import { HUNDRED, add, div, isZero, mul, sub, type Rational } from './rational.ts';
import type { RatioValue } from './measure.ts';

export type ChangeUnit = 'pp' | 'percent';

export type ChangeUndefinedReason =
  | 'missing-input'
  | 'zero-denominator-input'
  | 'base-zero';

export type ChangeValue =
  | { readonly kind: 'defined'; readonly unit: ChangeUnit; readonly value: Rational }
  | {
      readonly kind: 'undefined';
      readonly unit: ChangeUnit;
      readonly reason: ChangeUndefinedReason;
    };

function guardInputs(
  next: RatioValue,
  prev: RatioValue,
  unit: ChangeUnit,
): ChangeValue | null {
  if (next.kind === 'missing' || prev.kind === 'missing') {
    return { kind: 'undefined', unit, reason: 'missing-input' };
  }
  if (next.kind === 'zero-denominator' || prev.kind === 'zero-denominator') {
    return { kind: 'undefined', unit, reason: 'zero-denominator-input' };
  }
  return null;
}

/** Percentage-point change: (next - prev) * 100, unit 'pp'. */
export function ppChange(next: RatioValue, prev: RatioValue): ChangeValue {
  const invalid = guardInputs(next, prev, 'pp');
  if (invalid) return invalid;
  if (next.kind !== 'value' || prev.kind !== 'value') {
    throw new Error('unreachable');
  }
  const delta = mul(sub(next.value, prev.value), HUNDRED);
  return { kind: 'defined', unit: 'pp', value: delta };
}

/**
 * Relative change: (next - prev) / prev * 100, unit 'percent'.
 * Undefined when the old value is exactly zero.
 */
export function relChange(next: RatioValue, prev: RatioValue): ChangeValue {
  const invalid = guardInputs(next, prev, 'percent');
  if (invalid) return invalid;
  if (next.kind !== 'value' || prev.kind !== 'value') {
    throw new Error('unreachable');
  }
  if (isZero(prev.value)) {
    return { kind: 'undefined', unit: 'percent', reason: 'base-zero' };
  }
  const relative = mul(div(sub(next.value, prev.value), prev.value), HUNDRED);
  return { kind: 'defined', unit: 'percent', value: relative };
}

export class UnitMismatchError extends Error {
  constructor(a: ChangeUnit, b: ChangeUnit) {
    super(`unit mismatch: cannot combine '${a}' with '${b}' (pp != percent)`);
    this.name = 'UnitMismatchError';
  }
}

/**
 * Combining changes is only allowed within the same unit; percentage
 * points and percentages are not interchangeable and this enforces it
 * at runtime as well as in the type lattice.
 */
export function addChanges(a: ChangeValue, b: ChangeValue): ChangeValue {
  if (a.unit !== b.unit) {
    throw new UnitMismatchError(a.unit, b.unit);
  }
  if (a.kind === 'undefined') return a;
  if (b.kind === 'undefined') return b;
  return { kind: 'defined', unit: a.unit, value: add(a.value, b.value) };
}
