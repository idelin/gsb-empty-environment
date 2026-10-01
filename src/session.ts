/**
 * Resumable precision-refinement comparison protocol.
 *
 * Each operand is an EvidenceProvider: given a refinement level it
 * returns an AffineValue whose error radius never grows with the level.
 * A CompareSession starts at level 0 on both sides. While the evidence
 * overlaps, it spends budget to request finer evidence (higher level)
 * from the side with the larger residual uncertainty. A definite
 * answer is produced only when the intervals separate (lt/gt) or when
 * both sides are exact and equal (eq). Otherwise the session reports
 * `undetermined`, and its state can be suspended to JSON and resumed
 * later — possibly with a fresh budget — without redoing work.
 */

import { type Rational, cmp, toString as rationalToString } from './rational.ts';
import {
  type AffineValue,
  type CompareResult,
  decided,
  undetermined,
  compareAffine,
  totalRadius,
  isExact,
  describe,
} from './value.ts';

export interface EvidenceProvider {
  readonly id: string;
  /** Evidence at a refinement level; radius must be non-increasing in level. */
  evidenceAt(level: number): AffineValue;
  /** Highest useful level; undefined means unbounded refinement. */
  readonly maxLevel?: number;
}

export interface SuspendedState {
  readonly leftLevel: number;
  readonly rightLevel: number;
  readonly budgetUsed: number;
  readonly budget: number | null; // null encodes Infinity
}

export class CompareSession {
  leftLevel = 0;
  rightLevel = 0;
  budgetUsed = 0;
  readonly left: EvidenceProvider;
  readonly right: EvidenceProvider;
  readonly budget: number;

  constructor(
    left: EvidenceProvider,
    right: EvidenceProvider,
    budget: number = Number.POSITIVE_INFINITY,
  ) {
    if (budget < 0) throw new RangeError('budget must be >= 0');
    this.left = left;
    this.right = right;
    this.budget = budget;
  }

  leftEvidence(): AffineValue {
    return this.left.evidenceAt(this.leftLevel);
  }

  rightEvidence(): AffineValue {
    return this.right.evidenceAt(this.rightLevel);
  }

  /** Compare the evidence currently on hand, without refining. */
  evaluate(): CompareResult {
    return compareAffine(this.leftEvidence(), this.rightEvidence());
  }

  private canRefineLeft(): boolean {
    return this.left.maxLevel === undefined || this.leftLevel < this.left.maxLevel;
  }

  private canRefineRight(): boolean {
    return this.right.maxLevel === undefined || this.rightLevel < this.right.maxLevel;
  }

  /**
   * One protocol round: evaluate; if undetermined and budget remains,
   * refine the side with the larger residual radius and evaluate again.
   */
  step(): CompareResult {
    const first = this.evaluate();
    if (first.kind === 'decided') return first;
    if (this.budgetUsed >= this.budget) {
      return undetermined(`budget exhausted after ${this.budgetUsed} refinement(s)`);
    }
    if (!this.canRefineLeft() && !this.canRefineRight()) {
      const a = this.leftEvidence();
      const b = this.rightEvidence();
      if (isExact(a) && isExact(b)) {
        // exact evidence cannot disagree about order without deciding
        const c = cmp(a.centre, b.centre);
        return decided(c < 0 ? 'lt' : c > 0 ? 'gt' : 'eq');
      }
      return undetermined('no finer evidence available; intervals remain overlapping');
    }

    const rl = totalRadius(this.leftEvidence());
    const rr = totalRadius(this.rightEvidence());
    const preferLeft = this.canRefineLeft() && (!this.canRefineRight() || cmp(rl, rr) >= 0);
    if (preferLeft) this.leftLevel++;
    else this.rightLevel++;
    this.budgetUsed++;
    return this.evaluate();
  }

  /** Run rounds until decided, budget-exhausted, or evidence runs out. */
  run(): CompareResult {
    for (;;) {
      const r = this.step();
      if (r.kind === 'decided') return r;
      if (r.reason.startsWith('budget exhausted')) return r;
      if (r.reason.startsWith('no finer evidence')) return r;
    }
  }

