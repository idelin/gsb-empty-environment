import type {
  AnyNode,
  Expr,
  FnDeclStmt,
  Program,
  Stmt,
} from './ast.ts';

export class RuntimeError extends Error {
  node: AnyNode;
  constructor(message: string, node: AnyNode) {
    super(`${message} (line ${node.line})`);
    this.node = node;
  }
}

export class SideEffectError extends Error {}

export type Value = number | string | boolean | null | FnValue | NativeFn;

export interface FnValue {
  kind: 'fn';
  decl: FnDeclStmt;
  closure: Env;
}

export interface NativeFn {
  kind: 'native';
  name: string;
  fn: (args: Value[], ctx: Ctx) => Value;
}

export interface Ctx {
  output: string[];
  depth: number;
}

export class Env {
  parent: Env | null;
  vars: Map<string, Value>;

  constructor(parent: Env | null = null) {
    this.parent = parent;
    this.vars = new Map();
  }

  define(name: string, value: Value): void {
    this.vars.set(name, value);
  }

  lookup(name: string): Env | null {
    if (this.vars.has(name)) return this;
    return this.parent ? this.parent.lookup(name) : null;
  }

  get(name: string, node: AnyNode): Value {
    const found = this.lookup(name);
    if (!found) throw new RuntimeError(`undefined variable '${name}'`, node);
    return found.vars.get(name) as Value;
  }

  set(name: string, value: Value, node: AnyNode): void {
    const found = this.lookup(name);
    if (!found) throw new RuntimeError(`assignment to undefined variable '${name}'`, node);
    found.vars.set(name, value);
  }

  setOrDefine(name: string, value: Value): void {
    const found = this.lookup(name);
    (found ?? this).vars.set(name, value);
  }
}

export interface BeforeEvent {
  node: Stmt;
  env: Env;
  depth: number;
}

export type ExecGen = Generator<BeforeEvent, void, void>;

class ReturnSignal {
  value: Value;
  constructor(value: Value) {
    this.value = value;
  }
}

export function isTruthy(value: Value): boolean {
  if (value === null || value === false) return false;
  if (value === 0 || value === '') return false;
  return true;
}

export function display(value: Value): string {
  if (value === null) return 'null';
  if (typeof value === 'object') {
    return value.kind === 'native' ? `<native ${value.name}>` : `<fn ${value.decl.name}>`;
  }
  return String(value);
}

function valuesEqual(a: Value, b: Value): boolean {
  return a === b;
}

function typeName(value: Value): string {
  if (value === null) return 'null';
  if (typeof value === 'object') return 'function';
  return typeof value;
}

export function applyUnary(op: string, arg: Value, node: AnyNode): Value {
  if (op === '-') {
    if (typeof arg !== 'number') throw new RuntimeError(`unary '-' needs a number, got ${typeName(arg)}`, node);
    return -arg;
  }
  if (op === '!') return !isTruthy(arg);
  throw new RuntimeError(`unknown unary operator '${op}'`, node);
}

export function applyBinary(op: string, left: Value, right: Value, node: AnyNode): Value {
  switch (op) {
    case '+':
      if (typeof left === 'number' && typeof right === 'number') return left + right;
      if (typeof left === 'string' || typeof right === 'string') return display(left) + display(right);
      throw new RuntimeError(`'+' needs numbers or strings, got ${typeName(left)} and ${typeName(right)}`, node);
    case '-':
    case '*':
    case '/':
    case '%': {
      if (typeof left !== 'number' || typeof right !== 'number') {
        throw new RuntimeError(`'${op}' needs numbers, got ${typeName(left)} and ${typeName(right)}`, node);
      }
      if ((op === '/' || op === '%') && right === 0) {
        throw new RuntimeError(`division by zero`, node);
      }
      if (op === '-') return left - right;
      if (op === '*') return left * right;
      if (op === '/') return left / right;
      return left % right;
    }
    case '<':
    case '<=':
    case '>':
    case '>=': {
      if (typeof left !== 'number' || typeof right !== 'number') {
        throw new RuntimeError(`'${op}' needs numbers, got ${typeName(left)} and ${typeName(right)}`, node);
      }
      if (op === '<') return left < right;
      if (op === '<=') return left <= right;
      if (op === '>') return left > right;
      return left >= right;
    }
    case '==':
      return valuesEqual(left, right);
    case '!=':
      return !valuesEqual(left, right);
    default:
      throw new RuntimeError(`unknown operator '${op}'`, node);
  }
}

