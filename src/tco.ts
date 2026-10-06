// Tail-call-optimizing interpreter.
//
// Key design points:
//  * Function application uses an explicit trampoline loop. A call in tail
//    position produces a TailCall signal instead of recursing, and the
//    current Frame object is REUSED in place (reset with the new callee).
//    Only genuinely non-tail calls push a new Frame onto the stack.
//  * Error reporting keeps the FULL non-tail stack (ctx.stack) plus a
//    bounded ring buffer of the most recent tail-call summaries. The ring
//    capacity is fixed by the `maxTailSummary` option and never grows with
//    recursion depth.
//  * Space accounting (SpaceStats) proves the bound: framesAllocated and
//    maxStackDepth stay O(non-tail depth), tailSummaryMax <= maxTailSummary.

import { Expr, FunDef, Program } from './lang';
import {
  Env,
  EventLog,
  FrameInfo,
  TailRecord,
  TraceSource,
  Value,
  applyPrim,
  raiseError,
  showValue,
} from './runtime';

export interface TcoOptions {
  /** Maximum number of recent tail-call summaries retained for errors. */
  maxTailSummary: number;
  /** Optional event sink used to cross-check call order with the naive interpreter. */
  log?: EventLog;
}

export interface SpaceStats {
  framesAllocated: number;
  maxStackDepth: number;
  tailCalls: number;
  tailSummaryMax: number;
}

export interface TcoResult {
  value: Value;
  stats: SpaceStats;
}

class TailCall {
  constructor(readonly fn: FunDef, readonly args: Value[]) {}
}

/** A reusable runtime frame. Tail calls mutate it in place via reset(). */
class Frame {
  fn: FunDef;
  args: Value[];
  env: Env;
  /** Logical call chain for event logging; null when logging is disabled so
   *  that frame memory stays O(1) regardless of tail-recursion length. */
  private logicalNames: string[] | null;

  constructor(fn: FunDef, args: Value[], env: Env, logging: boolean) {
    this.fn = fn;
    this.args = args;
    this.env = env;
    this.logicalNames = logging ? [fn.name] : null;
  }

  reset(fn: FunDef, args: Value[], env: Env): void {
    this.fn = fn;
    this.args = args;
    this.env = env;
    this.logicalNames?.push(fn.name);
  }

  drainLogicalNames(): string[] {
    return this.logicalNames ?? [];
  }
}

/** Fixed-capacity ring buffer of tail-call summaries. */
class TailRing {
  private readonly buf: (TailRecord | undefined)[];
  private start = 0;
  private size = 0;

  constructor(private readonly cap: number) {
    this.buf = new Array<TailRecord | undefined>(cap);
  }

  get length(): number {
    return this.size;
  }

  push(rec: TailRecord): void {
    if (this.cap === 0) return;
    if (this.size < this.cap) {
      this.buf[(this.start + this.size) % this.cap] = rec;
      this.size++;
    } else {
      this.buf[this.start] = rec;
      this.start = (this.start + 1) % this.cap;
    }
  }

  entries(): TailRecord[] {
    const out: TailRecord[] = [];
    for (let i = 0; i < this.size; i++) {
      out.push(this.buf[(this.start + i) % this.cap]!);
    }
    return out;
  }
}

class Ctx implements TraceSource {
  readonly stack: Frame[] = [];
  readonly ring: TailRing;
  readonly stats: SpaceStats = {
    framesAllocated: 0,
    maxStackDepth: 0,
    tailCalls: 0,
    tailSummaryMax: 0,
  };
  tailOrdinal = 0;

  constructor(
    readonly defs: Map<string, FunDef>,
    readonly opts: TcoOptions,
    readonly log?: EventLog,
  ) {
    this.ring = new TailRing(Math.max(0, opts.maxTailSummary));
  }

  snapshotStack(): FrameInfo[] {
    return this.stack.map((f) => ({ fnName: f.fn.name, args: [...f.args] }));
  }
  snapshotTail(): TailRecord[] {
    return this.ring.entries();
  }
  tailCallCount(): number {
    return this.tailOrdinal;
  }
}

function bindParams(fn: FunDef, args: Value[]): Env {
  const env = new Env(null);
  for (let i = 0; i < fn.params.length; i++) env.define(fn.params[i]!, args[i]!);
  return env;
}

