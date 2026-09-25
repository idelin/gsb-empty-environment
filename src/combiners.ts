import type { Decision } from './decision.ts';
import { mergeAll, type Obligation, type ObligationConflict } from './obligations.ts';

export type CombinerName = 'deny-overrides' | 'permit-overrides' | 'first-applicable';

export interface BranchOutcome {
  readonly source: string;
  readonly decision: Decision;
  readonly obligations: readonly Obligation[];
  readonly missingEvidence: readonly string[];
  readonly missingValues: readonly string[];
  readonly typeErrors: readonly string[];
  readonly accessed: readonly string[];
}

export interface TraceEntry {
  readonly source: string;
  readonly decision: Decision;
}

export interface CombinedOutcome {
  readonly decision: Decision;
  readonly obligations: readonly Obligation[];
  /** Present when permit-side obligations conflict; the conflict is surfaced
   *  as Indeterminate instead of being masked by a Permit conclusion. */
  readonly conflict?: ObligationConflict;
  readonly missingEvidence: readonly string[];
  readonly missingValues: readonly string[];
  readonly typeErrors: readonly string[];
  readonly accessed: readonly string[];
  readonly trace: readonly TraceEntry[];
}

function evidence(inputs: readonly BranchOutcome[]) {
  const pick = (field: 'missingEvidence' | 'missingValues' | 'typeErrors' | 'accessed') =>
    [...new Set(inputs.flatMap((input) => input[field]))].sort();
  return {
    missingEvidence: pick('missingEvidence'),
    missingValues: pick('missingValues'),
    typeErrors: pick('typeErrors'),
    accessed: pick('accessed'),
    trace: inputs.map((input) => ({ source: input.source, decision: input.decision })),
  };
}

function mergedPermit(
  inputs: readonly BranchOutcome[],
  all: readonly BranchOutcome[],
): CombinedOutcome {
  const merged = mergeAll(inputs.map((input) => input.obligations));
  if (!merged.ok) {
    // A conflicting obligation set must not be hidden behind a Permit.
    return { decision: 'Indeterminate', obligations: [], conflict: merged.conflict, ...evidence(all) };
  }
  return { decision: 'Permit', obligations: merged.obligations, ...evidence(all) };
}

function mergedDeny(
  inputs: readonly BranchOutcome[],
  all: readonly BranchOutcome[],
): CombinedOutcome {
  const merged = mergeAll(inputs.map((input) => input.obligations));
  if (!merged.ok) {
    return { decision: 'Indeterminate', obligations: [], conflict: merged.conflict, ...evidence(all) };
  }
  return { decision: 'Deny', obligations: merged.obligations, ...evidence(all) };
}

function indeterminate(all: readonly BranchOutcome[]): CombinedOutcome {
  return { decision: 'Indeterminate', obligations: [], ...evidence(all) };
}

function notApplicable(all: readonly BranchOutcome[]): CombinedOutcome {
  return { decision: 'NotApplicable', obligations: [], ...evidence(all) };
}

/** Any Deny wins; otherwise Indeterminate; otherwise merged Permit; else NotApplicable. */
export function denyOverrides(inputs: readonly BranchOutcome[]): CombinedOutcome {
  const denies = inputs.filter((input) => input.decision === 'Deny');
  if (denies.length > 0) return mergedDeny(denies, inputs);
  if (inputs.some((input) => input.decision === 'Indeterminate')) return indeterminate(inputs);
  const permits = inputs.filter((input) => input.decision === 'Permit');
  if (permits.length > 0) return mergedPermit(permits, inputs);
  return notApplicable(inputs);
}

/** Any Permit wins (obligations merged, conflicts surfaced); otherwise Deny; then Indeterminate; else NotApplicable. */
export function permitOverrides(inputs: readonly BranchOutcome[]): CombinedOutcome {
  const permits = inputs.filter((input) => input.decision === 'Permit');
  if (permits.length > 0) return mergedPermit(permits, inputs);
  const denies = inputs.filter((input) => input.decision === 'Deny');
  if (denies.length > 0) return mergedDeny(denies, inputs);
  if (inputs.some((input) => input.decision === 'Indeterminate')) return indeterminate(inputs);
  return notApplicable(inputs);
}

/** The first branch that is not NotApplicable decides; order is semantically significant. */
export function firstApplicable(inputs: readonly BranchOutcome[]): CombinedOutcome {
  const chosen = inputs.find((input) => input.decision !== 'NotApplicable');
  if (!chosen) return notApplicable(inputs);
  return {
    decision: chosen.decision,
    obligations: chosen.obligations,
    ...evidence(inputs),
  };
}

export function combine(name: CombinerName, inputs: readonly BranchOutcome[]): CombinedOutcome {
  switch (name) {
    case 'deny-overrides':
      return denyOverrides(inputs);
    case 'permit-overrides':
      return permitOverrides(inputs);
    case 'first-applicable':
      return firstApplicable(inputs);
  }
}
