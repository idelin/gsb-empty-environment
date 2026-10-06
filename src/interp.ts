// Reference tree-walking interpreter over the ORIGINAL AST. This defines the
// language semantics: lexical scoping, by-reference capture of mutable
// bindings, fresh bindings per block/loop-iteration execution.

import { Program, Stmt, Expr, FnExpr } from './ast';
import { evalBinOp, truthy, formatValue } from './semantics';

export type Value = number | boolean | null | IClosure | Value[];

export interface IClosure { fn: FnExpr; env: Env }

class Cell {
  constructor(public value: Value) {}
}

class Env {
  private vars = new Map<string, Cell>();
  constructor(public parent: Env | null) {}
  define(name: string, value: Value): void {
    this.vars.set(name, new Cell(value));
  }
  lookup(name: string): Cell {
    let env: Env | null = this;
    while (env) {
      const cell = env.vars.get(name);
      if (cell) return cell;
      env = env.parent;
    }
    throw new Error(`unbound variable "${name}"`);
  }
}

class ReturnSignal {
  constructor(public value: Value) {}
}

function isClosure(v: Value): v is IClosure {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

export interface InterpResult {
  value: Value | null;
  output: string[];
}

export function run(program: Program): InterpResult {
  const output: string[] = [];
  const global = new Env(null);
  let value: Value | null = null;
  try {
    execStmts(global, program.body);
  } catch (sig) {
    if (sig instanceof ReturnSignal) value = sig.value;
    else throw sig;
  }
  return { value, output };

  function execStmts(env: Env, stmts: Stmt[]): void {
    for (const s of stmts) execStmt(env, s);
  }

  function execStmt(env: Env, s: Stmt): void {
    switch (s.kind) {
      case 'let':
        env.define(s.name, evalExpr(env, s.init));
        return;
      case 'set':
        env.lookup(s.name).value = evalExpr(env, s.value);
        return;
      case 'expr':
        evalExpr(env, s.expr);
        return;
      case 'block':
        execStmts(new Env(env), s.body);
        return;
      case 'if':
        execStmts(new Env(env), truthy(evalExpr(env, s.cond)) ? s.then : s.else);
        return;
      case 'while':
        while (truthy(evalExpr(env, s.cond))) {
          execStmts(new Env(env), s.body); // fresh bindings per iteration
        }
        return;
      case 'return':
        throw new ReturnSignal(s.expr ? evalExpr(env, s.expr) : null);
      case 'print':
        output.push(formatValue(evalExpr(env, s.expr)));
        return;
    }
  }

  function evalExpr(env: Env, e: Expr): Value {
    switch (e.kind) {
      case 'num':
      case 'bool':
        return e.value;
      case 'var':
        return env.lookup(e.name).value;
      case 'bin':
        return evalBinOp(e.op, evalExpr(env, e.left), evalExpr(env, e.right));
      case 'not':
        return !truthy(evalExpr(env, e.expr));
      case 'call': {
        const fnv = evalExpr(env, e.fn);
        if (!isClosure(fnv)) throw new Error('cannot call non-function');
        const args = e.args.map((a) => evalExpr(env, a));
        if (args.length !== fnv.fn.params.length) {
          throw new Error(`arity mismatch: expected ${fnv.fn.params.length}, got ${args.length}`);
        }
        const callEnv = new Env(fnv.env);
        if (fnv.fn.selfName) callEnv.define(fnv.fn.selfName, fnv);
        fnv.fn.params.forEach((p, i) => callEnv.define(p, args[i]));
        try {
          execStmts(callEnv, fnv.fn.body);
          return null;
        } catch (sig) {
          if (sig instanceof ReturnSignal) return sig.value;
          throw sig;
        }
      }
      case 'fn':
        return { fn: e, env };
      case 'arr':
        return e.items.map((item) => evalExpr(env, item));
      case 'index': {
        const arr = evalExpr(env, e.arr);
        const i = evalExpr(env, e.index);
        if (!Array.isArray(arr) || typeof i !== 'number') {
          throw new Error('index expects (array, number)');
        }
        if (i < 0 || i >= arr.length) throw new Error('index out of bounds');
        return arr[i];
      }
      case 'push': {
        const arr = evalExpr(env, e.arr);
        if (!Array.isArray(arr)) throw new Error('push expects an array');
        arr.push(evalExpr(env, e.value));
        return arr;
      }
      case 'len': {
        const arr = evalExpr(env, e.arr);
        if (!Array.isArray(arr)) throw new Error('len expects an array');
        return arr.length;
      }
    }
  }
}
