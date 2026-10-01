/**
 * Reference verification.
 *
 * The ground truth is always the direct aggregation of raw records.
 * Any sliced / hierarchical merge or any correction workflow must
 * reproduce that reference exactly (BigInt equality, field by field).
 */

import {
  aggregate,
  mergeAggregates,
  sameMetric,
  type Aggregate,
  type Observation,
} from './measure.ts';

export function directAggregate(records: readonly Observation[]): Aggregate {
  return aggregate(records);
}

/** One-level sliced merge: aggregate each slice, then merge partials. */
export function mergeSlices(slices: readonly (readonly Observation[])[]): Aggregate {
  return mergeAggregates(slices.map((s) => aggregate(s)));
}

/** Multi-level merge: slices -> groups -> total. */
export function mergeHierarchical(
  groups: readonly (readonly (readonly Observation[])[])[],
): Aggregate {
  return mergeAggregates(groups.map((g) => mergeSlices(g)));
}

export function aggregateDiffs(a: Aggregate, b: Aggregate): string[] {
  const diffs: string[] = [];
  if (!sameMetric(a.metric, b.metric)) {
    diffs.push(`metric: ${a.metric.id}@${a.metric.version} != ${b.metric.id}@${b.metric.version}`);
  }
  const fields = [
    'total',
    'pairCount',
    'numeratorSum',
    'denominatorSum',
    'numeratorMissing',
    'denominatorMissing',
    'numeratorRetracted',
    'denominatorRetracted',
    'unpaired',
  ] as const;
  for (const field of fields) {
    if (a[field] !== b[field]) {
      diffs.push(`${field}: ${a[field]} != ${b[field]}`);
    }
  }
  return diffs;
}

export interface VerifyResult {
  readonly ok: boolean;
  readonly diffs: readonly string[];
  readonly computed: Aggregate;
  readonly reference: Aggregate;
}

function verify(computed: Aggregate, reference: Aggregate): VerifyResult {
  const diffs = aggregateDiffs(computed, reference);
  return { ok: diffs.length === 0, diffs, computed, reference };
}

/** Arbitrary slice partitioning must merge to the direct aggregation. */
export function verifySliceMerging(
  slices: readonly (readonly Observation[])[],
): VerifyResult {
  const flat = slices.flat();
  return verify(mergeSlices(slices), directAggregate(flat));
}

/** Hierarchical (multi-level) merging must match the direct reference. */
export function verifyHierarchicalMerging(
  groups: readonly (readonly (readonly Observation[])[])[],
): VerifyResult {
  const flat = groups.flatMap((g) => g.flat());
  return verify(mergeHierarchical(groups), directAggregate(flat));
}

export type Correction =
  | { readonly type: 'retract-denominator'; readonly observationId: string }
  | { readonly type: 'retract-numerator'; readonly observationId: string }
  | { readonly type: 'replace'; readonly observation: Observation }
  | { readonly type: 'remove'; readonly observationId: string };

/** Pure correction application: returns a new record list. */
export function applyCorrections(
  records: readonly Observation[],
  corrections: readonly Correction[],
): Observation[] {
  let result = [...records];
  for (const correction of corrections) {
    switch (correction.type) {
      case 'retract-denominator':
        result = result.map((o) =>
          o.id === correction.observationId
            ? { ...o, denominator: { status: 'retracted' } }
            : o,
        );
        break;
      case 'retract-numerator':
        result = result.map((o) =>
          o.id === correction.observationId
            ? { ...o, numerator: { status: 'retracted' } }
            : o,
        );
        break;
      case 'replace':
        result = result.map((o) => (o.id === correction.observation.id ? correction.observation : o));
        break;
      case 'remove':
        result = result.filter((o) => o.id !== correction.observationId);
        break;
    }
  }
  return result;
}

/**
 * Corrections applied per-slice and merged hierarchically must equal
 * corrections applied to the flat record set and aggregated directly.
 */
export function verifyCorrections(
  slices: readonly (readonly Observation[])[],
  corrections: readonly Correction[],
): VerifyResult {
  const correctedSlices = slices.map((s) => applyCorrections(s, corrections));
  const nonEmpty = correctedSlices.filter((s) => s.length > 0);
  const computed = mergeSlices(nonEmpty);
  const reference = directAggregate(applyCorrections(slices.flat(), corrections));
  return verify(computed, reference);
}