  /** Serialise resumable state (levels + spent budget) to JSON. */
  suspend(): string {
    const state: SuspendedState = {
      leftLevel: this.leftLevel,
      rightLevel: this.rightLevel,
      budgetUsed: this.budgetUsed,
      budget: this.budget === Number.POSITIVE_INFINITY ? null : this.budget,
    };
    return JSON.stringify(state);
  }

  /**
   * Rebuild a session from suspended state. The same providers must be
   * supplied (they are behaviour, not data); refinement levels and the
   * spent budget carry over, optionally topped up with `extraBudget`.
   */
  static resume(
    json: string,
    left: EvidenceProvider,
    right: EvidenceProvider,
    extraBudget = 0,
  ): CompareSession {
    const state = JSON.parse(json) as SuspendedState;
    const base = state.budget === null ? Number.POSITIVE_INFINITY : state.budget;
    const session = new CompareSession(left, right, base + extraBudget);
    session.leftLevel = state.leftLevel;
    session.rightLevel = state.rightLevel;
    session.budgetUsed = state.budgetUsed;
    return session;
  }
}

/* ---------- built-in providers ---------- */

/** An exact number: zero-radius evidence at every level. */
export function exactProvider(id: string, value: Rational): EvidenceProvider {
  return {
    id,
    maxLevel: 0,
    evidenceAt: () => ({ centre: value, errors: [] }),
  };
}

/** A fixed explicit error interval; cannot be refined further. */
export function fixedIntervalProvider(
  id: string,
  centre: Rational,
  token: string,
  radius: Rational,
): EvidenceProvider {
  return {
    id,
    maxLevel: 0,
    evidenceAt: () => ({ centre, errors: [{ token, radius }] }),
  };
}

/**
 * A measurement whose error bound halves at each refinement level
 * (e.g. repeated readings of an instrument). The provenance token is
 * stable across levels, so it still cancels against the same source.
 */
export function halvingProvider(
  id: string,
  centre: Rational,
  token: string,
  initialRadius: Rational,
  maxLevel?: number,
): EvidenceProvider {
  return {
    id,
    maxLevel,
    evidenceAt: (level) => ({
      centre,
      errors: [{ token, radius: { num: initialRadius.num, den: initialRadius.den * 2n ** BigInt(level) } }],
    }),
  };
}

/**
 * A decimal digit stream: level k reveals the first `digitsPerLevel` * (k+1)
 * fractional digits; the radius is the place value of the first unrevealed
 * digit. Models "ask the source for more digits" refinement.
 */
export function digitStreamProvider(
  id: string,
  decimalText: string,
  token: string,
  digitsPerLevel = 1,
): EvidenceProvider {
  const m = /^([+-]?)(\d+)(?:\.(\d+))?$/.exec(decimalText);
  if (!m) throw new Error(`digitStreamProvider expects a plain decimal, got: ${decimalText}`);
  const negative = m[1] === '-';
  const intPart = BigInt(m[2]);
  const fractionDigits = m[3] ?? '';
  const maxLevel = Math.max(0, Math.ceil(fractionDigits.length / digitsPerLevel));
  return {
    id,
    maxLevel,
    evidenceAt: (level) => {
      const shown = Math.min(fractionDigits.length, digitsPerLevel * (level + 1));
      const frac = fractionDigits.slice(0, shown);
      let mantissa = intPart * 10n ** BigInt(shown) + BigInt(frac === '' ? '0' : frac);
      if (negative) mantissa = -mantissa;
      const c = { num: mantissa, den: 10n ** BigInt(shown) };
      const radius: Rational = { num: 1n, den: 10n ** BigInt(shown) };
      return { centre: c, errors: [{ token, radius }] };
    },
  };
}

export function describeSession(s: CompareSession): string {
  return `L@${s.leftLevel}=${describe(s.leftEvidence())}  R@${s.rightLevel}=${describe(s.rightEvidence())}  budgetUsed=${s.budgetUsed}`;
}

export { rationalToString };
