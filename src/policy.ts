/**
 * Policy engine. Rules read only the explicit attribute set; effects are
 * allow or deny. Allow decisions carry obligations that are merged with the
 * per-kind algebras. Deny wins and never executes allow obligations.
 *
 * A matched rule may suppress obligation kinds contributed by other rules;
 * suppressed obligations are recorded explicitly (never dropped silently)
 * and are restored when the suppressing rule is withdrawn.
 */

import type { Attrs, Expr } from './expr.ts';
import { evalExpr } from './expr.ts';
import type { Obligation, ObligationKind } from './obligations.ts';
import { mergeObligations, type Conflict, type MergedObligations } from './merge.ts';

export type Rule = {
  id: string;
  when: Expr;
  effect: 'allow' | 'deny';
  obligations?: readonly Obligation[];
  /** obligation kinds from OTHER matched rules that this rule suppresses */
  suppresses?: readonly ObligationKind[];
  reason?: string;
};

export type SuppressedRecord = {
  obligation: Obligation;
  fromRule: string;
  byRule: string;
};

export type Decision =
  | {
      effect: 'allow';
      matched: string[];
      merged: MergedObligations;
      suppressed: SuppressedRecord[];
    }
  | {
      effect: 'deny';
      matched: string[];
      denyRules: string[];
      /** allow obligations that were NOT executed because of the deny */
      notExecuted: { obligation: Obligation; fromRule: string }[];
    }
  | {
      effect: 'failure';
      matched: string[];
      conflict: Conflict;
      suppressed: SuppressedRecord[];
    };

export type EvaluateOptions = {
  /** rule ids treated as withdrawn (as if removed from the policy) */
  withdrawn?: readonly string[];
};

export function evaluate(
  rules: readonly Rule[],
  attrs: Attrs,
  opts: EvaluateOptions = {},
): Decision {
  // Attributes are read-only: freeze defensively and never mutate.
  const frozenAttrs = Object.freeze({ ...attrs });
  const withdrawn = new Set(opts.withdrawn ?? []);

  const matched = rules.filter((r) => !withdrawn.has(r.id) && evalExpr(r.when, frozenAttrs));
  const matchedIds = matched.map((r) => r.id);

  const denyRules = matched.filter((r) => r.effect === 'deny');
  const allowRules = matched.filter((r) => r.effect === 'allow');
  const allowObligations = allowRules.flatMap((r) =>
    (r.obligations ?? []).map((obligation) => ({ obligation, fromRule: r.id })),
  );

  if (denyRules.length > 0) {
    // Deny: allow obligations are explicitly not executed.
    return {
      effect: 'deny',
      matched: matchedIds,
      denyRules: denyRules.map((r) => r.id),
      notExecuted: allowObligations,
    };
  }

  const suppressed: SuppressedRecord[] = [];
  const active: Obligation[] = [];
  for (const { obligation, fromRule } of allowObligations) {
    const suppressor = matched.find(
      (r) => r.id !== fromRule && (r.suppresses ?? []).includes(obligation.kind),
    );
    if (suppressor) {
      suppressed.push({ obligation, fromRule, byRule: suppressor.id });
    } else {
      active.push(obligation);
    }
  }

  const outcome = mergeObligations(active);
  if (!outcome.ok) {
    // Incompatible obligations are an explicit failure, never silently dropped.
    return { effect: 'failure', matched: matchedIds, conflict: outcome.conflict, suppressed };
  }
  return { effect: 'allow', matched: matchedIds, merged: outcome.merged, suppressed };
}
