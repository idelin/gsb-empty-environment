/**
 * Candidate ordering — the explicit tie-breaking ("决胜") rules.
 *
 * Given a target x and two candidates a = pa/qa, b = pb/qb (qa, qb <= N):
 *   1. smaller |x - p/q| wins;
 *   2. tie -> smaller denominator wins;
 *   3. tie -> smaller |numerator| wins (closer to zero);
 *   4. tie -> larger numerator wins (prefers the non-negative one;
 *      can only matter for x = 0 with p = +/-k, which is already decided
 *      exactly by 0/1, so this rule is just a total-order backstop).
 *
 * All comparisons are exact BigInt cross-multiplications.
 */

import { absBigInt, type Rational } from "./rational.ts";

/** -1 if a is strictly closer to target, 0 if equidistant, 1 if b is closer. */
export function compareError(target: Rational, a: Rational, b: Rational): number {
  const ea = absBigInt(target.num * a.den - a.num * target.den) * b.den;
  const eb = absBigInt(target.num * b.den - b.num * target.den) * a.den;
  return ea < eb ? -1 : ea > eb ? 1 : 0;
}

/** Full tie-breaking order: negative => a wins, positive => b wins, 0 => identical. */
export function compareCandidates(target: Rational, a: Rational, b: Rational): number {
  const e = compareError(target, a, b);
  if (e !== 0) return e;
  if (a.den !== b.den) return a.den < b.den ? -1 : 1;
  const aa = absBigInt(a.num);
  const ab = absBigInt(b.num);
  if (aa !== ab) return aa < ab ? -1 : 1;
  if (a.num !== b.num) return a.num > b.num ? -1 : 1;
  return 0;
}
