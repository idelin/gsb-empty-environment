import { Rational } from "./rational.ts";
import type { LinearConstraint } from "./constraint.ts";
import type { MeasurementStore } from "./measurement.ts";

/**
 * A linear inequality in deviation space:  coeffs . d  <= rhs  (or `< rhs`
 * when strict). `combo` records provenance: which source inequalities, with
 * which non-negative multipliers, produced this one. It is the raw material
 * for Farkas infeasibility certificates.
 */
export interface Ineq {
  readonly coeffs: readonly Rational[];
  readonly rhs: Rational;
  readonly strict: boolean;
  readonly combo: ReadonlyMap<string, Rational>;
}

export function makeIneq(
  coeffs: readonly Rational[],
  rhs: Rational,
  strict: boolean,
  sourceId: string,
): Ineq {
  return { coeffs, rhs, strict, combo: new Map([[sourceId, Rational.one()]]) };
}

/** One deviation variable per measurement: m_i = value_i + d_i. */
export interface DeviationVariable {
  readonly measurementId: string;
  readonly lo: Rational | null; // -error, or null when error unknown
  readonly hi: Rational | null; // +error, or null when error unknown
}

export interface CompiledConstraint {
  readonly original: LinearConstraint;
  /** Inequality over deviation variables (constant part folded into rhs). */
  readonly ineq: Ineq;
  /** Constant part: sum(coeff_i * value_i). */
  readonly constPart: Rational;
  readonly sourceId: string;
}

export interface CompiledSystem {
  readonly variables: readonly DeviationVariable[];
  readonly constraints: readonly CompiledConstraint[];
  /** User constraints + box bounds, all as sourced inequalities. */
  readonly inequalities: readonly Ineq[];
  /** All source inequalities by id (for certificate verification). */
  readonly sources: ReadonlyMap<string, Ineq>;
}

export const CONSTRAINT_SOURCE_PREFIX = "constraint:";
export const BOX_LO_PREFIX = "box-lo:";
export const BOX_HI_PREFIX = "box-hi:";

/**
 * Compile measurements + constraints into deviation space.
 * Every constraint shares the SAME deviation variables, so joint analysis
 * reasons about the joint feasible region instead of per-constraint maybes.
 */
export function compileSystem(
  store: MeasurementStore,
  constraints: readonly LinearConstraint[],
): CompiledSystem {
  const ids = store.ids();
  const indexOf = new Map<string, number>(ids.map((id, i) => [id, i]));
  const variables: DeviationVariable[] = ids.map((id) => {
    const m = store.get(id);
    if (m.error === null) return { measurementId: id, lo: null, hi: null };
    return { measurementId: id, lo: m.error.neg(), hi: m.error };
  });

  const n = ids.length;
  const zeroRow = (): Rational[] => Array.from({ length: n }, () => Rational.zero());

  const compiled: CompiledConstraint[] = [];
  const inequalities: Ineq[] = [];
  const sources = new Map<string, Ineq>();

  for (const c of constraints) {
    // Normalise to "<="/"<": flip signs for ">="/">".
    const flip = c.sense === ">=" || c.sense === ">";
    const strict = c.sense === "<" || c.sense === ">";
    const coeffs = zeroRow();
    let constPart = Rational.zero();
    for (const t of c.terms) {
      const idx = indexOf.get(t.measurement);
      if (idx === undefined) {
        throw new Error(`constraint ${c.id}: unknown measurement ${t.measurement}`);
      }
      const coeff = flip ? t.coeff.neg() : t.coeff;
      coeffs[idx] = coeffs[idx].add(coeff);
      constPart = constPart.add(coeff.mul(store.get(t.measurement).value));
    }
    const rhs = (flip ? c.rhs.neg() : c.rhs).sub(constPart);
    const sourceId = CONSTRAINT_SOURCE_PREFIX + c.id;
    const ineq = makeIneq(coeffs, rhs, strict, sourceId);
    compiled.push({ original: c, ineq, constPart, sourceId });
    inequalities.push(ineq);
    sources.set(sourceId, ineq);
  }

  // Box bounds for bounded deviation variables.
  variables.forEach((v, i) => {
    if (v.hi !== null) {
      const coeffs = zeroRow();
      coeffs[i] = Rational.one();
      const id = BOX_HI_PREFIX + v.measurementId;
      const ineq = makeIneq(coeffs, v.hi, false, id);
      inequalities.push(ineq);
      sources.set(id, ineq);
    }
    if (v.lo !== null) {
      const coeffs = zeroRow();
      coeffs[i] = Rational.one().neg();
      const id = BOX_LO_PREFIX + v.measurementId;
      const ineq = makeIneq(coeffs, v.lo.neg(), false, id);
      inequalities.push(ineq);
      sources.set(id, ineq);
    }
  });

  return { variables, constraints: compiled, inequalities, sources };
}

/** Evaluate coeffs . x exactly. */
export function dot(coeffs: readonly Rational[], x: readonly Rational[]): Rational {
  let acc = Rational.zero();
  for (let i = 0; i < coeffs.length; i++) acc = acc.add(coeffs[i].mul(x[i]));
  return acc;
}

/** Check one assignment against one inequality, exactly. */
export function satisfies(ineq: Ineq, x: readonly Rational[]): boolean {
  const lhs = dot(ineq.coeffs, x);
  return ineq.strict ? lhs.lt(ineq.rhs) : lhs.lte(ineq.rhs);
}
