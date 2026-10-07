import type { AnyNode, Expr, Program, Stmt } from './ast.ts';
import { parse } from './parser.ts';

export class RuntimeError extends Error {}

export type Value = number | string | boolean | null | FunctionValue | BuiltinValue;

export interface FunctionValue {
  kind: 'fn';
  name: string;
  params: string[];
  body: Stmt[];
  closure: Environment;
}

export interface BuiltinValue {
  kind: 'builtin';
  name: string;
  fn: (args: Value[], ctx: Ctx) => Value;
}

export interface Ctx {
  output: string[];
}

export class Environment {
  vars = new Map<string, Value>();
  readonly parent: Environment | null;

  constructor(parent: Environment | null = null) {
    this.parent = parent;
  }

  declare(name: string, value: Value): void {
    this.vars.set(name, value);
  }

  lookupEnv(name: string): Environment | null {
    let env: Environment | null = this;
    while (env) {
      if (env.vars.has(name)) return env;
      env = env.parent;
    }
    return null;
  }

  get(name: string): Value {
    const env = this.lookupEnv(name);
    if (!env) throw new RuntimeError(`undefined variable '${name}'`);
    return env.vars.get(name)!;
  }

  set(name: string, value: Value): void {
    const env = this.lookupEnv(name);
    if (!env) throw new RuntimeError(`undefined variable '${name}'`);
    env.vars.set(name, value);
  }
}

export class ReturnSignal {
  readonly value: Value;

  constructor(value: Value) {
    this.value = value;
  }
}

export interface BeforeEvent {
  type: 'before';
  node: AnyNode;
  env: Environment;
}

export function truthy(value: Value): boolean {
  if (value === null || value === false) return false;
  if (value === 0 || value === '') return false;
  return true;
}

export function formatValue(value: Value): string {
  if (value === null) return 'null';
  if (typeof value === 'object') return `<fn ${value.name}>`;
  return String(value);
}

function isFunctionValue(value: Value): value is FunctionValue {
  return typeof value === 'object' && value !== null && value.kind === 'fn';
}

export function collectEnvs(start: Environment): Environment[] {
  const seen = new Set<Environment>();
  const out: Environment[] = [];
  const visit = (env: Environment | null): void => {
    if (!env || seen.has(env)) return;
    seen.add(env);
    out.push(env);
    visit(env.parent);
    for (const value of env.vars.values()) {
      if (isFunctionValue(value)) visit(value.closure);
    }
  };
  visit(start);
  return out;
}

function before(node: AnyNode, env: Environment): BeforeEvent {
  return { type: 'before', node, env };
}

function needNumber(value: Value, op: string): number {
  if (typeof value !== 'number') {
    throw new RuntimeError(`operator '${op}' expects numbers, got ${formatValue(value)}`);
  }
  return value;
}

