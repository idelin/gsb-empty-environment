export type Value = bigint | boolean | Value[];

export type AstNode =
  | { kind: 'int'; value: number | string | bigint }
  | { kind: 'bool'; value: boolean }
  | { kind: 'var'; name: string }
  | { kind: 'neg'; arg: AstNode }
  | { kind: 'arith'; op: 'add' | 'sub' | 'mul' | 'div' | 'mod'; args: [AstNode, AstNode] }
  | { kind: 'cmp'; op: 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge'; args: [AstNode, AstNode] }
  | { kind: 'logic'; op: 'and' | 'or'; args: [AstNode, AstNode] }
  | { kind: 'not'; arg: AstNode }
  | { kind: 'if'; cond: AstNode; then: AstNode; else: AstNode }
  | { kind: 'map'; array: AstNode; param: string; body: AstNode }
  | { kind: 'filter'; array: AstNode; param: string; body: AstNode }
  | { kind: 'fold'; array: AstNode; param: string; acc: string; init: AstNode; body: AstNode };

export type ErrorType =
  | 'overflow'
  | 'type'
  | 'unbound-var'
  | 'domain'
  | 'budget-steps'
  | 'budget-space'
  | 'tenant-space'
  | 'cancelled';

interface Path {
  seg: string;
  parent: Path | null;
}

const ROOT: Path = { seg: '$', parent: null };

function child(parent: Path, seg: string): Path {
  return { seg, parent };
}

function formatPath(path: Path | null): string {
  const parts: string[] = [];
  let cur: Path | null = path;
  while (cur) {
    parts.push(cur.seg);
    cur = cur.parent;
  }
  parts.reverse();
  let out = '';
  for (const seg of parts) {
    out += seg.startsWith('[') || out === '' ? seg : `.${seg}`;
  }
  return out;
}

export class RuleError extends Error {
  readonly type: ErrorType;
  readonly path: string;
  constructor(type: ErrorType, path: Path | null, message: string) {
    super(message);
    this.name = 'RuleError';
    this.type = type;
    this.path = formatPath(path);
  }
}

export interface Budget {
  steps: number;
  space: number;
}

export interface EvalRequest {
  tenantId: string;
  ast: AstNode;
  vars?: Record<string, unknown>;
  budget: Budget;
}

export type EvalResult =
  | { ok: true; value: Value; steps: number; space: number }
  | { ok: false; error: { type: ErrorType; path: string; message: string }; steps: number; space: number };

export interface EvalHandle {
  promise: Promise<EvalResult>;
  cancel(): void;
  usage(): { steps: number; space: number };
}

const I64_MIN = -(1n << 63n);
const I64_MAX = (1n << 63n) - 1n;

function checked(value: bigint, path: Path): bigint {
  if (value < I64_MIN || value > I64_MAX) {
    throw new RuleError('overflow', path, `int64 overflow: ${value} is outside [${I64_MIN}, ${I64_MAX}]`);
  }
  return value;
}

function typeName(value: Value): string {
  if (typeof value === 'bigint') return 'int';
  if (typeof value === 'boolean') return 'bool';
  return 'array';
}

function requireInt(value: Value, path: Path): bigint {
  if (typeof value !== 'bigint') {
    throw new RuleError('type', path, `expected int, got ${typeName(value)}`);
  }
  return value;
}

function requireBool(value: Value, path: Path): boolean {
  if (typeof value !== 'boolean') {
    throw new RuleError('type', path, `expected bool, got ${typeName(value)}`);
  }
  return value;
}

function requireArray(value: Value, path: Path): Value[] {
  if (!Array.isArray(value)) {
    throw new RuleError('type', path, `expected array, got ${typeName(value)}`);
  }
  return value;
}

function parseIntLiteral(value: number | string | bigint, path: Path): bigint {
  let parsed: bigint;
  if (typeof value === 'bigint') {
    parsed = value;
  } else if (typeof value === 'number') {
    if (!Number.isInteger(value)) {
      throw new RuleError('type', path, `integer literal expected, got ${value}`);
    }
    parsed = BigInt(value);
  } else {
    try {
      parsed = BigInt(value);
    } catch {
      throw new RuleError('type', path, `invalid integer literal "${value}"`);
    }
  }
  return checked(parsed, path);
}

function applyArith(op: 'add' | 'sub' | 'mul' | 'div' | 'mod', left: bigint, right: bigint, path: Path): bigint {
  switch (op) {
    case 'add':
      return checked(left + right, path);
    case 'sub':
      return checked(left - right, path);
    case 'mul':
      return checked(left * right, path);
    case 'div':
      if (right === 0n) throw new RuleError('domain', path, 'integer division by zero');
      return checked(left / right, path);
    case 'mod':
      if (right === 0n) throw new RuleError('domain', path, 'integer modulo by zero');
      return checked(left % right, path);
  }
}

function applyCmp(op: 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge', left: Value, right: Value, path: Path): boolean {
  if (op === 'eq' || op === 'ne') {
    if (Array.isArray(left) || Array.isArray(right)) {
      throw new RuleError('type', path, 'arrays cannot be compared');
    }
    if (typeof left !== typeof right) {
      throw new RuleError('type', path, `cannot compare ${typeName(left)} with ${typeName(right)}`);
    }
    return op === 'eq' ? left === right : left !== right;
  }
  const l = requireInt(left, path);
  const r = requireInt(right, path);
  switch (op) {
    case 'lt':
      return l < r;
    case 'le':
      return l <= r;
    case 'gt':
      return l > r;
    case 'ge':
      return l >= r;
  }
}

interface Env {
  name: string;
  value: Value;
  parent: Env | null;
}

function lookup(env: Env | null, name: string): Value | undefined {
  let cur = env;
  while (cur) {
    if (cur.name === name) return cur.value;
    cur = cur.parent;
  }
  return undefined;
}

function normalizeVar(value: unknown, path: Path): Value {
  if (typeof value === 'boolean') return value;
  if (typeof value === 'bigint') return checked(value, path);
  if (typeof value === 'number') {
    if (!Number.isInteger(value)) {
      throw new RuleError('type', path, `variable value ${value} is not an integer`);
    }
    return checked(BigInt(value), path);
  }
  if (typeof value === 'string') return parseIntLiteral(value, path);
  if (Array.isArray(value)) {
    return value.map((item, i) => normalizeVar(item, child(path, `[${i}]`)));
  }
  throw new RuleError('type', path, `unsupported variable value of type ${typeof value}`);
}

class EvalContext {
  steps = 0;
  space = 0;
  cancelled = false;
  currentPath: Path = ROOT;
  readonly budget: Budget;
  readonly service: RuleService;
  readonly tenantId: string;
  constructor(
    budget: Budget,
    service: RuleService,
    tenantId: string,
  ) {
    this.budget = budget;
    this.service = service;
    this.tenantId = tenantId;
  }
  chargeStep(path: Path): void {
    if (this.cancelled) throw new RuleError('cancelled', path, 'request cancelled');
    if (this.steps + 1 > this.budget.steps) {
      throw new RuleError('budget-steps', path, `step budget ${this.budget.steps} exhausted`);
    }
    this.steps += 1;
  }
  chargeSpace(n: number, path: Path): void {
    if (n <= 0) return;
    if (this.cancelled) throw new RuleError('cancelled', path, 'request cancelled');
    if (this.space + n > this.budget.space) {
      throw new RuleError('budget-space', path, `space budget ${this.budget.space} exhausted (requested ${n} more)`);
    }
    this.service.reserveTenantSpace(this.tenantId, n, path);
    this.space += n;
  }
}

interface ChildReq {
  node: AstNode;
  env: Env | null;
  path: Path;
}

type EvalGen = Generator<ChildReq, Value, Value>;

function* evalNode(node: AstNode, env: Env | null, path: Path, ctx: EvalContext): EvalGen {
  switch (node.kind) {
    case 'int':
      return parseIntLiteral(node.value, path);
    case 'bool':
      return node.value;
    case 'var': {
      const hit = lookup(env, node.name);
      if (hit === undefined) {
        throw new RuleError('unbound-var', path, `variable "${node.name}" is not defined`);
      }
      return hit;
    }
    case 'neg': {
      const operand = requireInt(yield { node: node.arg, env, path: child(path, 'arg') }, path);
      return checked(-operand, path);
    }
    case 'not': {
      const operand = requireBool(yield { node: node.arg, env, path: child(path, 'arg') }, path);
      return !operand;
    }
    case 'arith': {
      const left = requireInt(yield { node: node.args[0], env, path: child(path, 'args[0]') }, path);
      const right = requireInt(yield { node: node.args[1], env, path: child(path, 'args[1]') }, path);
      return applyArith(node.op, left, right, path);
    }
    case 'cmp': {
      const left = yield { node: node.args[0], env, path: child(path, 'args[0]') };
      const right = yield { node: node.args[1], env, path: child(path, 'args[1]') };
      return applyCmp(node.op, left, right, path);
    }
    case 'logic': {
      const left = requireBool(yield { node: node.args[0], env, path: child(path, 'args[0]') }, path);
      if (node.op === 'and' && !left) return false;
      if (node.op === 'or' && left) return true;
      return requireBool(yield { node: node.args[1], env, path: child(path, 'args[1]') }, path);
    }
    case 'if': {
      const cond = requireBool(yield { node: node.cond, env, path: child(path, 'cond') }, path);
      return yield { node: cond ? node.then : node.else, env, path: child(path, cond ? 'then' : 'else') };
    }
    case 'map': {
      const arr = requireArray(yield { node: node.array, env, path: child(path, 'array') }, path);
      ctx.chargeSpace(arr.length, path);
      const out: Value[] = new Array(arr.length);
      for (let i = 0; i < arr.length; i++) {
        ctx.chargeStep(child(path, `array[${i}]`));
        const elemEnv: Env = { name: node.param, value: arr[i], parent: env };
        out[i] = yield { node: node.body, env: elemEnv, path: child(path, `body[${i}]`) };
      }
      return out;
    }
    case 'filter': {
      const arr = requireArray(yield { node: node.array, env, path: child(path, 'array') }, path);
      const out: Value[] = [];
      for (let i = 0; i < arr.length; i++) {
        ctx.chargeStep(child(path, `array[${i}]`));
        const elemEnv: Env = { name: node.param, value: arr[i], parent: env };
        const keep = requireBool(
          yield { node: node.body, env: elemEnv, path: child(path, `body[${i}]`) },
          child(path, 'body'),
        );
        if (keep) {
          ctx.chargeSpace(1, path);
          out.push(arr[i]);
        }
      }
      return out;
    }
    case 'fold': {
      const arr = requireArray(yield { node: node.array, env, path: child(path, 'array') }, path);
      let acc: Value = yield { node: node.init, env, path: child(path, 'init') };
      for (let i = 0; i < arr.length; i++) {
        ctx.chargeStep(child(path, `array[${i}]`));
        const accEnv: Env = { name: node.acc, value: acc, parent: env };
        const elemEnv: Env = { name: node.param, value: arr[i], parent: accEnv };
        acc = yield { node: node.body, env: elemEnv, path: child(path, `body[${i}]`) };
      }
      return acc;
    }
  }
}

export interface RuleServiceOptions {
  tenantSpaceLimit: number;
  chunkSize?: number;
}

export class RuleService {
  private readonly tenantInFlight = new Map<string, number>();
  private readonly tenantSpaceLimit: number;
  private readonly chunkSize: number;

  constructor(options: RuleServiceOptions) {
    this.tenantSpaceLimit = options.tenantSpaceLimit;
    this.chunkSize = options.chunkSize ?? 4096;
  }

  inFlight(tenantId: string): number {
    return this.tenantInFlight.get(tenantId) ?? 0;
  }

  reserveTenantSpace(tenantId: string, n: number, path: Path): void {
    const used = this.inFlight(tenantId);
    if (used + n > this.tenantSpaceLimit) {
      throw new RuleError(
        'tenant-space',
        path,
        `tenant "${tenantId}" in-flight space limit ${this.tenantSpaceLimit} exceeded (${used} in use, ${n} requested)`,
      );
    }
    this.tenantInFlight.set(tenantId, used + n);
  }

  private releaseTenantSpace(tenantId: string, n: number): void {
    if (n <= 0) return;
    const next = this.inFlight(tenantId) - n;
    if (next <= 0) this.tenantInFlight.delete(tenantId);
    else this.tenantInFlight.set(tenantId, next);
  }

  submit(req: EvalRequest): EvalHandle {
    const ctx = new EvalContext(req.budget, this, req.tenantId);
    const promise = (async (): Promise<EvalResult> => {
      try {
        const value = await this.run(req, ctx);
        return { ok: true, value, steps: ctx.steps, space: ctx.space };
      } catch (err) {
        if (err instanceof RuleError) {
          return {
            ok: false,
            error: { type: err.type, path: err.path, message: err.message },
            steps: ctx.steps,
            space: ctx.space,
          };
        }
        throw err;
      } finally {
        this.releaseTenantSpace(req.tenantId, ctx.space);
      }
    })();
    return {
      promise,
      cancel: () => {
        ctx.cancelled = true;
      },
      usage: () => ({ steps: ctx.steps, space: ctx.space }),
    };
  }

  private async run(req: EvalRequest, ctx: EvalContext): Promise<Value> {
    let env: Env | null = null;
    for (const [name, raw] of Object.entries(req.vars ?? {})) {
      env = { name, value: normalizeVar(raw, child(ROOT, name)), parent: env };
    }
    ctx.chargeStep(ROOT);
    const stack: EvalGen[] = [evalNode(req.ast, env, ROOT, ctx)];
    let input: Value = false;
    let ops = 0;
    while (stack.length > 0) {
      if (ctx.cancelled) throw new RuleError('cancelled', ctx.currentPath, 'request cancelled');
      const top = stack[stack.length - 1];
      const result = top.next(input);
      if (result.done === true) {
        stack.pop();
        input = result.value;
      } else {
        const next = result.value;
        ctx.chargeStep(next.path);
        ctx.currentPath = next.path;
        stack.push(evalNode(next.node, next.env, next.path, ctx));
      }
      ops += 1;
      if (ops >= this.chunkSize) {
        ops = 0;
        await new Promise<void>((resolve) => setImmediate(resolve));
      }
    }
    return input;
  }
}
