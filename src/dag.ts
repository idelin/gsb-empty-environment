/**
 * 有理表达式 DAG：延迟约分 + 位长预算。
 * 运算时只做未约分的分子/分母组合，位长超预算时才尝试约分；
 * 仍超预算则判定 incomplete。比较走交叉乘，无需展开十进制串。
 */
import { Rational, bitLength, gcd } from './rational.ts';

export type Expr =
  | { readonly kind: 'const'; readonly value: Rational }
  | { readonly kind: 'add' | 'sub' | 'mul' | 'div'; readonly a: Expr; readonly b: Expr };

export const lit = (num: bigint | number, den: bigint | number = 1n): Expr => ({
  kind: 'const',
  value: Rational.of(num, den),
});
export const add = (a: Expr, b: Expr): Expr => ({ kind: 'add', a, b });
export const sub = (a: Expr, b: Expr): Expr => ({ kind: 'sub', a, b });
export const mul = (a: Expr, b: Expr): Expr => ({ kind: 'mul', a, b });
export const div = (a: Expr, b: Expr): Expr => ({ kind: 'div', a, b });

export interface EvalBudget {
  /** 中间结果分子/分母允许的最大二进制位长 */
  maxBits: number;
}

export type EvalResult =
  | { status: 'ok'; value: Rational }
  | { status: 'incomplete'; reason: string };

export type CompareResult =
  | { status: 'ok'; order: -1 | 0 | 1 }
  | { status: 'incomplete'; reason: string };

class BudgetExceeded extends Error {}
class DivByZero extends Error {}

/** 未约分的懒惰分数：只在需要时（超预算或最终输出）才做 gcd 约分 */
class LazyFrac {
  num: bigint;
  den: bigint;
  reduced: boolean;

  constructor(num: bigint, den: bigint, reduced = false) {
    this.num = num;
    this.den = den;
    this.reduced = reduced;
  }

  bits(): number {
    return Math.max(bitLength(this.num), bitLength(this.den));
  }

  reduce(): void {
    if (this.reduced) return;
    if (this.den < 0n) {
      this.num = -this.num;
      this.den = -this.den;
    }
    if (this.num === 0n) {
      this.den = 1n;
    } else {
      const g = gcd(this.num, this.den);
      this.num /= g;
      this.den /= g;
    }
    this.reduced = true;
  }
}

function combine(op: 'add' | 'sub' | 'mul' | 'div', x: LazyFrac, y: LazyFrac, maxBits: number): LazyFrac {
  let out: LazyFrac;
  switch (op) {
    case 'add':
      out = new LazyFrac(x.num * y.den + y.num * x.den, x.den * y.den);
      break;
    case 'sub':
      out = new LazyFrac(x.num * y.den - y.num * x.den, x.den * y.den);
      break;
    case 'mul':
      out = new LazyFrac(x.num * y.num, x.den * y.den);
      break;
    case 'div':
      if (y.num === 0n) throw new DivByZero('division by zero in expression');
      out = new LazyFrac(x.num * y.den, x.den * y.num);
      break;
  }
  if (out.bits() > maxBits) {
    // 延迟约分在此触发：先约分再复查，仍超预算才算 incomplete
    out.reduce();
    if (out.bits() > maxBits) {
      throw new BudgetExceeded(`bit-length ${out.bits()} exceeds budget ${maxBits}`);
    }
  }
  return out;
}

/** 自底向上惰性求值；同一 DAG 节点只计算一次（按对象记忆化） */
export function evaluate(expr: Expr, budget: EvalBudget = { maxBits: 4096 }): EvalResult {
  const memo = new Map<Expr, LazyFrac>();
  const visit = (e: Expr): LazyFrac => {
    const hit = memo.get(e);
    if (hit) return hit;
    const f =
      e.kind === 'const'
        ? new LazyFrac(e.value.num, e.value.den, true)
        : combine(e.kind, visit(e.a), visit(e.b), budget.maxBits);
    memo.set(e, f);
    return f;
  };
  try {
    const f = visit(expr);
    f.reduce();
    return { status: 'ok', value: new Rational(f.num, f.den) };
  } catch (err) {
    if (err instanceof BudgetExceeded || err instanceof DivByZero) {
      return { status: 'incomplete', reason: err.message };
    }
    throw err;
  }
}

/** 交叉乘比较两个表达式；任一超预算即 incomplete，绝不展开十进制 */
export function compareExprs(a: Expr, b: Expr, budget: EvalBudget = { maxBits: 4096 }): CompareResult {
  const ra = evaluate(a, budget);
  if (ra.status !== 'ok') return ra;
  const rb = evaluate(b, budget);
  if (rb.status !== 'ok') return rb;
  return { status: 'ok', order: ra.value.cmp(rb.value) };
}
