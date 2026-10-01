/**
 * Correctly rounded fixed-point roots on BigInt.
 *
 * Input and output precision are independent:
 *   - the input is the exact decimal  x * 10^(-inScale)   (x: BigInt)
 *   - the result is the k-th root rounded to `outScale` decimal places,
 *     returned as an integer count of ulps (1 ulp = 10^(-outScale)).
 *
 * Everything is exact integer arithmetic. The scaled value
 *     v = (x * 10^(-inScale))^(1/k) * 10^outScale
 * satisfies  v^k = A / B  with
 *     A = x * 10^max(k*outScale - inScale, 0)
 *     B = 10^max(inScale - k*outScale, 0)
 * so floor(v) = iroot(floor(A/B), k)  (valid because m^k is an integer and
 * the k-th root is monotone), and the midpoint test
 *     v >= floor(v) + 1/2   <=>   2^k * A  >=  (2*floor(v)+1)^k * B
 * is an exact integer power comparison. When inScale > k*outScale the input
 * is NOT truncated first: the denominator B is kept symbolically, which is
 * what keeps extremely small inputs from collapsing to 0.
 */
import { bitLength, irootRem, isqrtRem, pow } from "./integer-roots.ts";
import type { RoundingMode } from "./rounding.ts";

export interface RootBudget {
  /** absolute bound on both decimal scales */
  readonly maxScale: number;
  /** bit-size budget for the largest intermediate operand */
  readonly maxBits: number;
}

export const DEFAULT_BUDGET: RootBudget = {
  maxScale: 10_000,
  maxBits: 1_000_000,
};

export interface FixedRootResult {
  /** correctly rounded root, in units of 10^(-outScale) */
  readonly rounded: bigint;
  /** greatest integer <= exact scaled value (lower integer bracket) */
  readonly floor: bigint;
  /** least integer >= exact scaled value (upper integer bracket) */
  readonly ceil: bigint;
  /** true iff the scaled value is an integer (remainder === 0) */
  readonly exact: boolean;
  /**
   * exact remainder A - floor^k * B, in units of B = 10^max(inScale-k*outScale,0);
   * 0 <= remainder, and remainder === 0 iff exact
   */
  readonly remainder: bigint;
  /** worst-case distance from the exact value, in ulps of 10^(-outScale) */
  readonly ulpErrorBound: "1/2" | "1";
  readonly mode: RoundingMode;
  readonly degree: number;
  readonly outScale: number;
  /** Newton iterations used by the underlying integer root (termination audit) */
  readonly iterations: number;
}

function checkScale(name: string, scale: number, budget: RootBudget): void {
  if (!Number.isInteger(scale)) {
    throw new RangeError(`${name} must be an integer, got ${scale}`);
  }
  if (Math.abs(scale) > budget.maxScale) {
    throw new RangeError(
      `${name} |${scale}| exceeds budget maxScale=${budget.maxScale}`,
    );
  }
}

function computeRoot(
  x: bigint,
  inScale: number,
  outScale: number,
  degree: number,
  mode: RoundingMode,
  budget: RootBudget,
  fastPath: boolean,
): FixedRootResult {
  if (x < 0n) throw new RangeError("rootFixed: negative input rejected");
  if (!Number.isInteger(degree) || degree < 2) {
    throw new RangeError("rootFixed: degree must be an integer >= 2");
  }
  checkScale("inScale", inScale, budget);
  checkScale("outScale", outScale, budget);

  const e = degree * outScale - inScale;
  const A = e >= 0 ? x * pow(10n, e) : x;
  const B = e >= 0 ? 1n : pow(10n, -e);

  // Budget: bound the bit size of the largest operands we will form.
  // The midpoint comparison needs (2r+1)^degree * B with r <= (A/B)^(1/degree).
  const workBits =
    bitLength(A) + bitLength(B) + degree * (bitLength(A / B) + 2);
  if (workBits > budget.maxBits) {
    throw new RangeError(
      `rootFixed: operand size ~${workBits} bits exceeds budget maxBits=${budget.maxBits}`,
    );
  }

  if (x === 0n) {
    return {
      rounded: 0n,
      floor: 0n,
      ceil: 0n,
      exact: true,
      remainder: 0n,
      ulpErrorBound: mode === "floor" || mode === "ceil" ? "1" : "1/2",
      mode,
      degree,
      outScale,
      iterations: 0,
    };
  }

  // floor(v) where v^k = A/B: valid because iroot(floor(A/B)) = floor((A/B)^(1/k)).
  const q = A / B;
  const { root: r, iterations } =
    fastPath && degree === 2 ? isqrtRem(q) : irootRem(q, degree);

  const rPow = pow(r, degree);
  const remainder = A - rPow * B; // exact, >= 0 by construction of r
  const exact = remainder === 0n;
  const floorV = r;
  const ceilV = exact ? r : r + 1n;

  // Midpoint test: v >= r + 1/2  <=>  2^degree * A >= (2r+1)^degree * B.
  // Exact integer power comparison; ties (equality) are possible only when
  // B > 1 and are resolved here by the chosen rounding rule.
  const lhs = pow(2n, degree) * A;
  const rhs = pow(2n * r + 1n, degree) * B;

  let rounded: bigint;
  switch (mode) {
    case "floor":
      rounded = floorV;
      break;
    case "ceil":
      rounded = ceilV;
      break;
    case "halfUp":
      rounded = lhs >= rhs ? r + 1n : r;
      break;
    case "halfEven":
      rounded = lhs > rhs || (lhs === rhs && r % 2n === 1n) ? r + 1n : r;
      break;
    default:
      throw new RangeError(`rootFixed: unknown rounding mode ${String(mode)}`);
  }

  return {
    rounded,
    floor: floorV,
    ceil: ceilV,
    exact,
    remainder,
    ulpErrorBound: mode === "floor" || mode === "ceil" ? "1" : "1/2",
    mode,
    degree,
    outScale,
    iterations,
  };
}

/**
 * Correctly rounded k-th root of the exact decimal x * 10^(-inScale),
 * at outScale decimal places. General path (any degree >= 2).
 */
export function rootFixed(
  x: bigint,
  inScale: number,
  outScale: number,
  degree: number,
  mode: RoundingMode = "halfEven",
  budget: RootBudget = DEFAULT_BUDGET,
): FixedRootResult {
  return computeRoot(x, inScale, outScale, degree, mode, budget, false);
}

/**
 * Correctly rounded square root of the exact decimal x * 10^(-inScale),
 * at outScale decimal places. Dedicated fast path: uses the shift-based
 * integer square root instead of the general n-th-root iteration.
 */
export function sqrtFixed(
  x: bigint,
  inScale: number,
  outScale: number,
  mode: RoundingMode = "halfEven",
  budget: RootBudget = DEFAULT_BUDGET,
): FixedRootResult {
  return computeRoot(x, inScale, outScale, 2, mode, budget, true);
}
