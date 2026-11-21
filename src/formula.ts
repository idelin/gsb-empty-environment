import { lookupAtom } from './state.ts';
import type { AbstractState } from './state.ts';

/**
 * Logical vocabulary shared by declarations and implementations.
 *
 * An `Atom` is an opaque proposition about the program state, for example
 * `sanitized(buffer)` or `buffer.closed`. A `Literal` is a signed atom:
 * positive polarity asserts the atom, negative polarity refutes it.
 *
 * This module is mutually recursive with `state.ts`: the formula layer owns
 * the meaning of polarity, the state layer owns storage, and each delegates
 * to the other through ESM live bindings.
 */
export type Atom = string;

export interface Literal {
  readonly atom: Atom;
  readonly polarity: boolean;
}

export function pos(atom: Atom): Literal {
  return { atom, polarity: true };
}

export function neg(atom: Atom): Literal {
  return { atom, polarity: false };
}

export function not(literal: Literal): Literal {
  return { atom: literal.atom, polarity: !literal.polarity };
}

export type LiteralStatus = 'held' | 'refuted' | 'unknown';

/**
 * Evaluate a literal against an abstract state. Delegates the actual lookup
 * to `state.ts`, closing the mutual recursion: state#entails delegates back
 * here for polarity-aware interpretation.
 */
export function evalLiteral(literal: Literal, state: AbstractState): LiteralStatus {
  const value = lookupAtom(state, literal.atom);
  if (value === undefined) {
    return 'unknown';
  }
  return value === literal.polarity ? 'held' : 'refuted';
}

export function isHeld(literal: Literal, state: AbstractState): boolean {
  return evalLiteral(literal, state) === 'held';
}

/** Return the literals from `required` that are not currently held. */
export function unsatisfied(
  required: readonly Literal[],
  state: AbstractState,
): Literal[] {
  return required.filter((literal) => evalLiteral(literal, state) !== 'held');
}

export function sameLiteral(left: Literal, right: Literal): boolean {
  return left.polarity === right.polarity && left.atom === right.atom;
}

export function dedupeLiterals(literals: readonly Literal[]): Literal[] {
  const seen: Literal[] = [];
  for (const literal of literals) {
    if (!seen.some((existing) => sameLiteral(existing, literal))) {
      seen.push(literal);
    }
  }
  return seen;
}

/**
 * Intersection of literal sets: only literals present (with the same
 * polarity) in every set survive. Used to compute what a caller may rely on
 * across all paths reaching the same kind of exit.
 */
export function intersectLiterals(sets: readonly (readonly Literal[])[]): Literal[] {
  if (sets.length === 0) {
    return [];
  }
  return dedupeLiterals(sets[0]).filter((candidate) =>
    sets.every((set) => set.some((literal) => sameLiteral(literal, candidate))),
  );
}
