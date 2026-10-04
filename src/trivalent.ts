// 三值逻辑（SQL 风格）：真 T、假 F、未知 U。
// 任何含 NULL 的比较都得 UNKNOWN；筛选只保留 TRUE。

export type Tri = 'T' | 'F' | 'U';

export const T: Tri = 'T';
export const F: Tri = 'F';
export const U: Tri = 'U';

export function triNot(a: Tri): Tri {
  if (a === 'T') return 'F';
  if (a === 'F') return 'T';
  return 'U';
}

// SQL AND 真值表：
//   T AND x = x
//   F AND x = F
//   U AND T = U, U AND F = F, U AND U = U
export function triAnd(a: Tri, b: Tri): Tri {
  if (a === 'F' || b === 'F') return 'F';
  if (a === 'T') return b;
  if (b === 'T') return a;
  return 'U';
}

// SQL OR 真值表：
//   T OR x = T
//   F OR x = x
//   U OR T = T, U OR F = U, U OR U = U
export function triOr(a: Tri, b: Tri): Tri {
  if (a === 'T' || b === 'T') return 'T';
  if (a === 'F') return b;
  if (b === 'F') return a;
  return 'U';
}

export function triOf(b: boolean): Tri {
  return b ? 'T' : 'F';
}
