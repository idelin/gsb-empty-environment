/**
 * 策略表达式（用户侧 AST）。只允许：
 * 布尔字面量、有限整数集合成员、整数区间比较、and/or/not。
 */
export type Expr =
  | { kind: 'lit'; value: boolean }
  | { kind: 'in'; attr: string; values: readonly number[] }
  | { kind: 'range'; attr: string; min: number | null; max: number | null }
  | { kind: 'and'; args: readonly Expr[] }
  | { kind: 'or'; args: readonly Expr[] }
  | { kind: 'not'; arg: Expr };

function assertAttr(attr: string): void {
  if (typeof attr !== 'string' || attr.length === 0) {
    throw new Error('attribute name must be a non-empty string');
  }
}

function assertInt(value: number, what: string): void {
  if (!Number.isInteger(value)) {
    throw new Error(`${what} must be an integer, got ${value}`);
  }
}

export function lit(value: boolean): Expr {
  return { kind: 'lit', value };
}

/** 有限集合成员条件；集合必须有限且全为整数。 */
export function inSet(attr: string, values: readonly number[]): Expr {
  assertAttr(attr);
  if (!Array.isArray(values)) throw new Error('set must be a finite array');
  for (const v of values) assertInt(v, 'set member');
  return { kind: 'in', attr, values: [...new Set(values)].sort((x, y) => x - y) };
}

/** 整数区间条件；min/max 可为 null 表示开区间端。 */
export function inRange(attr: string, min: number | null, max: number | null): Expr {
  assertAttr(attr);
  if (min !== null) assertInt(min, 'range min');
  if (max !== null) assertInt(max, 'range max');
  if (min !== null && max !== null && min > max) {
    throw new Error(`empty range [${min}, ${max}]`);
  }
  return { kind: 'range', attr, min, max };
}

export function and(...args: readonly Expr[]): Expr {
  return { kind: 'and', args };
}

export function or(...args: readonly Expr[]): Expr {
  return { kind: 'or', args };
}

export function not(arg: Expr): Expr {
  return { kind: 'not', arg };
}
