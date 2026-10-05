/**
 * 表达式语言：常量、变量、四则运算、短路逻辑（&&/||）、条件表达式。
 * 每个语法树节点在创建时分配一个全局唯一且稳定的 id，
 * 优化前后 id 不变，用于把可观察行为（变量读取、错误）对应回来源位置。
 */

export type Value = number | boolean;

export type ErrorKind = 'DivByZero' | 'TypeError' | 'UnboundVar';

export interface EvalError {
  kind: ErrorKind;
  /** 引发错误的操作的稳定 id（来源节点 id） */
  opId: number;
  message: string;
}

export type BinOp = '+' | '-' | '*' | '/';

export type Expr =
  | { kind: 'const'; id: number; value: Value }
  | { kind: 'var'; id: number; name: string }
  | { kind: 'bin'; id: number; op: BinOp; left: Expr; right: Expr }
  | { kind: 'and'; id: number; left: Expr; right: Expr }
  | { kind: 'or'; id: number; left: Expr; right: Expr }
  | { kind: 'cond'; id: number; cond: Expr; then: Expr; else: Expr };

/** 带稳定 id 分配的表达式构造器 */
export class Builder {
  private nextId = 1;

  private id(): number {
    return this.nextId++;
  }

  num(value: number): Expr {
    return { kind: 'const', id: this.id(), value };
  }

  bool(value: boolean): Expr {
    return { kind: 'const', id: this.id(), value };
  }

  variable(name: string): Expr {
    return { kind: 'var', id: this.id(), name };
  }

  bin(op: BinOp, left: Expr, right: Expr): Expr {
    return { kind: 'bin', id: this.id(), op, left, right };
  }

  and(left: Expr, right: Expr): Expr {
    return { kind: 'and', id: this.id(), left, right };
  }

  or(left: Expr, right: Expr): Expr {
    return { kind: 'or', id: this.id(), left, right };
  }

  cond(cond: Expr, then: Expr, else_: Expr): Expr {
    return { kind: 'cond', id: this.id(), cond, then, else: else_ };
  }
}

export function showExpr(e: Expr): string {
  switch (e.kind) {
    case 'const':
      return `${JSON.stringify(e.value)}#${e.id}`;
    case 'var':
      return `${e.name}#${e.id}`;
    case 'bin':
      return `(${showExpr(e.left)} ${e.op}#${e.id} ${showExpr(e.right)})`;
    case 'and':
      return `(${showExpr(e.left)} &&#${e.id} ${showExpr(e.right)})`;
    case 'or':
      return `(${showExpr(e.left)} ||#${e.id} ${showExpr(e.right)})`;
    case 'cond':
      return `(${showExpr(e.cond)} ?#${e.id} ${showExpr(e.then)} : ${showExpr(e.else)})`;
  }
}
