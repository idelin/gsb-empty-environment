import {
  type Atom,
  type Literal,
  dedupeLiterals,
  evalLiteral,
  not,
  sameLiteral,
} from './formula.ts';

/**
 * Three-valued abstract state.
 *
 * Each atom maps to `true` (established), `false` (refuted) or `undefined`
 * (no information). A copy-on-write transition model keeps every state
 * immutable, so states attached to different control-flow paths never alias.
 *
 * Mutually recursive with `formula.ts`: this module stores facts and
 * delegates interpretation (polarity, entailment vocabulary) back there.
 */
export class AbstractState {
  readonly facts: ReadonlyMap<Atom, boolean>;

  private constructor(facts: ReadonlyMap<Atom, boolean>) {
    this.facts = facts;
  }

  static empty(): AbstractState {
    return new AbstractState(new Map());
  }

  withAtom(atom: Atom, value: boolean): AbstractState {
    const next = new Map(this.facts);
    next.set(atom, value);
    return new AbstractState(next);
  }

  /** Record an effect: the literal holds after the effect executes. */
  effect(literal: Literal): AbstractState {
    return this.withAtom(literal.atom, literal.polarity);
  }

  /**
   * Assume a literal holds from this point on.
   *
   * Returns `null` when the assumption contradicts an already established
   * fact: such a path is infeasible and must not be treated as a satisfying
   * witness (nor as a counterexample).
   */
  assume(literal: Literal): AbstractState | null {
    const known = this.facts.get(literal.atom);
    if (known !== undefined && known !== literal.polarity) {
      return null;
    }
    return this.withAtom(literal.atom, literal.polarity);
  }

  assumeAll(literals: readonly Literal[]): AbstractState | null {
    let next: AbstractState | null = this;
    for (const literal of literals) {
      if (next === null) {
        return null;
      }
      next = next.assume(literal);
    }
    return next;
  }

  /** Drop every fact mentioning an atom (used by invalidation). */
  forget(atom: Atom): AbstractState {
    if (!this.facts.has(atom)) {
      return this;
    }
    const next = new Map(this.facts);
    next.delete(atom);
    return new AbstractState(next);
  }

  /**
   * Retract literals that were invalidated. Only currently held literals are
   * removed; the weakened state simply loses knowledge rather than asserting
   * the opposite polarity.
   */
  invalidate(literals: readonly Literal[]): AbstractState {
    let next: AbstractState = this;
    for (const literal of dedupeLiterals(literals)) {
      const status = evalLiteral(literal, next);
      if (status === 'held') {
        next = next.forget(literal.atom);
      }
    }
    return next;
  }

  entails(literal: Literal): boolean {
    return evalLiteral(literal, this) === 'held';
  }

  entailsAll(literals: readonly Literal[]): boolean {
    return literals.every((literal) => this.entails(literal));
  }

  /** Snapshot every held fact as positive/negative literals. */
  toLiterals(): Literal[] {
    const literals: Literal[] = [];
    for (const [atom, value] of this.facts) {
      literals.push({ atom, polarity: value });
    }
    return literals;
  }
}

export function lookupAtom(state: AbstractState, atom: Atom): boolean | undefined {
  return state.facts.get(atom);
}

/**
 * Re-export so callers can construct complementary literals without reaching
 * into formula internals; keeps the mutual-recursion boundary explicit.
 */
export { not, sameLiteral };
