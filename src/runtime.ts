// Function registry and evaluation context.
//
// Three kinds of functions:
//  - pure + total   : no side effects, never throw (add, mul, ...)
//  - pure + partial : no side effects, may throw on some inputs (div, mod, sqrt)
//  - impure         : counting side effects, recorded in ctx.counters (tick, bump)

export class EvalError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'EvalError';
  }
}

export interface TraceEntry {
  fn: string;
  args: number[];
  impure: boolean;
}

export interface EvalContext {
  trace: TraceEntry[];
  counters: Map<string, number>;
  memo: Map<number, number>;
}

export function newContext(): EvalContext {
  return { trace: [], counters: new Map(), memo: new Map() };
}

export interface FnDef {
  arity: number;
  pure: boolean;
  partial: boolean;
  impl: (args: number[], ctx: EvalContext) => number;
}

function bumpCounter(ctx: EvalContext, name: string): number {
  const next = (ctx.counters.get(name) ?? 0) + 1;
  ctx.counters.set(name, next);
  return next;
}

function pureTotal(arity: number, impl: (args: number[]) => number): FnDef {
  return { arity, pure: true, partial: false, impl };
}

function purePartial(arity: number, impl: (args: number[]) => number): FnDef {
  return { arity, pure: true, partial: true, impl };
}

export const FUNCTIONS: Record<string, FnDef> = {
  add: pureTotal(2, ([a, b]) => a + b),
  sub: pureTotal(2, ([a, b]) => a - b),
  mul: pureTotal(2, ([a, b]) => a * b),
  neg: pureTotal(1, ([a]) => -a),
  abs: pureTotal(1, ([a]) => Math.abs(a)),
  min: pureTotal(2, ([a, b]) => Math.min(a, b)),
  max: pureTotal(2, ([a, b]) => Math.max(a, b)),

  div: purePartial(2, ([a, b]) => {
    if (b === 0) throw new EvalError(`div: division by zero (${a}, ${b})`);
    return a / b;
  }),
  mod: purePartial(2, ([a, b]) => {
    if (b === 0) throw new EvalError(`mod: division by zero (${a}, ${b})`);
    return a % b;
  }),
  sqrt: purePartial(1, ([a]) => {
    if (a < 0) throw new EvalError(`sqrt: negative input (${a})`);
    return Math.sqrt(a);
  }),

  tick: {
    arity: 1,
    pure: false,
    partial: false,
    impl: ([a], ctx) => {
      bumpCounter(ctx, 'tick');
      return a;
    },
  },
  bump: {
    arity: 0,
    pure: false,
    partial: false,
    impl: (_args, ctx) => bumpCounter(ctx, 'bump'),
  },
};
