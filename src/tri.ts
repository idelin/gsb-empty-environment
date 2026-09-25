/**
 * Three-valued Kleene logic. `unknown` is a first-class outcome and is never
 * collapsed to `false`.
 */
export type Tri = 'true' | 'false' | 'unknown';

export const T: Tri = 'true';
export const F: Tri = 'false';
export const U: Tri = 'unknown';

export function triNot(a: Tri): Tri {
  if (a === T) return F;
  if (a === F) return T;
  return U;
}

export function triAnd(a: Tri, b: Tri): Tri {
  if (a === F || b === F) return F;
  if (a === T && b === T) return T;
  return U;
}

export function triOr(a: Tri, b: Tri): Tri {
  if (a === T || b === T) return T;
  if (a === F && b === F) return F;
  return U;
}

export function triOfBoolean(b: boolean): Tri {
  return b ? T : F;
}
