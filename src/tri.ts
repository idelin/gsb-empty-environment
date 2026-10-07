/** Three-valued truth value: true / false / unknown. */
export type Tri = 'T' | 'F' | 'U';

export const triNot = (v: Tri): Tri => (v === 'T' ? 'F' : v === 'F' ? 'T' : 'U');

export const triAnd = (a: Tri, b: Tri): Tri =>
  a === 'F' || b === 'F' ? 'F' : a === 'T' && b === 'T' ? 'T' : 'U';

export const triOr = (a: Tri, b: Tri): Tri =>
  a === 'T' || b === 'T' ? 'T' : a === 'F' && b === 'F' ? 'F' : 'U';

export const triOf = (v: boolean): Tri => (v ? 'T' : 'F');
