import type { Literal } from './formula.ts';
import type { ReturnTag } from './model.ts';

/**
 * A verified summary is the *only* artifact callers may consume. The brand
 * is private to this package: user code cannot forge a summary without
 * going through `verifyContract`, and an unverified object is rejected.
 */
export const VERIFIED_BRAND: unique symbol = Symbol('contract.verified');

export interface VerifiedSummary {
  readonly [VERIFIED_BRAND]: true;
  readonly contract: string;
  readonly precondition: readonly Literal[];
  readonly returnTags: readonly ReturnTag[];
  readonly returns: Readonly<Record<ReturnTag, readonly Literal[]>>;
  readonly throws: Readonly<Record<string, readonly Literal[]>>;
}

export function isVerifiedSummary(value: unknown): value is VerifiedSummary {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as { [VERIFIED_BRAND]?: unknown })[VERIFIED_BRAND] === true
  );
}
