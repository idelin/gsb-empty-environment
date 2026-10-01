import { Rational } from "./rational.ts";
import { satisfies } from "./system.ts";
import type {
  CompiledSystem,
  CompiledConstraint,
  DeviationVariable,
} from "./system.ts";
import { DEFAULT_BUDGET, feasible, verifyFarkas, verifyWitness } from "./fm.ts";
import type { Budget, FarkasCertificate } from "./fm.ts";

/**
 * Joint analysis over SHARED deviation variables.
 *
 * The verdict is computed on the joint feasible region; per-constraint
 * "possibly passes" are never OR-ed into an overall pass.
 *
 * - determinate-pass: EVERY error assignment inside the error box satisfies
 *   ALL constraints. Certificate: per-constraint worst-case vertex + margin.
 * - determinate-fail: NO assignment satisfies all constraints.
 *   Certificate: Farkas multipliers deriving a contradiction.
 * - mixed: some assignment satisfies all, some assignment violates at least
 *   one. Witnesses: one of each.
 * - undecided: the elimination budget was exhausted before a verdict.
 */

export type JointStatus =
  | "determinate-pass"
  | "determinate-fail"
  | "mixed"
  | "undecided";

export interface PassCertificateEntry {
  readonly constraintId: string;
  /** Worst-case vertex of the error box for this constraint. */
  readonly vertex: readonly (Rational | null)[];
  /** LHS value at that vertex (null = unbounded, cannot certify). */
  readonly worstValue: Rational | null;
  readonly rhs: Rational;
  readonly strict: boolean;
}

export interface PassCertificate {
  readonly entries: readonly PassCertificateEntry[];
}

export interface JointWitnesses {
  /** Assignment (deviation space) satisfying ALL constraints. */
  readonly satisfying: readonly Rational[];
  /** Assignment (deviation space) violating AT LEAST ONE constraint. */
  readonly violating: readonly Rational[];
  readonly violatedConstraintIds: readonly string[];
}

export interface JointJudgment {
  readonly status: JointStatus;
  readonly passCertificate?: PassCertificate;
  readonly failCertificate?: FarkasCertificate;
  readonly witnesses?: JointWitnesses;
  readonly undecidedReason?: string;
}

interface UniversalResult {
  readonly allHold: boolean;
  readonly entries: PassCertificateEntry[];
  /** First violating vertex found (deviation space), if any. */
  readonly violating: readonly Rational[] | null;
  readonly violatedConstraintIds: readonly string[];
}

function universalCheck(system: CompiledSystem): UniversalResult {
  const vars = system.variables;
  const entries: PassCertificateEntry[] = [];
  let violating: Rational[] | null = null;
  const violatedIds: string[] = [];

  for (const cc of system.constraints) {
    const vertex: (Rational | null)[] = [];
    // Accumulate in MEASUREMENT space: constPart + sum(coeff * deviation).
    let worst: Rational | null = cc.constPart;
    let unbounded = false;
    cc.ineq.coeffs.forEach((coeff, i) => {
      if (coeff.isZero()) {
        vertex.push(Rational.zero());
        return;
      }
      const bound = coeff.sign() > 0 ? vars[i].hi : vars[i].lo;
      if (bound === null) {
        unbounded = true;
        vertex.push(null);
        return;
      }
      vertex.push(bound);
      worst = (worst as Rational).add(coeff.mul(bound));
    });
    if (unbounded) worst = null;

    const holds =
      worst !== null &&
      (cc.ineq.strict ? worst.lt(cc.original.rhs) : worst.lte(cc.original.rhs));
    entries.push({
      constraintId: cc.original.id,
      vertex,
      worstValue: worst,
      rhs: cc.original.rhs,
      strict: cc.ineq.strict,
    });
    if (!holds) {
      violatedIds.push(cc.original.id);
      if (violating === null) violating = buildViolating(system, cc);
    }
  }

  return {
    allHold: violatedIds.length === 0,
    entries,
    violating,
    violatedConstraintIds: violatedIds,
  };
}

