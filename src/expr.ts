// 标量表达式与谓词。NULL 用 null 表示；任何涉及 NULL 的比较均为 UNKNOWN。
import type { Tri } from './trivalent.js';
import { T, U, triOf, triAnd, triOr, triNot } from './trivalent.js';

export type Value = number | string | boolean | null;
export type Row = Record<string, Value>;

export type Expr =
  | { kind: 'col'; name: string }
  | { kind: 'lit'; value: Value }
  | { kind: 'not'; expr: Expr }
  | { kind: 'and'; exprs: Expr[] }
  | { kind: 'or'; exprs: Expr[] }
  | { kind: 'cmp'; op: CmpOp; left: Expr; right: Expr };

export type CmpOp = '=' | '<>' | '<' | '<=' | '>' | '>=';

export const col = (name: string): Expr => ({ kind: 'col', name });
export const lit = (value: Value): Expr => ({ kind: 'lit', value });
export const not = (expr: Expr): Expr => ({ kind: 'not', expr });
export const and = (...exprs: Expr[]): Expr => ({ kind: 'and', exprs });
export const or = (...exprs: Expr[]): Expr => ({ kind: 'or', exprs });
export const cmp = (op: CmpOp, left: Expr, right: Expr): Expr => ({ kind: 'cmp', op, left, right });

// 求值结果为三值；任何表达式都可能因 NULL 或比较而得到 UNKNOWN。
export function evalExpr(expr: Expr, row: Row): Tri {
  switch (expr.kind) {
    case 'col': {
      const v = row[expr.name];
      if (v === null || v === undefined) return U;
      if (typeof v === 'boolean') return triOf(v);
      // 非布尔列直接当布尔用是不合法的；这里按 SQL 习惯视为类型错误。
      throw new Error(`column ${expr.name} is not boolean`);
    }
    case 'lit': {
      if (expr.value === null) return U;
      if (typeof expr.value === 'boolean') return triOf(expr.value);
      throw new Error('non-boolean literal used as predicate');
    }
    case 'not':
      return triNot(evalExpr(expr.expr, row));
    case 'and': {
      let acc: Tri = T;
      for (const e of expr.exprs) acc = triAnd(acc, evalExpr(e, row));
      return acc;
    }
    case 'or': {
      let acc: Tri = 'F';
      for (const e of expr.exprs) acc = triOr(acc, evalExpr(e, row));
      return acc;
    }
    case 'cmp':
      return evalCmp(expr.op, evalScalar(expr.left, row), evalScalar(expr.right, row));
  }
}

// 标量求值（允许返回 NULL 与任意值，供比较使用）。
export function evalScalar(expr: Expr, row: Row): Value {
  switch (expr.kind) {
    case 'col': {
      const v = row[expr.name];
      return v === undefined ? null : v;
    }
    case 'lit':
      return expr.value;
    case 'not':
    case 'and':
    case 'or': {
      const t = evalExpr(expr, row);
      return t === 'U' ? null : t === 'T';
    }
    case 'cmp': {
      const t = evalExpr(expr, row);
      return t === 'U' ? null : t === 'T';
    }
  }
}

// 空值比较规则：任一操作数为 NULL => UNKNOWN（含 NULL = NULL）。
export function evalCmp(op: CmpOp, a: Value, b: Value): Tri {
  if (a === null || b === null) return U;
  switch (op) {
    case '=': return triOf(a === b);
    case '<>': return triOf(a !== b);
    case '<': return triOf(a < b);
    case '<=': return triOf(a <= b);
    case '>': return triOf(a > b);
    case '>=': return triOf(a >= b);
  }
}

// 收集表达式引用的列名（下推分析用）。
export function referencedCols(expr: Expr, into: Set<string> = new Set()): Set<string> {
  switch (expr.kind) {
    case 'col':
      into.add(expr.name);
      break;
    case 'lit':
      break;
    case 'not':
      referencedCols(expr.expr, into);
      break;
    case 'and':
    case 'or':
      for (const e of expr.exprs) referencedCols(e, into);
      break;
    case 'cmp':
      referencedCols(expr.left, into);
      referencedCols(expr.right, into);
      break;
  }
  return into;
}

// 将 AND 拍平为合取项列表（下推按合取项粒度进行）。
export function conjuncts(expr: Expr): Expr[] {
  if (expr.kind === 'and') return expr.exprs.flatMap(conjuncts);
  return [expr];
}

export function formatExpr(expr: Expr): string {
  switch (expr.kind) {
    case 'col': return expr.name;
    case 'lit': return expr.value === null ? 'NULL' : JSON.stringify(expr.value);
    case 'not': return `NOT (${formatExpr(expr.expr)})`;
    case 'and': return expr.exprs.map(formatExpr).join(' AND ');
    case 'or': return `(${expr.exprs.map(formatExpr).join(' OR ')})`;
    case 'cmp': return `${formatExpr(expr.left)} ${expr.op} ${formatExpr(expr.right)}`;
  }
}
