import {
  BudgetExceeded,
  DepthExceededError,
  EvaluationError,
  EvaluationTimeoutError,
  MemoryExceeded,
} from './errors.ts';
import { parse, type AstNode } from './parser.ts';
import { validate } from './validator.ts';

export type Value = number | string | boolean | null;

export type BuiltinFn = (args: Value[], ctx: EvalContext) => Value;

/** Mutable budget ledger, shared by reference across nested eval() calls. */
export interface Budget {
  stepsUsed: number;
  memoryUsed: number;
  lastDeadlineCheck: number;
}

export interface EvalContext {
  budget: Budget;
  maxSteps: number;
  maxMemoryBytes: number;
  timeoutMs: number;
  deadline: number;
  depth: number;
  maxDepth: number;
  variables: Record<string, Value>;
  functions: Record<string, BuiltinFn>;
}

const DEADLINE_CHECK_INTERVAL = 64;

/** Charge `n` instruction steps against the shared budget. */
export function charge(ctx: EvalContext, n = 1): void {
  ctx.budget.stepsUsed += n;
  if (ctx.budget.stepsUsed > ctx.maxSteps) {
    throw new BudgetExceeded(ctx.maxSteps);
  }
  if (ctx.budget.stepsUsed - ctx.budget.lastDeadlineCheck >= DEADLINE_CHECK_INTERVAL) {
    ctx.budget.lastDeadlineCheck = ctx.budget.stepsUsed;
    if (Date.now() >= ctx.deadline) {
      throw new EvaluationTimeoutError(ctx.timeoutMs);
    }
  }
}

/** Charge `bytes` of memory against the shared budget. */
export function alloc(ctx: EvalContext, bytes: number): void {
  ctx.budget.memoryUsed += bytes;
  if (ctx.budget.memoryUsed > ctx.maxMemoryBytes) {
    throw new MemoryExceeded(ctx.maxMemoryBytes);
  }
}

function toNumber(value: Value, op: string): number {
  if (typeof value !== 'number') {
    throw new EvaluationError(`operator '${op}' expects numbers, got ${typeof value}`);
  }
  return value;
}

function evalNode(node: AstNode, ctx: EvalContext): Value {
  charge(ctx);
  switch (node.kind) {
    case 'num':
      return node.value;
    case 'str':
      alloc(ctx, node.value.length);
      return node.value;
    case 'bool':
      return node.value;
    case 'null':
      return null;
    case 'ident':
      return ctx.variables[node.name] ?? null;
    case 'unary': {
      const arg = evalNode(node.arg, ctx);
      if (node.op === '!') return !arg;
      return -toNumber(arg, node.op);
    }
    case 'binary':
      return evalBinary(node, ctx);
    case 'call': {
      const fn = ctx.functions[node.name];
      if (!fn) {
        // Unreachable after validation, but stay safe if the validator is bypassed.
        throw new EvaluationError(`function '${node.name}' is not available`);
      }
      const args = node.args.map((a) => evalNode(a, ctx));
      charge(ctx); // calling a builtin costs an extra step
      return fn(args, ctx);
    }
  }
}

function evalBinary(node: Extract<AstNode, { kind: 'binary' }>, ctx: EvalContext): Value {
  const { op } = node;
  if (op === '&&') {
    const left = evalNode(node.left, ctx);
    return left ? evalNode(node.right, ctx) : false;
  }
  if (op === '||') {
    const left = evalNode(node.left, ctx);
    return left ? true : evalNode(node.right, ctx);
  }
  const left = evalNode(node.left, ctx);
  const right = evalNode(node.right, ctx);
  switch (op) {
    case '+':
      if (typeof left === 'string' && typeof right === 'string') {
        alloc(ctx, left.length + right.length);
        return left + right;
      }
      return toNumber(left, op) + toNumber(right, op);
    case '-':
      return toNumber(left, op) - toNumber(right, op);
    case '*':
      return toNumber(left, op) * toNumber(right, op);
    case '/':
      return toNumber(left, op) / toNumber(right, op);
    case '%':
      return toNumber(left, op) % toNumber(right, op);
    case '==':
      return left === right;
    case '!=':
      return left !== right;
    case '<':
      return toNumber(left, op) < toNumber(right, op);
    case '<=':
      return toNumber(left, op) <= toNumber(right, op);
    case '>':
      return toNumber(left, op) > toNumber(right, op);
    case '>=':
      return toNumber(left, op) >= toNumber(right, op);
    default:
      throw new EvaluationError(`unsupported operator '${op}'`);
  }
}