/**
 * Build a concrete assignment that violates constraint `cc`.
 * Works also for unbounded (unknown-error) variables: push the deviation
 * far enough in the harmful direction, exactly.
 */
function buildViolating(
  system: CompiledSystem,
  cc: CompiledConstraint,
): readonly Rational[] {
  const vars = system.variables;
  const x: Rational[] = vars.map(() => Rational.zero());
  // First pass: bounded harmful directions.
  cc.ineq.coeffs.forEach((coeff, i) => {
    if (coeff.isZero()) return;
    const bound = coeff.sign() > 0 ? vars[i].hi : vars[i].lo;
    if (bound !== null) x[i] = bound;
  });
  let lhs = Rational.zero();
  cc.ineq.coeffs.forEach((coeff, i) => {
    lhs = lhs.add(coeff.mul(x[i]));
  });
  const violates = cc.ineq.strict ? lhs.gte(cc.ineq.rhs) : lhs.gt(cc.ineq.rhs);
  if (violates) return x;
  // Some unbounded variable must finish the job.
  for (let i = 0; i < cc.ineq.coeffs.length; i++) {
    const coeff = cc.ineq.coeffs[i];
    if (coeff.isZero()) continue;
    const bound = coeff.sign() > 0 ? vars[i].hi : vars[i].lo;
    if (bound !== null) continue;
    // Need coeff*x_i to push lhs strictly past rhs. Choose exactly:
    // x_i = (rhs - lhs + 1) / coeff  when coeff > 0 (and mirrored for < 0).
    const gap = cc.ineq.rhs.sub(lhs).add(Rational.one());
    x[i] = gap.div(coeff.abs()).mul(Rational.fromBigInt(BigInt(coeff.sign())));
    return x;
  }
  return x; // unreachable when universal check failed
}

export interface AnalyzeOptions {
  readonly budget?: Budget;
}

export function analyzeJoint(
  system: CompiledSystem,
  options: AnalyzeOptions = {},
): JointJudgment {
  const budget = options.budget ?? DEFAULT_BUDGET;
  const universal = universalCheck(system);

  if (universal.allHold) {
    return {
      status: "determinate-pass",
      passCertificate: { entries: universal.entries },
    };
  }

  const result = feasible(system.inequalities, system.variables.length, budget);

  if (result.kind === "undecided") {
    return { status: "undecided", undecidedReason: result.reason };
  }

  if (result.kind === "infeasible") {
    if (!verifyFarkas(system.sources, result.certificate, system.variables.length)) {
      throw new Error("internal error: invalid Farkas certificate");
    }
    return { status: "determinate-fail", failCertificate: result.certificate };
  }

  // Feasible but not universal: mixed region, provide both witnesses.
  if (!verifyWitness(system.inequalities, result.witness)) {
    throw new Error("internal error: FM witness does not satisfy the system");
  }
  const witnesses: JointWitnesses = {
    satisfying: result.witness,
    violating: universal.violating ?? [],
    violatedConstraintIds: universal.violatedConstraintIds,
  };
  return { status: "mixed", witnesses };
}

/** Verify a pass certificate: every entry's worst value must satisfy its bound. */
export function verifyPassCertificate(cert: PassCertificate): boolean {
  return cert.entries.every((e) => {
    if (e.worstValue === null) return false;
    return e.strict ? e.worstValue.lt(e.rhs) : e.worstValue.lte(e.rhs);
  });
}

/** Re-check witnesses against the compiled system (exact). */
export function verifyJointWitnesses(
  system: CompiledSystem,
  witnesses: JointWitnesses,
): boolean {
  const allOk = system.constraints.every((cc) => satisfies(cc.ineq, witnesses.satisfying));
  const boxOk = system.variables.every((v: DeviationVariable, i: number) => {
    const x = witnesses.satisfying[i];
    return (v.lo === null || x.gte(v.lo)) && (v.hi === null || x.lte(v.hi));
  });
  const someViolated = system.constraints.some(
    (cc) => !satisfies(cc.ineq, witnesses.violating),
  );
  return allOk && boxOk && someViolated;
}