export function* evalExpr(node: Expr, env: Environment, ctx: Ctx): Generator<BeforeEvent, Value, void> {
  yield before(node, env);
  switch (node.kind) {
    case 'NumberLit':
    case 'StringLit':
    case 'BoolLit':
      return node.value;
    case 'Identifier':
      return env.get(node.name);
    case 'Unary': {
      const arg = yield* evalExpr(node.argument, env, ctx);
      if (node.op === '!') return !truthy(arg);
      return -needNumber(arg, node.op);
    }
    case 'Binary': {
      if (node.op === '&&') {
        const left = yield* evalExpr(node.left, env, ctx);
        return truthy(left) ? yield* evalExpr(node.right, env, ctx) : left;
      }
      if (node.op === '||') {
        const left = yield* evalExpr(node.left, env, ctx);
        return truthy(left) ? left : yield* evalExpr(node.right, env, ctx);
      }
      const left = yield* evalExpr(node.left, env, ctx);
      const right = yield* evalExpr(node.right, env, ctx);
      switch (node.op) {
        case '+':
          if (typeof left === 'number' && typeof right === 'number') return left + right;
          if (typeof left === 'string' || typeof right === 'string') {
            return formatValue(left) + formatValue(right);
          }
          throw new RuntimeError(`operator '+' cannot combine ${formatValue(left)} and ${formatValue(right)}`);
        case '-':
          return needNumber(left, '-') - needNumber(right, '-');
        case '*':
          return needNumber(left, '*') * needNumber(right, '*');
        case '/':
          return needNumber(left, '/') / needNumber(right, '/');
        case '%':
          return needNumber(left, '%') % needNumber(right, '%');
        case '<':
          return needNumber(left, '<') < needNumber(right, '<');
        case '<=':
          return needNumber(left, '<=') <= needNumber(right, '<=');
        case '>':
          return needNumber(left, '>') > needNumber(right, '>');
        case '>=':
          return needNumber(left, '>=') >= needNumber(right, '>=');
        case '==':
        case '===':
          return left === right;
        case '!=':
        case '!==':
          return left !== right;
        default:
          throw new RuntimeError(`unknown operator '${node.op}'`);
      }
    }
    case 'Assign': {
      const value = yield* evalExpr(node.value, env, ctx);
      env.set(node.name, value);
      return value;
    }
    case 'Call': {
      const callee = yield* evalExpr(node.callee, env, ctx);
      const args: Value[] = [];
      for (const argExpr of node.args) {
        args.push(yield* evalExpr(argExpr, env, ctx));
      }
      if (typeof callee !== 'object' || callee === null) {
        throw new RuntimeError(`cannot call ${formatValue(callee)}`);
      }
      if (callee.kind === 'builtin') {
        return callee.fn(args, ctx);
      }
      const callEnv = new Environment(callee.closure);
      callee.params.forEach((param, index) => {
        callEnv.declare(param, index < args.length ? args[index] : null);
      });
      try {
        for (const stmt of callee.body) {
          yield* execStmt(stmt, callEnv, ctx);
        }
      } catch (signal) {
        if (signal instanceof ReturnSignal) return signal.value;
        throw signal;
      }
      return null;
    }
  }
}

export function* execStmt(node: Stmt, env: Environment, ctx: Ctx): Generator<BeforeEvent, void, void> {
  yield before(node, env);
  switch (node.kind) {
    case 'VarDecl': {
      const value = node.init ? yield* evalExpr(node.init, env, ctx) : null;
      env.declare(node.name, value);
      return;
    }
    case 'ExprStmt':
      yield* evalExpr(node.expr, env, ctx);
      return;
    case 'Block': {
      const blockEnv = new Environment(env);
      for (const stmt of node.body) {
        yield* execStmt(stmt, blockEnv, ctx);
      }
      return;
    }
    case 'If': {
      const test = yield* evalExpr(node.test, env, ctx);
      if (truthy(test)) {
        yield* execStmt(node.consequent, env, ctx);
      } else if (node.alternate) {
        yield* execStmt(node.alternate, env, ctx);
      }
      return;
    }
    case 'While':
      while (truthy(yield* evalExpr(node.test, env, ctx))) {
        yield* execStmt(node.body, env, ctx);
      }
      return;
    case 'For': {
      const loopEnv = new Environment(env);
      if (node.init) yield* execStmt(node.init, loopEnv, ctx);
      while (true) {
        if (node.test && !truthy(yield* evalExpr(node.test, loopEnv, ctx))) break;
        yield* execStmt(node.body, loopEnv, ctx);
        if (node.update) yield* evalExpr(node.update, loopEnv, ctx);
      }
      return;
    }
    case 'FunctionDecl':
      env.declare(node.name, {
        kind: 'fn',
        name: node.name,
        params: node.params,
        body: node.body,
        closure: env,
      });
      return;
    case 'Return': {
      const value = node.argument ? yield* evalExpr(node.argument, env, ctx) : null;
      throw new ReturnSignal(value);
    }
  }
}

export function* runProgramGen(program: Program, ctx: Ctx): Generator<BeforeEvent, void, void> {
  const global = new Environment(null);
  global.declare('print', {
    kind: 'builtin',
    name: 'print',
    fn: (args, c) => {
      c.output.push(args.map(formatValue).join(' '));
      return null;
    },
  });
  for (const stmt of program.body) {
    yield* execStmt(stmt, global, ctx);
  }
}

export function run(source: string): { output: string[] } {
  const ctx: Ctx = { output: [] };
  const gen = runProgramGen(parse(source), ctx);
  while (!gen.next().done) {
    // drive to completion
  }
  return { output: ctx.output };
}
