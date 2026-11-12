/**
 * Sealed verified summaries. A {@link VerifiedSummary} carries a runtime-only
 * brand plus a structural brand type. The brand symbol is never exported, and
 * {@link sealSummary} is only reachable from internal verifier modules; public
 * entry points never re-export it. Callers therefore cannot turn an arbitrary
 * object into a verified summary.
 */

const VERIFIED_BRAND: unique symbol = Symbol("contract-guard.verified");

export interface ExitSummary {
  readonly tag: string;
  /**
   * Concrete value bound to a result/error reference: the return label for
   * "return" exits, the exception tag for exception exits. It models control
   * identity, never the post-throw state of unrelated object fields.
   */
  readonly label: string;
  readonly allowedLabels: ReadonlyArray<string>;
  readonly post: ReadonlyArray<unknown>;
}

export interface VerifiedSummary {
  readonly __verifiedBrand: typeof VERIFIED_BRAND;
  readonly id: string;
  readonly pre: ReadonlyArray<unknown>;
  readonly exits: ReadonlyArray<ExitSummary>;
}

export interface PathCounterexample {
  readonly nodePath: ReadonlyArray<string>;
  readonly input: Readonly<Record<string, unknown>>;
  readonly exitTag: string;
  readonly reason:
    | "return-label-not-allowed"
    | "missing-contract-exit"
    | "post-condition-failed"
    | "unhandled-throw"
    | "unverified-callee"
    | "infeasible-assumption"
    | "malformed-model";
  readonly detail: string;
}

export interface VerificationFailure {
  readonly ok: false;
  readonly id: string;
  readonly counterexamples: ReadonlyArray<PathCounterexample>;
}

export interface VerificationSuccess {
  readonly ok: true;
  readonly id: string;
  readonly summary: VerifiedSummary;
}

export type VerificationResult = VerificationSuccess | VerificationFailure;

/** Internal only: stamp a plain summary with the unforgeable brand. */
export function sealSummary(
  id: string,
  pre: ReadonlyArray<unknown>,
  exits: ReadonlyArray<ExitSummary>,
): VerifiedSummary {
  return {
    __verifiedBrand: VERIFIED_BRAND,
    id,
    pre,
    exits,
  };
}

export function isVerifiedSummary(value: unknown): value is VerifiedSummary {
  return (
    typeof value === "object" &&
    value !== null &&
    (value as { __verifiedBrand?: unknown }).__verifiedBrand === VERIFIED_BRAND
  );
}
