import type { Literal } from './formula.ts';
import { unsatisfied } from './formula.ts';
import type { ReturnTag } from './model.ts';
import { AbstractState } from './state.ts';
import { isVerifiedSummary, type VerifiedSummary } from './summary.ts';

/**
 * Caller-side API.
 *
 * Only a `VerifiedSummary` produced by the verifier is accepted; a raw
 * declaration, an implementation model, or a failed verification result are
 * rejected both at compile time (no matching overload) and at runtime
 * (brand check). This is the enforcement point for "callers only use
 * verified summaries".
 */

export class UnverifiedContractError extends TypeError {
  constructor(message: string) {
    super(message);
    this.name = 'UnverifiedContractError';
  }
}

export class PreconditionViolationError extends TypeError {
  constructor(message: string) {
    super(message);
    this.name = 'PreconditionViolationError';
  }
}

export class UnknownExitError extends TypeError {
  constructor(message: string) {
    super(message);
    this.name = 'UnknownExitError';
  }
}

/** Call with caller-provided facts stated as literals. */
export function invoke(
  summary: VerifiedSummary,
  callerFacts: readonly Literal[],
  exit: { kind: 'return'; tag: ReturnTag },
): readonly Literal[];
export function invoke(
  summary: VerifiedSummary,
  callerFacts: readonly Literal[],
  exit: { kind: 'throw'; label: string },
): readonly Literal[];
/** Call when the caller simply assumes the precondition holds. */
export function invoke(
  summary: VerifiedSummary,
  exit: { kind: 'return'; tag: ReturnTag },
): readonly Literal[];
export function invoke(
  summary: VerifiedSummary,
  exit: { kind: 'throw'; label: string },
): readonly Literal[];
export function invoke(
  summary: VerifiedSummary,
  factsOrExit:
    | readonly Literal[]
    | { kind: 'return' | 'throw'; tag?: ReturnTag; label?: string },
  maybeExit?: { kind: 'return' | 'throw'; tag?: ReturnTag; label?: string },
): readonly Literal[] {
  if (!isVerifiedSummary(summary)) {
    throw new UnverifiedContractError(
      'invoke() requires a VerifiedSummary from verifyContract(); unverified declarations cannot be called',
    );
  }

  const exit =
    maybeExit ??
    (factsOrExit as { kind: 'return' | 'throw'; tag?: ReturnTag; label?: string });

  // The two-argument overload explicitly relies on the precondition being
  // assumed. The three-argument form must prove it with caller facts.
  if (Array.isArray(factsOrExit)) {
    const callerState = AbstractState.empty().assumeAll(factsOrExit);
    if (callerState === null) {
      throw new PreconditionViolationError('caller supplied contradictory facts');
    }
    const missing = unsatisfied(summary.precondition, callerState);
    if (missing.length > 0) {
      throw new PreconditionViolationError(
        `caller of ${summary.contract} cannot establish precondition: ${missing
          .map((literal) => `${literal.polarity ? '' : '!'}${literal.atom}`)
          .join(', ')}`,
      );
    }
  }

  if (exit.kind === 'return') {
    const tag = exit.tag ?? '';
    const guaranteed = summary.returns[tag];
    if (guaranteed === undefined) {
      throw new UnknownExitError(`verified summary ${summary.contract} has no return tag "${tag}"`);
    }
    return guaranteed;
  }

  const label = exit.label ?? '';
  const guaranteed = summary.throws[label];
  if (guaranteed === undefined) {
    throw new UnknownExitError(`verified summary ${summary.contract} has no exception "${label}"`);
  }
  return guaranteed;
}

/** Type-level guard usable in generic caller code. */
export function assertVerified(value: unknown): asserts value is VerifiedSummary {
  if (!isVerifiedSummary(value)) {
    throw new UnverifiedContractError('value is not a VerifiedSummary');
  }
}
