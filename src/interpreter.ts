import type { Expr, Value } from './ast.ts';

export const I64_MIN: bigint = -(1n << 63n);
export const I64_MAX: bigint = (1n << 63n) - 1n;

export type ErrorKind =
  | 'Overflow'
  | 'TypeError'
  | 'UndefinedVariable'
  | 'StepsExhausted'
  | 'SpaceExhausted'
  | 'DivisionByZero'
  | 'ForbiddenNode'
  | 'Cancelled'
  | 'InvalidInput';

export interface EvalError {
  kind: ErrorKind;
  path: string; // AST path such as $/args/0 or $/body/left
  message: string;
}

export class EvalFailure extends Error {
  readonly error: EvalError;
  constructor(error: EvalError) {
    super(error.message);
    this.name = 'EvalFailure';
    this.error = error;
  }
}

// One token per in-flight request: cancelling it never touches other requests.
export class CancelToken {
  private flag = false;
  get cancelled(): boolean {
    return this.flag;
  }
  cancel(): void {
    this.flag = true;
  }
}

export interface Budget {
  steps: number; // 1 per AST node visit + 1 per array element read
  space: number; // 1 per element of every array created by map/filter
}

// Tenant-level in-flight space accounting, supplied by the service layer.
export interface SpaceLedger {
  reserve(n: number, path: string): void; // throws EvalFailure(SpaceExhausted)
}

export interface EvaluateOptions {
  ast: Expr;
  variables?: Record<string, unknown>;
  budget: Budget;
  token?: CancelToken;
  ledger?: SpaceLedger;
  sliceSteps?: number; // work units between event-loop yields
}

export type EvalOutcome =
  | { ok: true; value: Value; stepsUsed: number; spaceUsed: number }
  | { ok: false; error: EvalError; stepsUsed: number; spaceUsed: number };

interface Env {
  parent: Env | null;
  vars: Map<string, Value>;
}

// Explicit interpretation stack frame: evaluation never recurses, so AST
// depth is bounded by heap, not by the JS call stack.
interface Frame {
  node: Expr;
  path: string;
  env: Env;
  pc: number;
  kids: Value[];
  src: Value[] | null;
  idx: number;
  acc: Value | undefined;
  out: Value[] | null;
  iterEnv: Env | null;
}

const yieldToEventLoop = (): Promise<void> =>
  new Promise((resolve) => {
    setImmediate(resolve);
  });

