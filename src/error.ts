/**
 * Worst-case error evaluation, grouped by error source (correlation-aware).
 *
 * Semantics (documented rounding: none — all exact rational):
 * - shared source: one coherent scalar delta in [-1,1] multiplies the SIGNED
 *   sum of sensitivity*error over all referencing components. Worst case is
 *   |sum_i a_i * e_i|. This is the correlation-aware bound: contributions
 *   may cancel, and we do NOT replace it by sum_i |a_i|*e_i and call that
 *   tight — that naive figure is reported only as `independentSum` for
 *   comparison, never used for feasibility.
 * - independent source: each component's interval error is an independent
 *   adversarial variable, so the worst case is sum_i |a_i| * e_i. Negative
 *   sensitivities contribute by absolute value.
 * - total worst-case error = sum over sources of their worst cases
 *   (sources are mutually independent).
 */
import { ZERO, add, mul, abs, cmp } from './rational.ts';
import type { Rational } from './rational.ts';
import type { Instance, Tier } from './model.ts';

/** componentId -> tierId */
export type Selection = ReadonlyMap<string, string>;

export interface ErrorTerm {
  readonly componentId: string;
  readonly tierId: string;
  readonly sensitivity: Rational;
  readonly tierError: Rational;
  /** a_i * e_i (signed); for independent sources the worst case uses |this| */
  readonly signedContribution: Rational;
}

export interface SourceReport {
  readonly sourceId: string;
  readonly kind: 'shared' | 'independent';
  readonly terms: readonly ErrorTerm[];
  /** signed sum of contributions (for independent: sum of |terms|) */
  readonly signedSum: Rational;
  /** worst-case contribution of this source to the total */
  readonly worstCase: Rational;
  /** naive sum_i |a_i|*e_i — shown for contrast on shared sources only */
  readonly independentSum: Rational;
}

export interface ErrorReport {
  readonly perSource: readonly SourceReport[];
  readonly total: Rational;
  readonly budget: Rational;
  readonly feasible: boolean;
}

export function resolveSelection(
  inst: Instance,
  selection: Selection,
): Map<string, Tier> {
  const tiers = new Map<string, Tier>();
  for (const c of inst.components) {
    const tierId = selection.get(c.id);
    if (tierId === undefined) throw new Error(`selection missing component ${c.id}`);
    const tier = c.tiers.find((t) => t.id === tierId);
    if (!tier) throw new Error(`component ${c.id}: unknown tier ${tierId}`);
    tiers.set(c.id, tier);
  }
  return tiers;
}

export function evaluateError(inst: Instance, selection: Selection): ErrorReport {
  const tiers = resolveSelection(inst, selection);
  const perSource: SourceReport[] = inst.sources.map((src) => {
    const terms: ErrorTerm[] = [];
    for (const c of inst.components) {
      const sens = c.sensitivities.get(src.id);
      if (sens === undefined) continue;
      const tier = tiers.get(c.id)!;
      terms.push({
        componentId: c.id,
        tierId: tier.id,
        sensitivity: sens,
        tierError: tier.error,
        signedContribution: mul(sens, tier.error),
      });
    }
    let signedSum = ZERO;
    let independentSum = ZERO;
    for (const t of terms) {
      independentSum = add(independentSum, abs(t.signedContribution));
      signedSum =
        src.kind === 'shared'
          ? add(signedSum, t.signedContribution)
          : add(signedSum, abs(t.signedContribution));
    }
    const worstCase = src.kind === 'shared' ? abs(signedSum) : signedSum;
    return { sourceId: src.id, kind: src.kind, terms, signedSum, worstCase, independentSum };
  });
  let total = ZERO;
  for (const s of perSource) total = add(total, s.worstCase);
  return {
    perSource,
    total,
    budget: inst.errorBudget,
    feasible: cmp(total, inst.errorBudget) <= 0,
  };
}

/** Human-readable violation list; empty means the selection is valid. */
export function checkConstraints(inst: Instance, selection: Selection): string[] {
  const violations: string[] = [];
  for (const ex of inst.exclusions) {
    if (selection.get(ex.a.component) === ex.a.tier && selection.get(ex.b.component) === ex.b.tier) {
      violations.push(
        `exclusion violated: ${ex.a.component}=${ex.a.tier} excludes ${ex.b.component}=${ex.b.tier}`,
      );
    }
  }
  for (const dep of inst.dependencies) {
    if (
      selection.get(dep.ifTier.component) === dep.ifTier.tier &&
      selection.get(dep.requires.component) !== dep.requires.tier
    ) {
      violations.push(
        `dependency violated: ${dep.ifTier.component}=${dep.ifTier.tier} requires ` +
          `${dep.requires.component}=${dep.requires.tier}`,
      );
    }
  }
  return violations;
}

export function selectionCost(inst: Instance, selection: Selection): Rational {
  const tiers = resolveSelection(inst, selection);
  let total = ZERO;
  for (const c of inst.components) total = add(total, tiers.get(c.id)!.cost);
  return total;
}
