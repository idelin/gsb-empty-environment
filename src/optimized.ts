/**
 * Tail-call-optimized interpreter.
 *
 * A call in tail position does NOT push a new runtime frame: it produces a
 * TailCall signal and the trampoline loop reuses the current activation
 * (`continue` with the new function/arguments). Only non-tail calls allocate
 * a frame on `nonTailStack`, so pure tail recursion runs in O(1) frames.
 *
 * Error reporting keeps:
 *   - the COMPLETE non-tail stack (`nonTailStack`), and
 *   - a BOUNDED summary of the most recent tail calls (`tailBuffer`,
 *     capacity = `maxTailSummary`, oldest entries dropped past the bound).
 * Memory for the summary never grows with recursion depth; `tailElided`
 * counts how many older tail calls were dropped.
 */

import {
  Env, ErrorReport, Expr, FrameInfo, FunDef, InterpError, Program, Value,
  applyPrim, formatFrame, indexFunctions,
} from "./lang";

interface TailCall {
  kind: "tailcall";
  fn: FunDef;
  args: Value[];
}

function isTailCall(x: Value | TailCall): x is TailCall {
  return typeof x === "object" && x !== null && (x as TailCall).kind === "tailcall";
}

export interface TailStats {
  /** Tail calls that reused the current frame instead of allocating one. */
  tailCalls: number;
  /** Non-tail calls (each allocates one frame). */
  nonTailCalls: number;
  /** Total frames ever allocated; stays O(1) for pure tail recursion. */
  framesAllocated: number;
  /** Deepest the non-tail stack ever got. */
  maxNonTailDepth: number;
}

export class TailCallInterpreter {
  readonly trace: string[] = [];
  readonly stats: TailStats = {
    tailCalls: 0,
    nonTailCalls: 0,
    framesAllocated: 0,
    maxNonTailDepth: 0,
  };
  private readonly functions: Map<string, FunDef>;
  private readonly nonTailStack: FrameInfo[] = [];
  private readonly tailBuffer: FrameInfo[] = [];
  private tailElided = 0;

  constructor(
    private readonly program: Program,
    /** Bound on the recent-tail-call summary kept for error reports. */
    private readonly maxTailSummary: number,
    private readonly traceEnabled = true
  ) {
    if (maxTailSummary < 0) throw new Error("maxTailSummary must be >= 0");
    this.functions = indexFunctions(program);
  }

  run(): Value {
    const mainFn: FunDef = { name: "<main>", params: [], body: this.program.main };
    return this.applyNonTail(mainFn, []);
  }

  /**
   * Evaluate `expr`. When `inTail` is true a call evaluates to a TailCall
   * signal instead of recursing; the owning trampoline reuses its frame.
   */
  private eval(expr: Expr, env: Env, inTail: boolean): Value | TailCall {
    switch (expr.tag) {
      case "num":
      case "bool":
        return expr.value;
      case "var":
        return env.lookup(expr.name);
      case "prim":
        return applyPrim(expr.op, expr.args.map((a) => this.evalValue(a, env)));
      case "if": {
        const c = this.evalValue(expr.cond, env);
        if (typeof c !== "boolean") throw new InterpError("if condition must be boolean");
        return this.eval(c ? expr.then : expr.else, env, inTail);
      }
      case "let": {
        const child = new Env(env);
        child.define(expr.name, this.evalValue(expr.value, env));
        return this.eval(expr.body, child, inTail);
      }
      case "begin": {
        if (expr.exprs.length === 0) throw new InterpError("empty begin");
        for (let i = 0; i < expr.exprs.length - 1; i++) {
          this.evalValue(expr.exprs[i], env);
        }
        return this.eval(expr.exprs[expr.exprs.length - 1], env, inTail);
      }
      case "call": {
        const fn = this.functions.get(expr.name);
        if (!fn) throw new InterpError(`undefined function: ${expr.name}`);
        const args = expr.args.map((a) => this.evalValue(a, env));
        if (inTail) {
          return { kind: "tailcall", fn, args };
        }
        return this.applyNonTail(fn, args);
      }
      case "raise":
        throw new InterpError(expr.message);
    }
  }

  /** Evaluate where a plain value is required (non-tail position). */
  private evalValue(expr: Expr, env: Env): Value {
    const r = this.eval(expr, env, false);
    return r as Value;
  }

  /** Non-tail call: allocates a real frame and runs a fresh trampoline. */
  private applyNonTail(fn: FunDef, args: Value[]): Value {
    this.checkArity(fn, args);
    const frame: FrameInfo = { funName: fn.name, args };
    this.nonTailStack.push(frame);
    this.stats.nonTailCalls++;
    this.stats.framesAllocated++;
    this.stats.maxNonTailDepth = Math.max(
      this.stats.maxNonTailDepth,
      this.nonTailStack.length
    );
    if (this.traceEnabled) this.trace.push(`call ${formatFrame(frame)}`);
    try {
      return this.trampoline(fn, args);
    } catch (e) {
      if (e instanceof InterpError && !e.snapshot) {
        e.snapshot = this.capture(e.message);
      }
      throw e;
    } finally {
      this.nonTailStack.pop();
    }
  }

  /**
   * Run one activation. A tail call reuses THIS activation: no frame is
   * allocated, only the bounded summary buffer is updated.
   */
  private trampoline(fn: FunDef, args: Value[]): Value {
    let curFn = fn;
    let curArgs = args;
    for (;;) {
      const env = new Env();
      curFn.params.forEach((p, i) => env.define(p, curArgs[i]));
      const r = this.eval(curFn.body, env, true);
      if (!isTailCall(r)) return r;
      this.checkArity(r.fn, r.args);
      this.stats.tailCalls++;
      this.recordTail({ funName: r.fn.name, args: r.args });
      curFn = r.fn;
      curArgs = r.args;
    }
  }

  /** Bounded ring: keeps only the last `maxTailSummary` tail calls. */
  private recordTail(frame: FrameInfo): void {
    if (this.traceEnabled) this.trace.push(`call ${formatFrame(frame)}`);
    if (this.maxTailSummary === 0) {
      this.tailElided++;
      return;
    }
    if (this.tailBuffer.length >= this.maxTailSummary) {
      this.tailBuffer.shift();
      this.tailElided++;
    }
    this.tailBuffer.push(frame);
  }

  private checkArity(fn: FunDef, args: Value[]): void {
    if (args.length !== fn.params.length) {
      throw new InterpError(
        `${fn.name}: expected ${fn.params.length} args, got ${args.length}`
      );
    }
  }

  /**
   * Snapshot for error reports: complete non-tail stack + bounded tail
   * summary. Called at the innermost frame while the stack is still intact.
   */
  private capture(message: string): ErrorReport {
    return {
      message,
      nonTailStack: this.nonTailStack.map((f) => ({ ...f, args: [...f.args] })),
      recentTailCalls: this.tailBuffer.map((f) => ({ ...f, args: [...f.args] })),
      tailElided: this.tailElided,
      tailTotal: this.stats.tailCalls,
    };
  }
}