function expectNumbers(args: Value[], name: string): number[] {
  return args.map((a) => {
    if (typeof a !== 'number') throw new EvaluationError(`${name}() expects number arguments`);
    return a;
  });
}

/** Sandbox-internal builtins. None of them touch the host environment. */
export function defaultFunctions(): Record<string, BuiltinFn> {
  return {
    abs: (args) => Math.abs(expectNumbers(args, 'abs')[0]),
    floor: (args) => Math.floor(expectNumbers(args, 'floor')[0]),
    ceil: (args) => Math.ceil(expectNumbers(args, 'ceil')[0]),
    round: (args) => Math.round(expectNumbers(args, 'round')[0]),
    min: (args) => Math.min(...expectNumbers(args, 'min')),
    max: (args) => Math.max(...expectNumbers(args, 'max')),
    len: (args) => {
      const [s] = args;
      if (typeof s !== 'string') throw new EvaluationError('len() expects a string');
      return s.length;
    },
    concat: (args, ctx) => {
      const parts = args.map((a) => {
        if (typeof a !== 'string') throw new EvaluationError('concat() expects string arguments');
        return a;
      });
      const total = parts.reduce((n, p) => n + p.length, 0);
      alloc(ctx, total);
      return parts.join('');
    },
    repeat: (args, ctx) => {
      const [s, n] = args;
      if (typeof s !== 'string' || typeof n !== 'number') {
        throw new EvaluationError('repeat() expects (string, number)');
      }
      if (!Number.isInteger(n) || n < 0) throw new EvaluationError('repeat() count must be a non-negative integer');
      alloc(ctx, s.length * n); // charged BEFORE the allocation happens
      return s.repeat(n);
    },
    // Sandboxed self-evaluation: re-parses, re-validates and evaluates the
    // given source inside the SAME budget context. This is not the host
    // `eval`; it can only run sandbox expressions and cannot escape.
    eval: (args, ctx) => {
      const [src] = args;
      if (typeof src !== 'string') throw new EvaluationError('eval() expects a string source');
      alloc(ctx, src.length);
      if (ctx.depth + 1 > ctx.maxDepth) {
        throw new DepthExceededError(ctx.maxDepth);
      }
      return evaluateSource(src, { ...ctx, depth: ctx.depth + 1 });
    },
  };
}

export interface EvaluateOptions {
  maxSteps?: number;
  maxMemoryBytes?: number;
  timeoutMs?: number;
  maxDepth?: number;
  variables?: Record<string, Value>;
  functions?: Record<string, BuiltinFn>;
  budget?: Budget;
  depth?: number;
}

export interface EvalResult {
  value: Value;
  stepsUsed: number;
  memoryUsed: number;
}

/** Parse, validate and evaluate a source string within a fresh (or supplied) budget. */
export function evaluateSource(source: string, options: EvaluateOptions = {}): Value {
  return evaluateDetailed(source, options).value;
}

export function evaluateDetailed(source: string, options: EvaluateOptions = {}): EvalResult {
  const {
    maxSteps = 1000,
    maxMemoryBytes = 1_000_000,
    timeoutMs = 100,
    maxDepth = 5000,
    variables = {},
    functions = {},
    budget = { stepsUsed: 0, memoryUsed: 0, lastDeadlineCheck: 0 },
    depth = 0,
  } = options;

  const allFunctions: Record<string, BuiltinFn> = { ...defaultFunctions(), ...functions };
  const allVariables: Record<string, Value> = { ...variables, self: source };

  const ast = parse(source);
  validate(ast, {
    variables: new Set(Object.keys(allVariables)),
    functions: new Set(Object.keys(allFunctions)),
  });

  const ctx: EvalContext = {
    budget,
    maxSteps,
    maxMemoryBytes,
    timeoutMs,
    deadline: timeoutMs === Infinity ? Infinity : Date.now() + timeoutMs,
    depth,
    maxDepth,
    variables: allVariables,
    functions: allFunctions,
  };

  try {
    const value = evalNode(ast, ctx);
    return { value, stepsUsed: budget.stepsUsed, memoryUsed: budget.memoryUsed };
  } catch (err) {
    // Attach consumed resources so callers (e.g. QuotaGuard) can bill tenants
    // even for aborted evaluations.
    if (err instanceof Error) {
      (err as Error & { stepsUsed?: number; memoryUsed?: number }).stepsUsed = budget.stepsUsed;
      (err as Error & { memoryUsed?: number }).memoryUsed = budget.memoryUsed;
    }
    throw err;
  }
}
