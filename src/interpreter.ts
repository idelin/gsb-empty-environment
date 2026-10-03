import { isQuoted, type Expr, type QuotedExpr } from './ast.ts';
import { ValidationError } from './errors.ts';
import type { ResourceGuard } from './guard.ts';
import { parse } from './parser.ts';
import { validate, type ValidationPolicy } from './validator.ts';

export type Value = number | string | boolean | null | QuotedExpr;

export interface RuntimeEnv {
  readonly variables: Readonly<Record<string, Value>>;
  readonly guard: ResourceGuard;
  readonly policy: ValidationPolicy;
}

type Builtin = (args: readonly Value[], env: RuntimeEnv, depth: number) => Value;

const requireNumber = (value: Value, fn: string): number => {
  if (typeof value !== 'number') {
    throw new ValidationError(`'${fn}' expects a number, got ${typeof value}`);
  }
  return value;
};

const requireString = (value: Value, fn: string): string => {
  if (typeof value !== 'string') {
    throw new ValidationError(`'${fn}' expects a string, got ${typeof value}`);
  }
  return value;
};

const arity = (name: string, args: readonly Value[], expected: number): void => {
  if (args.length !== expected) {
    throw new ValidationError(`'${name}' expects ${expected} argument(s), got ${args.length}`);
  }
};

const BUILTINS: Readonly<Record<string, Builtin>> = {
  min: (a) => Math.min(...a.map((v) => requireNumber(v, 'min'))),
  max: (a) => Math.max(...a.map((v) => requireNumber(v, 'max'))),
  abs: (a) => (arity('abs', a, 1), Math.abs(requireNumber(a[0]!, 'abs'))),
  floor: (a) => (arity('floor', a, 1), Math.floor(requireNumber(a[0]!, 'floor'))),
  ceil: (a) => (arity('ceil', a, 1), Math.ceil(requireNumber(a[0]!, 'ceil'))),
  round: (a) => (arity('round', a, 1), Math.round(requireNumber(a[0]!, 'round'))),
  len: (a) => (arity('len', a, 1), requireString(a[0]!, 'len').length),
  contains: (a) => (
    arity('contains', a, 2),
    requireString(a[0]!, 'contains').includes(requireString(a[1]!, 'contains'))
  ),
  startsWith: (a) => (
    arity('startsWith', a, 2),
    requireString(a[0]!, 'startsWith').startsWith(requireString(a[1]!, 'startsWith'))
  ),
  endsWith: (a) => (
    arity('endsWith', a, 2),
    requireString(a[0]!, 'endsWith').endsWith(requireString(a[1]!, 'endsWith'))
  ),
  lower: (a) => (arity('lower', a, 1), requireString(a[0]!, 'lower').toLowerCase()),
  upper: (a) => (arity('upper', a, 1), requireString(a[0]!, 'upper').toUpperCase()),
};

function evalBuiltin(args: readonly Value[], env: RuntimeEnv, depth: number): Value {
  arity('eval', args, 1);
  const target = args[0]!;
  if (isQuoted(target)) {
    return evaluateNode(target.expr, env, depth + 1);
  }
  if (typeof target === 'string') {
    // Dynamically generated expressions are parsed and re-validated under the
    // same policy, and share the same guard: no budget amplification.
    const parsed = parse(target);
    validate(parsed, env.policy);
    return evaluateNode(parsed, env, depth + 1);
  }
  throw new ValidationError(`'eval' expects a quoted expression or string, got ${typeof target}`);
}

function toNumber(value: Value, op: string): number {
  if (typeof value !== 'number') {
    throw new ValidationError(`Operator '${op}' expects numbers, got ${typeof value}`);
  }
  return value;
}

function isTruthy(value: Value): boolean {
  if (value === null) return false;
  if (isQuoted(value)) return true;
  return Boolean(value);
}

function valuesEqual(a: Value, b: Value): boolean {
  if (isQuoted(a) || isQuoted(b)) return a === b;
  return a === b;
}

export function evaluateNode(node: Expr, env: RuntimeEnv, depth: number): Value {
  const { guard } = env;
  guard.step();
  guard.chargeMemory(1);
  if (depth > guard.maxCallDepth) {
    throw new ValidationError(`eval recursion exceeds ${guard.maxCallDepth} levels`);
  }

  switch (node.kind) {
    case 'Literal':
      if (typeof node.value === 'string') guard.chargeMemory(node.value.length);
      return node.value;

    case 'Identifier': {
      if (!(node.name in env.variables)) {
        throw new ValidationError(`Unknown identifier '${node.name}'`);
      }
      return env.variables[node.name]!;
    }

    case 'Unary': {
      const operand = evaluateNode(node.operand, env, depth);
      if (node.op === '!') return !isTruthy(operand);
      return -toNumber(operand, '-');
    }

    case 'Binary': {
      const op = node.op;
      if (op === '&&') {
        const left = evaluateNode(node.left, env, depth);
        return isTruthy(left) ? evaluateNode(node.right, env, depth) : left;
      }
      if (op === '||') {
        const left = evaluateNode(node.left, env, depth);
        return isTruthy(left) ? left : evaluateNode(node.right, env, depth);
      }
      const left = evaluateNode(node.left, env, depth);
      const right = evaluateNode(node.right, env, depth);
      switch (op) {
        case '+': {
          if (typeof left === 'string' || typeof right === 'string') {
            const result = String(unwrap(left)) + String(unwrap(right));
            guard.chargeMemory(result.length);
            return result;
          }
          return toNumber(left, op) + toNumber(right, op);
        }
        case '-':
          return toNumber(left, op) - toNumber(right, op);
        case '*':
          return toNumber(left, op) * toNumber(right, op);
        case '/':
          return toNumber(left, op) / toNumber(right, op);
        case '%':
          return toNumber(left, op) % toNumber(right, op);
        case '==':
          return valuesEqual(left, right);
        case '!=':
          return !valuesEqual(left, right);
        case '<':
          return compare(left, right, op) < 0;
        case '<=':
          return compare(left, right, op) <= 0;
        case '>':
          return compare(left, right, op) > 0;
        case '>=':
          return compare(left, right, op) >= 0;
      }
      throw new ValidationError(`Unsupported operator '${op}'`);
    }

    case 'Call': {
      const args = node.args.map((arg) => evaluateNode(arg, env, depth));
      if (node.callee === 'eval') return evalBuiltin(args, env, depth);
      const fn = BUILTINS[node.callee];
      if (!fn) {
        throw new ValidationError(`Call to unauthorized function '${node.callee}' is forbidden`);
      }
      const result = fn(args, env, depth);
      if (typeof result === 'string') guard.chargeMemory(result.length);
      return result;
    }
  }
}

function unwrap(value: Value): string | number | boolean | null {
  return isQuoted(value) ? '[expr]' : value;
}

function compare(a: Value, b: Value, op: string): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b;
  if (typeof a === 'string' && typeof b === 'string') return a < b ? -1 : a > b ? 1 : 0;
  throw new ValidationError(`Operator '${op}' cannot compare ${typeof a} and ${typeof b}`);
}
