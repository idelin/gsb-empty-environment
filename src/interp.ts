// Reference tree-walking interpreter for the surface AST.
// Environments are first-class: closures capture the defining environment
// object, so mutations through one closure are visible to its siblings.

import type { Expr, Program, Stmt } from './ast.ts';
import { ReturnSignal, formatValue, truthy } from './common.ts';

export type Value = number | boolean | null | ClosureVal;

export interface ClosureVal {
  kind: 'closure';
  name: string;
  params: string[];
  body: Stmt[];
  env: Env;
}

export class Env {
  slots: Map<string, Value> = new Map();
  parent: Env | null;

  constructor(parent: Env | null) {
    this.parent = parent;
  }

  resolve(name: string): Env | null {
    let env: Env | null = this;
    while (env !== null) {
      if (env.slots.has(name)) return env;
      env = env.parent;
    }
    return null;
  }
}

export function runProgram(prog: Program, out: string[]): Value {
  const env = new Env(null);
  try {
    execBlock(prog, env, out);
  } catch (signal) {
    if (signal instanceof ReturnSignal) return signal.value as Value;
    throw signal;
  }
  return null;
}

function execBlock(stmts: Stmt[], env: Env, out: string[]): void {
  for (const stmt of stmts) execStmt(stmt, env, out);
}

function execStmt(stmt: Stmt, env: Env, out: string[]): void {
  switch (stmt.tag) {
    case 'let':
      env.slots.set(stmt.name, evalExpr(stmt.init, env, out));
      return;
    case 'assign': {
      const target = env.resolve(stmt.name);
      if (!target) throw new Error(`undefined variable: ${stmt.name}`);
      target.slots.set(stmt.name, evalExpr(stmt.value, env, out));
      return;
    }
    case 'func':
      env.slots.set(stmt.name, {
        kind: 'closure',
        name: stmt.name,
        params: stmt.params,
        body: stmt.body,
        env,
      });
      return;
    case 'if':
      execBlock(truthy(evalExpr(stmt.cond, env, out)) ? stmt.then : stmt.else, env, out);
      return;
    case 'while':
      // Fresh environment per iteration: `let` in the body is per-iteration,
      // so closures created in different iterations capture distinct slots.
      while (truthy(evalExpr(stmt.cond, env, out))) {
        execBlock(stmt.body, new Env(env), out);
      }
      return;
    case 'return':
      throw new ReturnSignal(evalExpr(stmt.value, env, out));
    case 'print':
      out.push(formatValue(evalExpr(stmt.value, env, out)));
      return;
    case 'expr':
      evalExpr(stmt.value, env, out);
      return;
  }
}

function evalExpr(expr: Expr, env: Env, out: string[]): Value {
  switch (expr.tag) {
    case 'num':
      return expr.value;
    case 'bool':
      return expr.value;
    case 'null':
      return null;
    case 'var': {
      const target = env.resolve(expr.name);
      if (!target) throw new Error(`undefined variable: ${expr.name}`);
      return target.slots.get(expr.name) as Value;
    }
    case 'bin': {
      const left = evalExpr(expr.left, env, out);
      const right = evalExpr(expr.right, env, out);
      switch (expr.op) {
        case '+':
          return (left as number) + (right as number);
        case '-':
          return (left as number) - (right as number);
        case '*':
          return (left as number) * (right as number);
        case '<':
          return (left as number) < (right as number);
        case '<=':
          return (left as number) <= (right as number);
        case '>':
          return (left as number) > (right as number);
        case '>=':
          return (left as number) >= (right as number);
        case '==':
          return left === right;
        case '!=':
          return left !== right;
      }
      throw new Error(`unknown operator: ${expr.op}`);
    }
    case 'call': {
      const fn = evalExpr(expr.fn, env, out);
      if (typeof fn !== 'object' || fn === null || fn.kind !== 'closure') {
        throw new Error('attempted to call a non-function');
      }
      const args = expr.args.map((arg) => evalExpr(arg, env, out));
      const frame = new Env(fn.env);
      fn.params.forEach((param, index) => {
        frame.slots.set(param, index < args.length ? args[index] : null);
      });
      try {
        execBlock(fn.body, frame, out);
      } catch (signal) {
        if (signal instanceof ReturnSignal) return signal.value as Value;
        throw signal;
      }
      return null;
    }
  }
}