export function* evalExpr(expr: Expr, env: Env, ctx: Ctx): Generator<BeforeEvent, Value, void> {
  switch (expr.kind) {
    case 'Num':
    case 'Str':
    case 'Bool':
      return expr.value;
    case 'Null':
      return null;
    case 'Ident':
      return env.get(expr.name, expr);
    case 'Unary': {
      const arg = yield* evalExpr(expr.arg, env, ctx);
      return applyUnary(expr.op, arg, expr);
    }
    case 'Binary': {
      if (expr.op === '&&') {
        const left = yield* evalExpr(expr.left, env, ctx);
        if (!isTruthy(left)) return left;
        return yield* evalExpr(expr.right, env, ctx);
      }
      if (expr.op === '||') {
        const left = yield* evalExpr(expr.left, env, ctx);
        if (isTruthy(left)) return left;
        return yield* evalExpr(expr.right, env, ctx);
      }
      const left = yield* evalExpr(expr.left, env, ctx);
      const right = yield* evalExpr(expr.right, env, ctx);
      return applyBinary(expr.op, left, right, expr);
    }
    case 'Assign': {
      const value = yield* evalExpr(expr.value, env, ctx);
      env.set(expr.name, value, expr);
      return value;
    }
    case 'Call': {
      const callee = yield* evalExpr(expr.callee, env, ctx);
      const args: Value[] = [];
      for (const argExpr of expr.args) args.push(yield* evalExpr(argExpr, env, ctx));
      if (typeof callee === 'object' && callee !== null && callee.kind === 'native') {
        return callee.fn(args, ctx);
      }
      if (typeof callee === 'object' && callee !== null && callee.kind === 'fn') {
        if (args.length !== callee.decl.params.length) {
          throw new RuntimeError(
            `function '${callee.decl.name}' expects ${callee.decl.params.length} args, got ${args.length}`,
            expr,
          );
        }
        const local = new Env(callee.closure);
        callee.decl.params.forEach((param, i) => local.define(param, args[i]));
        ctx.depth++;
        try {
          for (const stmt of callee.decl.body.body) {
            const signal = yield* execStmt(stmt, local, ctx);
            if (signal instanceof ReturnSignal) return signal.value;
          }
          return null;
        } finally {
          ctx.depth--;
        }
      }
      throw new RuntimeError(`value of type ${typeName(callee)} is not callable`, expr);
    }
  }
}

export function* execStmt(stmt: Stmt, env: Env, ctx: Ctx): Generator<BeforeEvent, ReturnSignal | null, void> {
  yield { node: stmt, env, depth: ctx.depth };
  switch (stmt.kind) {
    case 'Let': {
      const value = yield* evalExpr(stmt.init, env, ctx);
      env.define(stmt.name, value);
      return null;
    }
    case 'ExprStmt': {
      yield* evalExpr(stmt.expr, env, ctx);
      return null;
    }
    case 'Block': {
      const child = new Env(env);
      for (const inner of stmt.body) {
        const signal = yield* execStmt(inner, child, ctx);
        if (signal instanceof ReturnSignal) return signal;
      }
      return null;
    }
    case 'If': {
      const cond = yield* evalExpr(stmt.cond, env, ctx);
      if (isTruthy(cond)) return yield* execStmt(stmt.thenBranch, env, ctx);
      if (stmt.elseBranch) return yield* execStmt(stmt.elseBranch, env, ctx);
      return null;
    }
    case 'While': {
      while (isTruthy(yield* evalExpr(stmt.cond, env, ctx))) {
        const signal = yield* execStmt(stmt.body, env, ctx);
        if (signal instanceof ReturnSignal) return signal;
      }
      return null;
    }
    case 'FnDecl': {
      env.define(stmt.name, { kind: 'fn', decl: stmt, closure: env });
      return null;
    }
    case 'Return': {
      const value = stmt.value ? yield* evalExpr(stmt.value, env, ctx) : null;
      return new ReturnSignal(value);
    }
  }
}

export function* execProgram(program: Program, env: Env, ctx: Ctx): ExecGen {
  for (const stmt of program.body) {
    const signal = yield* execStmt(stmt, env, ctx);
    if (signal instanceof ReturnSignal) {
      throw new RuntimeError(`'return' outside of a function`, stmt);
    }
  }
}

export function evalPureExpr(expr: Expr, env: Env): Value {
  switch (expr.kind) {
    case 'Num':
    case 'Str':
    case 'Bool':
      return expr.value;
    case 'Null':
      return null;
    case 'Ident':
      return env.get(expr.name, expr);
    case 'Unary':
      return applyUnary(expr.op, evalPureExpr(expr.arg, env), expr);
    case 'Binary': {
      if (expr.op === '&&') {
        const left = evalPureExpr(expr.left, env);
        if (!isTruthy(left)) return left;
        return evalPureExpr(expr.right, env);
      }
      if (expr.op === '||') {
        const left = evalPureExpr(expr.left, env);
        if (isTruthy(left)) return left;
        return evalPureExpr(expr.right, env);
      }
      return applyBinary(expr.op, evalPureExpr(expr.left, env), evalPureExpr(expr.right, env), expr);
    }
    case 'Assign':
      throw new SideEffectError(`assignment '${expr.name} = ...' is not allowed in a breakpoint condition`);
    case 'Call':
      throw new SideEffectError(`function calls are not allowed in a breakpoint condition`);
  }
}