export async function evaluate(options: EvaluateOptions): Promise<EvalOutcome> {
  const budget = options.budget;
  const token = options.token;
  const ledger = options.ledger;
  const sliceSteps = Math.max(1, options.sliceSteps ?? 256);

  let stepsUsed = 0;
  let spaceUsed = 0;

  const fail = (kind: ErrorKind, path: string, message: string): never => {
    throw new EvalFailure({ kind, path, message });
  };

  // Every budget check happens BEFORE the action it pays for.
  const takeStep = (path: string, action: string): void => {
    if (stepsUsed + 1 > budget.steps) {
      fail('StepsExhausted', path, `step budget exhausted before ${action} (budget ${budget.steps})`);
    }
    stepsUsed += 1;
  };

  const takeSpace = (n: number, path: string): void => {
    if (n <= 0) return;
    if (spaceUsed + n > budget.space) {
      fail('SpaceExhausted', path, `request space budget exhausted before allocating ${n} slot(s) (budget ${budget.space})`);
    }
    if (ledger) ledger.reserve(n, path); // may throw tenant-level SpaceExhausted
    spaceUsed += n;
  };

  const checkRange = (v: bigint, path: string): bigint => {
    if (v < I64_MIN || v > I64_MAX) {
      fail('Overflow', path, `integer overflow: ${v} is outside the signed 64-bit range`);
    }
    return v;
  };

  const typeName = (v: Value): string => (Array.isArray(v) ? 'array' : typeof v);

  const expectInt = (v: Value, path: string, what: string): bigint => {
    if (typeof v !== 'bigint') fail('TypeError', path, `${what} must be an integer, got ${typeName(v)}`);
    return v;
  };

  const expectArray = (v: Value, path: string, what: string): Value[] => {
    if (!Array.isArray(v)) fail('TypeError', path, `${what} must be an array, got ${typeName(v)}`);
    return v;
  };

  const toI64 = (raw: string | number | bigint, path: string): bigint => {
    let v: bigint;
    if (typeof raw === 'bigint') {
      v = raw;
    } else if (typeof raw === 'number') {
      if (!Number.isInteger(raw)) fail('InvalidInput', path, `integer literal expected, got ${raw}`);
      v = BigInt(raw);
    } else {
      try {
        v = BigInt(raw);
      } catch {
        return fail('InvalidInput', path, `invalid integer literal '${raw}'`);
      }
    }
    return checkRange(v, path);
  };

  const toValue = (raw: unknown, path: string): Value => {
    if (typeof raw === 'boolean') return raw;
    if (typeof raw === 'bigint') return checkRange(raw, path);
    if (typeof raw === 'number') {
      if (!Number.isInteger(raw)) fail('InvalidInput', path, `expected an integer, got ${raw}`);
      return checkRange(BigInt(raw), path);
    }
    if (Array.isArray(raw)) return raw.map((x, i) => toValue(x, `${path}/${i}`));
    return fail('InvalidInput', path, `unsupported input value of type ${typeof raw}`);
  };

  const lookup = (env: Env, name: string): Value | undefined => {
    let cur: Env | null = env;
    while (cur !== null) {
      if (cur.vars.has(name)) return cur.vars.get(name);
      cur = cur.parent;
    }
    return undefined;
  };

  const rootEnv: Env = { parent: null, vars: new Map() };
  for (const [name, raw] of Object.entries(options.variables ?? {})) {
    rootEnv.vars.set(name, toValue(raw, `variables/${name}`));
  }

  const stack: Frame[] = [];
  let lastValue: Value = false;

  const push = (node: Expr, path: string, env: Env): void => {
    takeStep(path, `visiting node '${(node as { kind: string }).kind}'`);
    stack.push({ node, path, env, pc: 0, kids: [], src: null, idx: 0, acc: undefined, out: null, iterEnv: null });
  };

  // Returns true when the frame completed; its value is then in lastValue.
  const step = (f: Frame): boolean => {
    const node = f.node as Record<string, any>;
    switch (node.kind) {
      case 'int': {
        lastValue = toI64(node.value, f.path);
        return true;
      }
      case 'bool': {
        lastValue = node.value;
        return true;
      }
      case 'var': {
        const v = lookup(f.env, node.name);
        if (v === undefined) fail('UndefinedVariable', f.path, `variable '${node.name}' is not defined`);
        lastValue = v;
        return true;
      }
      case 'neg': {
        if (f.pc === 0) {
          f.pc = 1;
          push(node.arg, `${f.path}/arg`, f.env);
          return false;
        }
        lastValue = checkRange(-expectInt(lastValue, f.path, `'neg' operand`), f.path);
        return true;
      }
      case 'arith': {
        if (f.pc === 0) {
          f.pc = 1;
          push(node.left, `${f.path}/left`, f.env);
          return false;
        }
        if (f.pc === 1) {
          f.kids[0] = lastValue;
          f.pc = 2;
          push(node.right, `${f.path}/right`, f.env);
          return false;
        }
        const l = expectInt(f.kids[0], f.path, 'left operand');
        const r = expectInt(lastValue, f.path, 'right operand');
        let v: bigint;
        switch (node.op) {
          case 'add': v = l + r; break;
          case 'sub': v = l - r; break;
          case 'mul': v = l * r; break;
          case 'div':
            if (r === 0n) fail('DivisionByZero', f.path, 'integer division by zero');
            v = l / r; // truncates toward zero
            break;
          case 'mod':
            if (r === 0n) fail('DivisionByZero', f.path, 'integer modulo by zero');
            v = l % r;
            break;
          default:
            return fail('ForbiddenNode', f.path, `operator '${String(node.op)}' is not allowed`);
        }
        lastValue = checkRange(v, f.path);
        return true;
      }
      case 'cmp': {
        if (f.pc === 0) {
          f.pc = 1;
          push(node.left, `${f.path}/left`, f.env);
          return false;
        }
        if (f.pc === 1) {
          f.kids[0] = lastValue;
          f.pc = 2;
          push(node.right, `${f.path}/right`, f.env);
          return false;
        }
        const l = f.kids[0];
        const r = lastValue;
        let v: boolean;
        if (typeof l === 'bigint' && typeof r === 'bigint') {
          switch (node.op) {
            case 'eq': v = l === r; break;
            case 'ne': v = l !== r; break;
            case 'lt': v = l < r; break;
            case 'le': v = l <= r; break;
            case 'gt': v = l > r; break;
            case 'ge': v = l >= r; break;
            default:
              return fail('ForbiddenNode', f.path, `operator '${String(node.op)}' is not allowed`);
          }
        } else if (typeof l === 'boolean' && typeof r === 'boolean' && (node.op === 'eq' || node.op === 'ne')) {
          v = node.op === 'eq' ? l === r : l !== r;
        } else {
          return fail('TypeError', f.path, `cannot compare ${typeName(l)} with ${typeName(r)} using '${String(node.op)}'`);
        }
        lastValue = v;
        return true;
      }
      case 'and':
      case 'or': {
        const isAnd = node.kind === 'and';
        if (f.pc > 0) {
          const v = lastValue;
          if (typeof v !== 'boolean') fail('TypeError', f.path, `'${node.kind}' expects boolean operands`);
          // Short-circuit: remaining args are never pushed, hence never counted.
          if (isAnd && v === false) {
            lastValue = false;
            return true;
          }
          if (!isAnd && v === true) {
            lastValue = true;
            return true;
          }
        }
        if (f.pc < node.args.length) {
          const i = f.pc;
          f.pc += 1;
          push(node.args[i], `${f.path}/args/${i}`, f.env);
          return false;
        }
        lastValue = isAnd; // empty 'and' is true, empty 'or' is false
        return true;
      }
      case 'not': {
        if (f.pc === 0) {
          f.pc = 1;
          push(node.arg, `${f.path}/arg`, f.env);
          return false;
        }
        const v = lastValue;
        if (typeof v !== 'boolean') fail('TypeError', f.path, `'not' expects a boolean operand`);
        lastValue = !v;
        return true;
      }
      case 'if': {
        if (f.pc === 0) {
          f.pc = 1;
          push(node.cond, `${f.path}/cond`, f.env);
          return false;
        }
        if (f.pc === 1) {
          const c = lastValue;
          if (typeof c !== 'boolean') fail('TypeError', f.path, `'if' condition must be a boolean`);
          f.pc = 2;
          // Only the taken branch is visited, so only it is counted.
          push(c ? node.then : node.else, `${f.path}/${c ? 'then' : 'else'}`, f.env);
          return false;
        }
        return true;
      }
      case 'map':
      case 'filter': {
        if (f.pc === 0) {
          f.pc = 1;
          push(node.list, `${f.path}/list`, f.env);
          return false;
        }
        if (f.pc === 1) {
          f.src = expectArray(lastValue, f.path, `'${node.kind}' source`);
          f.idx = 0;
          f.out = [];
          f.iterEnv = { parent: f.env, vars: new Map() };
          f.pc = 2;
          return false;
        }
        if (f.pc === 2) {
          if (f.idx >= f.src.length) {
            lastValue = f.out;
            return true;
          }
          takeStep(f.path, `reading element ${f.idx} of '${node.kind}' source`);
          const el = f.src[f.idx];
          f.kids[0] = el;
          f.iterEnv!.vars.set(node.param, el);
          f.pc = 3;
          push(node.body, `${f.path}/body`, f.iterEnv!);
          return false;
        }
        if (node.kind === 'map') {
          takeSpace(1, f.path); // reserve before appending
          f.out!.push(lastValue);
        } else {
          const keep = lastValue;
          if (typeof keep !== 'boolean') fail('TypeError', f.path, `'filter' body must return a boolean`);
          if (keep) {
            takeSpace(1, f.path);
            f.out!.push(f.kids[0]);
          }
        }
        f.idx += 1;
        f.pc = 2;
        return false;
      }
      case 'fold': {
        if (f.pc === 0) {
          f.pc = 1;
          push(node.list, `${f.path}/list`, f.env);
          return false;
        }
        if (f.pc === 1) {
          f.src = expectArray(lastValue, f.path, `'fold' source`);
          f.pc = 2;
          push(node.init, `${f.path}/init`, f.env);
          return false;
        }
        if (f.pc === 2) {
          f.acc = lastValue;
          f.idx = 0;
          f.iterEnv = { parent: f.env, vars: new Map() };
          f.pc = 3;
          return false;
        }
        if (f.pc === 3) {
          if (f.idx >= f.src.length) {
            lastValue = f.acc!;
            return true;
          }
          takeStep(f.path, `reading element ${f.idx} of 'fold' source`);
          f.iterEnv!.vars.set(node.param, f.src[f.idx]);
          f.iterEnv!.vars.set(node.acc, f.acc!);
          f.pc = 4;
          push(node.body, `${f.path}/body`, f.iterEnv!);
          return false;
        }
        f.acc = lastValue;
        f.idx += 1;
        f.pc = 3;
        return false;
      }
      default:
        return fail(
          'ForbiddenNode',
          f.path,
          `node kind '${String(node.kind)}' is not allowed: no eval, no host functions, no dynamic imports`,
        );
    }
  };

  push(options.ast, '$', rootEnv);
  let work = 0;
  try {
    while (stack.length > 0) {
      if (token?.cancelled) {
        fail('Cancelled', stack[stack.length - 1].path, 'request cancelled');
      }
      work += 1;
      if (work >= sliceSteps) {
        work = 0;
        await yieldToEventLoop(); // slice boundary: let other requests run
      }
      const f = stack[stack.length - 1];
      if (step(f)) stack.pop();
    }
    return { ok: true, value: lastValue, stepsUsed, spaceUsed };
  } catch (e) {
    if (e instanceof EvalFailure) {
      return { ok: false, error: e.error, stepsUsed, spaceUsed };
    }
    throw e;
  }
}
