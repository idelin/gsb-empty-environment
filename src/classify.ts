import { Rational } from "./rational.ts";
import type { LinearConstraint } from "./constraint.ts";
import type { MeasurementStore } from "./measurement.ts";

export type PointStatus = "determinate-pass" | "determinate-fail" | "needs-review";

export interface Classification {
  readonly constraintId: string;
  readonly status: PointStatus;
  /** Worst-case (max) and best-case (min) of the LHS over the error box. */
  readonly maxValue: Rational | null; // null = +infinity (unknown error)
  readonly minValue: Rational | null; // null = -infinity
  readonly reason: string;
}

/**
 * Classify ONE constraint against measurement error intervals.
 *
 * Rules enforced here:
 * - An error interval crossing the threshold is NEVER passed by centre value.
 * - Exactly touching the threshold is decided by open/closed sense.
 * - Unknown error is never treated as 0: it makes the affected side
 *   unbounded, which forces "needs-review" unless the coefficient is 0.
 */
export function classifyConstraint(
  c: LinearConstraint,
  store: MeasurementStore,
): Classification {
  let centre = Rational.zero();
  let radius = Rational.zero();
  let unboundedAbove = false;
  let unboundedBelow = false;

  for (const t of c.terms) {
    const m = store.get(t.measurement);
    centre = centre.add(t.coeff.mul(m.value));
    if (t.coeff.isZero()) continue;
    if (m.error === null) {
      // Unknown error: deviation is unbounded in BOTH directions.
      unboundedAbove = true;
      unboundedBelow = true;
    } else {
      radius = radius.add(t.coeff.abs().mul(m.error));
    }
  }

  const maxValue = unboundedAbove ? null : centre.add(radius);
  const minValue = unboundedBelow ? null : centre.sub(radius);

  // Normalise: does "lhs <sense> rhs" hold certainly / certainly not?
  // For ">="/">" flip the comparison against min/max accordingly.
  const holdsAt = (v: Rational): boolean => {
    switch (c.sense) {
      case "<=":
        return v.lte(c.rhs);
      case "<":
        return v.lt(c.rhs);
      case ">=":
        return v.gte(c.rhs);
      case ">":
        return v.gt(c.rhs);
    }
  };

  const upperSide = c.sense === "<=" || c.sense === "<"; // bounded above needed
  const worst = upperSide ? maxValue : minValue;
  const best = upperSide ? minValue : maxValue;

  // worst === null: unbounded in the HARMFUL direction -> cannot guarantee.
  // best === null: unbounded in the FAVOURABLE direction -> best case holds.
  const worstBad = worst === null || !holdsAt(worst);
  const bestBad = best !== null && !holdsAt(best);

  let status: PointStatus;
  let reason: string;
  if (!worstBad) {
    status = "determinate-pass";
    reason = `worst case ${worst} satisfies ${c.sense} ${c.rhs}`;
  } else if (bestBad) {
    status = "determinate-fail";
    reason = `best case still violates ${c.sense} ${c.rhs}`;
  } else {
    status = "needs-review";
    const why =
      worst === null || best === null
        ? "unknown error makes the interval unbounded"
        : `interval [${minValue}, ${maxValue}] straddles threshold ${c.rhs}`;
    reason = `${why}; centre value must not be used to pass`;
  }

  return { constraintId: c.id, status, maxValue, minValue, reason };
}
