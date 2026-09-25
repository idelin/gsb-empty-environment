import { mergeObligations } from './obligations.ts';
import type { EvaluationResult } from './types.ts';

export type CombiningAlgorithm = 'deny-overrides' | 'permit-overrides' | 'first-applicable';

export function denyOverrides(results: readonly EvaluationResult[]): EvaluationResult {
  if (results.some((r) => r.decision === 'Deny')) {
    return { decision: 'Deny', obligations: [], conflicts: [] };
  }
  const indet = results.filter((r) => r.decision === 'Indeterminate');
  if (indet.length > 0) {
    return { decision: 'Indeterminate', obligations: [], conflicts: indet.flatMap((r) => r.conflicts) };
  }
  const permits = results.filter((r) => r.decision === 'Permit');
  if (permits.length > 0) {
    const { obligations, conflicts } = mergeObligations(permits.map((p) => p.obligations));
    if (conflicts.length > 0) return { decision: 'Indeterminate', obligations: [], conflicts };
    return { decision: 'Permit', obligations, conflicts: [] };
  }
  return { decision: 'NotApplicable', obligations: [], conflicts: [] };
}

export function permitOverrides(results: readonly EvaluationResult[]): EvaluationResult {
  const permits = results.filter((r) => r.decision === 'Permit');
  if (permits.length > 0) {
    const { obligations, conflicts } = mergeObligations(permits.map((p) => p.obligations));
    if (conflicts.length > 0) return { decision: 'Indeterminate', obligations: [], conflicts };
    return { decision: 'Permit', obligations, conflicts: [] };
  }
  if (results.some((r) => r.decision === 'Deny')) {
    return { decision: 'Deny', obligations: [], conflicts: [] };
  }
  const indet = results.filter((r) => r.decision === 'Indeterminate');
  if (indet.length > 0) {
    return { decision: 'Indeterminate', obligations: [], conflicts: indet.flatMap((r) => r.conflicts) };
  }
  return { decision: 'NotApplicable', obligations: [], conflicts: [] };
}

export function firstApplicable(results: readonly EvaluationResult[]): EvaluationResult {
  for (const r of results) {
    if (r.decision !== 'NotApplicable') return r;
  }
  return { decision: 'NotApplicable', obligations: [], conflicts: [] };
}

export function combine(algorithm: CombiningAlgorithm, results: readonly EvaluationResult[]): EvaluationResult {
  switch (algorithm) {
    case 'deny-overrides': return denyOverrides(results);
    case 'permit-overrides': return permitOverrides(results);
    case 'first-applicable': return firstApplicable(results);
  }
}
