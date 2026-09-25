/** Kleene 三值逻辑：unknown 绝不退化为 false。 */
export type Tri = 'true' | 'false' | 'unknown';

export function triOf(value: boolean): Tri {
  return value ? 'true' : 'false';
}

export function triNot(a: Tri): Tri {
  if (a === 'true') return 'false';
  if (a === 'false') return 'true';
  return 'unknown';
}

export function triAnd(a: Tri, b: Tri): Tri {
  if (a === 'false' || b === 'false') return 'false';
  if (a === 'true' && b === 'true') return 'true';
  return 'unknown';
}

export function triOr(a: Tri, b: Tri): Tri {
  if (a === 'true' || b === 'true') return 'true';
  if (a === 'false' && b === 'false') return 'false';
  return 'unknown';
}
