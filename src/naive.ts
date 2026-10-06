/**
 * Reference interpreter: plain recursive evaluation, no tail-call handling.
 * Every call (tail or not) pushes a real frame, so the error report always
 * contains the complete call stack. Used to cross-check the optimized
 * interpreter on small programs.
 */

import {
  Env, ErrorReport, Expr, FrameInfo, FunDef, InterpError, Program, Value,
  applyPrim, formatFrame, indexFunctions,
} from "./lang";

export interface NaiveStats {
  calls: number;
  maxDepth: number;
}

export class NaiveInterpreter {
  readonly trace: string[] = [];
  readonly stats: NaiveStats = { calls: 0, maxDepth: 0 };
  private readonly functions: Map<string, FunDef>;
  private readonly callStack: FrameInfo[] = [];

  constructor(
    private readonly program: Program,
    private readonly traceEnabled = true
  ) {
    this.functions = indexFunctions(program);
  }

  run(): Value {
    const mainFn: FunDef = { name: "<main>", params: [], body: this.program.main };
    return this.apply(mainFn, []);
  }

  private eval(expr: Expr, env: Env): Value {
    switch (expr.tag) {
      case "num":
      case "bool":
        return expr.value;
      case "var":
        return env.lookup(expr.name);
      case "prim":
        return applyPrim(expr.op, expr.args.map((a) => this.eval(a, env)));
      case "if":
        return this.eval(this.truthy(expr.cond, env) ? expr.then : expr.else, env);
      case "let": {
        const child = new Env(env);
        child.define(expr.name, this.eval(expr.value, env));
        return this.eval(expr.body, child);
      }
      case "begin": {
        if (expr.exprs.length === 0) throw new InterpError("empty begin");
        let last: Value = false;
        for (const e of expr.exprs) last = this.eval(e, env);
        return last;
      }
      case "call": {
        const fn = this.functions.get(expr.name);
        if (!fn) throw new InterpError(`undefined function: ${expr.name}`);
        const args = expr.args.map((a) => this.eval(a, env));
        return this.apply(fn, args);
      }
      case "raise":
        throw new InterpError(expr.message);
    }
  }

  private truthy(cond: Expr, env: Env): boolean {
    const v = this.eval(cond, env);
    if (typeof v !== "boolean") throw new InterpError("if condition must be boolean");
    return v;
  }

  private apply(fn: FunDef, args: Value[]): Value {
    if (args.length !== fn.params.length) {
      throw new InterpError(
        `${fn.name}: expected ${fn.params.length} args, got ${args.length}`
      );
    }
    const frame: FrameInfo = { funName: fn.name, args };
    this.callStack.push(frame);
    this.stats.calls++;
    this.stats.maxDepth = Math.max(this.stats.maxDepth, this.callStack.length);
    if (this.traceEnabled) this.trace.push(`call ${formatFrame(frame)}`);
    try {
      const env = new Env();
      fn.params.forEach((p, i) => env.define(p, args[i]));
      return this.eval(fn.body, env);
    } catch (e) {
      if (e instanceof InterpError && !e.snapshot) {
        e.snapshot = this.capture(e.message);
      }
      throw e;
    } finally {
      this.callStack.pop();
    }
  }

  /** The naive interpreter keeps every frame, so the full stack is the report. */
  private capture(message: string): ErrorReport {
    return {
      message,
      nonTailStack: this.callStack.map((f) => ({ ...f, args: [...f.args] })),
      recentTailCalls: [],
      tailElided: 0,
      tailTotal: 0,
    };
  }
}