function apply(fn: FunDef, args: Value[], ctx: Ctx): Value {
  const frame = new Frame(fn, args, bindParams(fn, args), ctx.log !== undefined);
  ctx.stats.framesAllocated++;
  ctx.stack.push(frame);
  if (ctx.stack.length > ctx.stats.maxStackDepth) ctx.stats.maxStackDepth = ctx.stack.length;
  ctx.log?.enter(fn.name, args);
  try {
    for (;;) {
      const result = evalExpr(frame.fn.body, frame.env, ctx, true);
      if (result instanceof TailCall) {
        // Tail call: reuse the current frame instead of pushing a new one.
        ctx.tailOrdinal++;
        ctx.stats.tailCalls++;
        ctx.ring.push({ ordinal: ctx.tailOrdinal, fnName: result.fn.name, args: [...result.args] });
        if (ctx.ring.length > ctx.stats.tailSummaryMax) {
          ctx.stats.tailSummaryMax = ctx.ring.length;
        }
        ctx.log?.enter(result.fn.name, result.args);
        frame.reset(result.fn, result.args, bindParams(result.fn, result.args));
        continue;
      }
      // Logical exits, innermost first, to mirror the naive interpreter.
      const names = frame.drainLogicalNames();
      for (let i = names.length - 1; i >= 0; i--) ctx.log?.exit(names[i]!, result);
      return result;
    }
  } finally {
    ctx.stack.pop();
  }
}

function evalNonTail(e: Expr, env: Env, ctx: Ctx): Value {
  const r = evalExpr(e, env, ctx, false);
  if (r instanceof TailCall) throw new Error('internal: unexpected tail call');
  return r;
}

function evalExpr(e: Expr, env: Env, ctx: Ctx, tail: boolean): Value | TailCall {
  switch (e.kind) {
    case 'num':
    case 'bool':
    case 'str':
      return e.value;
    case 'var': {
      const v = env.lookup(e.name);
      if (v === undefined) raiseError(`unbound variable '${e.name}'`, ctx);
      return v;
    }
    case 'if': {
      const cond = evalNonTail(e.cond, env, ctx);
      if (typeof cond !== 'boolean') {
        raiseError(`if condition must be boolean, got ${showValue(cond)}`, ctx);
      }
      return evalExpr(cond ? e.then : e.else, env, ctx, tail);
    }
    case 'begin': {
      for (let i = 0; i < e.exprs.length - 1; i++) evalNonTail(e.exprs[i]!, env, ctx);
      return evalExpr(e.exprs[e.exprs.length - 1]!, env, ctx, tail);
    }
    case 'let': {
      const v = evalNonTail(e.value, env, ctx);
      const inner = new Env(env);
      inner.define(e.name, v);
      return evalExpr(e.body, inner, ctx, tail);
    }
    case 'error': {
      const msg = evalNonTail(e.message, env, ctx);
      raiseError(typeof msg === 'string' ? msg : showValue(msg), ctx);
    }
    case 'prim': {
      if (e.op === 'and' || e.op === 'or') {
        for (const a of e.args) {
          const v = evalNonTail(a, env, ctx);
          if (typeof v !== 'boolean') {
            raiseError(`'${e.op}' expects boolean operands, got ${showValue(v)}`, ctx);
          }
          if (e.op === 'and' && !v) return false;
          if (e.op === 'or' && v) return true;
        }
        return e.op === 'and';
      }
      const args = e.args.map((a) => evalNonTail(a, env, ctx));
      return applyPrim(e.op, args, ctx);
    }
    case 'call': {
      const fn = ctx.defs.get(e.name);
      if (fn === undefined) raiseError(`unknown function '${e.name}'`, ctx);
      if (fn.params.length !== e.args.length) {
        raiseError(
          `arity mismatch: '${e.name}' expects ${fn.params.length} argument(s), got ${e.args.length}`,
          ctx,
        );
      }
      const args = e.args.map((a) => evalNonTail(a, env, ctx));
      if (tail) return new TailCall(fn, args);
      return apply(fn, args, ctx);
    }
  }
}

export function runTco(program: Program, opts: TcoOptions): TcoResult {
  const defs = new Map<string, FunDef>();
  for (const d of program.defs) defs.set(d.name, d);
  const ctx = new Ctx(defs, opts, opts.log);
  const globalEnv = new Env(null);
  let value: Value = false;
  for (const e of program.main) value = evalNonTail(e, globalEnv, ctx);
  return { value, stats: ctx.stats };
}
