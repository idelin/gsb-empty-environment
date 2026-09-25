/**
 * Three truth values.
 *
 * - TRUE  / FALSE have their classical meaning.
 * - MISSING is Kleene "unknown": the attribute is absent, so the value is
 *   neither true nor false. Under this semantics x OR NOT x is MISSING when x
 *   is missing, i.e. the law of excluded middle does NOT hold on a
 *   lossy/nullable domain.
 */
export type TriValue = 0 | 1 | 2;

export const FALSE: TriValue = 0;
export const TRUE: TriValue = 1;
export const MISSING: TriValue = 2;

export function triNot(a: TriValue): TriValue {
  if (a === MISSING) return MISSING;
  return a === TRUE ? FALSE : TRUE;
}

/** Strong Kleene AND (null-propagating, monotone). */
export function triAnd(a: TriValue, b: TriValue): TriValue {
  if (a === FALSE || b === FALSE) return FALSE;
  if (a === TRUE && b === TRUE) return TRUE;
  return MISSING;
}

/** Strong Kleene OR (null-propagating, monotone). */
export function triOr(a: TriValue, b: TriValue): TriValue {
  if (a === TRUE || b === TRUE) return TRUE;
  if (a === FALSE && b === FALSE) return FALSE;
  return MISSING;
}

export function triName(v: TriValue): string {
  return v === TRUE ? "TRUE" : v === FALSE ? "FALSE" : "MISSING";
}
