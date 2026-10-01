/**
 * Observations, calibers (metric definition versions) and aggregation.
 *
 * Core rules:
 *  - Aggregation sums raw quantities first (sum of numerators / sum of
 *    denominators). It never averages per-record percentages.
 *  - Records of different calibers (metric id or version) must not be
 *    mixed: attempting to do so throws CaliberMismatchError.
 *  - Missing (unknown) fields propagate and poison the ratio.
 *  - Retracted fields (e.g. a withdrawn denominator) are deliberately
 *    excluded from sums, counted for audit, and do NOT poison the ratio.
 *  - A ratio is computed from paired sums: only records with both
 *    fields present contribute.
 */

import { rational, type Rational } from './rational.ts';

export interface MetricRef {
  readonly id: string;
  readonly version: string;
}

export function metricRef(id: string, version: string): MetricRef {
  return { id, version };
}

export function metricKey(metric: MetricRef): string {
  return `${metric.id}@${metric.version}`;
}

export function sameMetric(a: MetricRef, b: MetricRef): boolean {
  return a.id === b.id && a.version === b.version;
}

export class CaliberMismatchError extends Error {
  constructor(a: MetricRef, b: MetricRef) {
    super(
      `caliber mismatch: cannot mix ${metricKey(a)} with ${metricKey(b)}`,
    );
    this.name = 'CaliberMismatchError';
  }
}

export type FieldState =
  | { readonly status: 'present'; readonly value: bigint }
  | { readonly status: 'missing' }
  | { readonly status: 'retracted' };

export function present(value: bigint): FieldState {
  return { status: 'present', value };
}

export function missingField(): FieldState {
  return { status: 'missing' };
}

export function retractedField(): FieldState {
  return { status: 'retracted' };
}

export interface Observation {
  readonly id: string;
  readonly metric: MetricRef;
  readonly numerator: FieldState;
  readonly denominator: FieldState;
}

/** Convenience constructor for a fully-present observation. */
export function obs(
  id: string,
  metric: MetricRef,
  numerator: bigint,
  denominator: bigint,
): Observation {
  return { id, metric, numerator: present(numerator), denominator: present(denominator) };
}

export interface Aggregate {
  readonly metric: MetricRef;
  /** total records seen */
  readonly total: number;
  /** records with both fields present; basis of the sums below */
  readonly pairCount: number;
  /** sum of numerators over paired records */
  readonly numeratorSum: bigint;
  /** sum of denominators over paired records */
  readonly denominatorSum: bigint;
  readonly numeratorMissing: number;
  readonly denominatorMissing: number;
  readonly numeratorRetracted: number;
  readonly denominatorRetracted: number;
  /** records present on exactly one side (excluded from sums) */
  readonly unpaired: number;
}

function emptyAggregate(metric: MetricRef): Aggregate {
  return {
    metric,
    total: 0,
    pairCount: 0,
    numeratorSum: 0n,
    denominatorSum: 0n,
    numeratorMissing: 0,
    denominatorMissing: 0,
    numeratorRetracted: 0,
    denominatorRetracted: 0,
    unpaired: 0,
  };
}

export function aggregate(observations: readonly Observation[]): Aggregate {
  if (observations.length === 0) {
    throw new RangeError('aggregate: at least one observation is required');
  }
  const metric = observations[0].metric;
  const acc = { ...emptyAggregate(metric) } as {
    -readonly [K in keyof Aggregate]: Aggregate[K];
  };
  for (const o of observations) {
    if (!sameMetric(o.metric, metric)) {
      throw new CaliberMismatchError(metric, o.metric);
    }
    acc.total += 1;
    if (o.numerator.status === 'missing') acc.numeratorMissing += 1;
    if (o.numerator.status === 'retracted') acc.numeratorRetracted += 1;
    if (o.denominator.status === 'missing') acc.denominatorMissing += 1;
    if (o.denominator.status === 'retracted') acc.denominatorRetracted += 1;
    if (o.numerator.status === 'present' && o.denominator.status === 'present') {
      acc.pairCount += 1;
      acc.numeratorSum += o.numerator.value;
      acc.denominatorSum += o.denominator.value;
    } else if (o.numerator.status === 'present' || o.denominator.status === 'present') {
      acc.unpaired += 1;
    }
  }
  return acc;
}

/** Multi-level aggregation: merge pre-aggregated partials. */
export function mergeAggregates(parts: readonly Aggregate[]): Aggregate {
  if (parts.length === 0) {
    throw new RangeError('mergeAggregates: at least one part is required');
  }
  const metric = parts[0].metric;
  const acc = { ...emptyAggregate(metric) } as {
    -readonly [K in keyof Aggregate]: Aggregate[K];
  };
  for (const part of parts) {
    if (!sameMetric(part.metric, metric)) {
      throw new CaliberMismatchError(metric, part.metric);
    }
    acc.total += part.total;
    acc.pairCount += part.pairCount;
    acc.numeratorSum += part.numeratorSum;
    acc.denominatorSum += part.denominatorSum;
    acc.numeratorMissing += part.numeratorMissing;
    acc.denominatorMissing += part.denominatorMissing;
    acc.numeratorRetracted += part.numeratorRetracted;
    acc.denominatorRetracted += part.denominatorRetracted;
    acc.unpaired += part.unpaired;
  }
  return acc;
}

export type RatioValue =
  | { readonly kind: 'value'; readonly value: Rational }
  | { readonly kind: 'zero-denominator'; readonly numerator: bigint }
  | {
      readonly kind: 'missing';
      readonly missingNumerator: boolean;
      readonly missingDenominator: boolean;
    };

/**
 * Evaluate a ratio from an aggregate.
 *
 *  - any missing (unknown) field poisons the result: kind 'missing';
 *  - otherwise, a zero denominator sum is reported as 'zero-denominator',
 *    which is deliberately distinct from a TRUE zero ratio
 *    (numerator 0 over a positive denominator -> value 0/1).
 */
export function ratioOf(agg: Aggregate): RatioValue {
  const missingNumerator = agg.numeratorMissing > 0;
  const missingDenominator = agg.denominatorMissing > 0;
  if (missingNumerator || missingDenominator) {
    return { kind: 'missing', missingNumerator, missingDenominator };
  }
  if (agg.pairCount === 0 || agg.denominatorSum === 0n) {
    return { kind: 'zero-denominator', numerator: agg.numeratorSum };
  }
  return { kind: 'value', value: rational(agg.numeratorSum, agg.denominatorSum) };
}
