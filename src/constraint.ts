import type { Rational } from "./rational.ts";

/** Threshold sense. `<=`/`>=` are closed bounds, `<`/`>` are open bounds. */
export type Sense = "<=" | "<" | ">=" | ">";

export interface LinearTerm {
  readonly measurement: string;
  readonly coeff: Rational;
}

/**
 * A rational linear constraint over measurements:
 *   sum(coeff_i * m_i) <sense> rhs
 */
export interface LinearConstraint {
  readonly id: string;
  readonly terms: readonly LinearTerm[];
  readonly sense: Sense;
  readonly rhs: Rational;
  readonly label?: string;
}

export function constraint(
  id: string,
  terms: readonly LinearTerm[],
  sense: Sense,
  rhs: Rational,
  label?: string,
): LinearConstraint {
  return { id, terms, sense, rhs, label };
}

/** Versioned constraint set; version bumps invalidate dependent evidence. */
export class ConstraintSet {
  private constraints = new Map<string, LinearConstraint>();
  private versionCounter = 0;

  add(c: LinearConstraint): void {
    this.constraints.set(c.id, c);
    this.versionCounter += 1;
  }

  remove(id: string): void {
    if (this.constraints.delete(id)) this.versionCounter += 1;
  }

  get(id: string): LinearConstraint {
    const c = this.constraints.get(id);
    if (!c) throw new Error(`unknown constraint: ${id}`);
    return c;
  }

  all(): LinearConstraint[] {
    return [...this.constraints.values()];
  }

  get version(): number {
    return this.versionCounter;
  }
}
