/**
 * Rational expression DAG with delayed reduction and a bit-length budget.
 *
 * Leaves hold raw (numerator, denominator) pairs. Internal nodes combine the
 * raw products of their operands and deliberately do NOT cancel factors while
 * the graph is being built or traversed: reduction is delayed until the value
 * is requested, so a comparison (which uses cross multiplication) can decide
 * the order of two large expressions without ever expanding giant decimal
 * strings or fully normalizing the terms.
 *
 * Every evaluation is run under an explicit bit budget; nodes exceeding it are
 * reported as "incomplete" rather than silently producing an enormous value.
 */

import { cmp as cmpFrac, frac, bitLength, type Fraction } from "./fraction.ts";

export type Op = "leaf" | "add" | "sub" | "mul" | "div" | "neg";

export interface RawPair {
  readonly n: bigint;
  readonly d: bigint;
}

type Descriptor =
  | { op: "leaf"; value: RawPair }
  | { op: "neg"; a: number }
  | { op: Exclude<Op, "leaf" | "neg">; a: number; b: number };

export type EvalStatus = "ok" | "incomplete";

export interface EvalResult {
  /** canonical reduced value; null whenever status === "incomplete" */
  readonly value: Fraction | null;
  readonly status: EvalStatus;
  /** ids of nodes whose intermediate bit length exceeded the budget */
  readonly overBudget: readonly number[];
  /** ids of nodes dividing by zero */
  readonly divisionByZero: readonly number[];
  /** peak bit length of any intermediate numerator/denominator */
  readonly peakBits: number;
}

export interface CompareResult {
  readonly result: -1 | 0 | 1 | null;
  readonly status: EvalStatus;
  readonly overBudget: readonly number[];
  readonly divisionByZero: readonly number[];
  readonly peakBits: number;
}

/** Normalize a raw leaf pair's sign (denominator positive; zero -> 0/1), without reducing. */
function signNormalize(n: bigint, d: bigint): RawPair {
  if (d === 0n) throw new RangeError("Zero denominator in leaf");
  if (n === 0n) return { n: 0n, d: 1n };
  return d < 0n ? { n: -n, d: -d } : { n, d };
}

export class RationalDAG {
  private readonly nodes: Descriptor[] = [];
  private readonly cache = new Map<number, { value: RawPair | null; peakBits: number }>();

  leaf(n: bigint | number, d: bigint | number = 1n): number {
    const nn = typeof n === "bigint" ? n : BigInt(n);
    const dd = typeof d === "bigint" ? d : BigInt(d);
    this.nodes.push({ op: "leaf", value: signNormalize(nn, dd) });
    return this.nodes.length - 1;
  }

  frac(value: Fraction): number {
    // Leaves store raw pairs; a canonical Fraction is simply stored as-is
    // (already reduced and sign-normalized).
    this.nodes.push({ op: "leaf", value: { n: value.n, d: value.d } });
    return this.nodes.length - 1;
  }

  private bin(op: Exclude<Op, "leaf" | "neg">, a: number, b: number): number {
    this.checkId(a);
    this.checkId(b);
    this.nodes.push({ op, a, b });
    return this.nodes.length - 1;
  }

  add(a: number, b: number): number {
    return this.bin("add", a, b);
  }
  sub(a: number, b: number): number {
    return this.bin("sub", a, b);
  }
  mul(a: number, b: number): number {
    return this.bin("mul", a, b);
  }
  div(a: number, b: number): number {
    return this.bin("div", a, b);
  }
  neg(a: number): number {
    this.checkId(a);
    this.nodes.push({ op: "neg", a });
    return this.nodes.length - 1;
  }

  get size(): number {
    return this.nodes.length;
  }

  private checkId(id: number): void {
    if (!Number.isInteger(id) || id < 0 || id >= this.nodes.length) {
      throw new RangeError(`Unknown node id: ${id}`);
    }
  }

  /**
   * Evaluate one node to its RAW (unreduced) pair under a bit budget,
   * memoized per node so shared subexpressions are computed once.
   *
   * Returns null for the value if this node or any dependency is invalid
   * (over budget or division by zero). Budget violations do not throw: they
   * are accumulated and reported.
   */
  private evalRaw(root: number, budgetBits: number, ctx: { over: Set<number>; divZero: Set<number>; peak: number }): RawPair | null {
    const cached = this.cache.get(root);
    if (cached) return cached.value;

    const node = this.nodes[root];
    let out: RawPair | null = null;

    switch (node.op) {
      case "leaf":
        out = node.value;
        break;
      case "neg": {
        const a = this.evalRaw(node.a, budgetBits, ctx);
        out = a === null ? null : { n: -a.n, d: a.d };
        break;
      }
      case "add":
      case "sub": {
        const a = this.evalRaw(node.a, budgetBits, ctx);
        const b = this.evalRaw(node.b, budgetBits, ctx);
        if (a === null || b === null) {
          out = null;
          break;
        }
        const n = node.op === "add" ? a.n * b.d + b.n * a.d : a.n * b.d - b.n * a.d;
        const d = a.d * b.d;
        out = n === 0n ? { n: 0n, d: 1n } : { n, d };
        break;
      }
      case "mul": {
        const a = this.evalRaw(node.a, budgetBits, ctx);
        const b = this.evalRaw(node.b, budgetBits, ctx);
        if (a === null || b === null) {
          out = null;
          break;
        }
        out = { n: a.n * b.n, d: a.d * b.d };
        break;
      }
      case "div": {
        const a = this.evalRaw(node.a, budgetBits, ctx);
        const b = this.evalRaw(node.b, budgetBits, ctx);
        if (a === null || b === null) {
          out = null;
          break;
        }
        if (b.n === 0n) {
          ctx.divZero.add(root);
          out = null;
          break;
        }
        out = { n: a.n * b.d, d: a.d * b.n };
        break;
      }
    }

    if (out !== null) {
      if (out.d < 0n) out = { n: -out.n, d: -out.d };
      const bits = Math.max(bitLength(out.n), bitLength(out.d));
      ctx.peak = Math.max(ctx.peak, bits);
      if (bits > budgetBits) {
        ctx.over.add(root);
        out = null;
      }
    }

    this.cache.set(root, { value: out, peakBits: ctx.peak });
    return out;
  }

  /**
   * Fully evaluate and reduce a node to a canonical Fraction under the budget.
   * Reduction (one gcd) is performed only here, at the root, i.e. lazily.
   */
  evaluate(root: number, budgetBits = 65_536): EvalResult {
    this.checkId(root);
    this.cache.clear();
    const ctx = { over: new Set<number>(), divZero: new Set<number>(), peak: 0 };
    const raw = this.evalRaw(root, budgetBits, ctx);

    if (raw === null) {
      return {
        value: null,
        status: "incomplete",
        overBudget: [...ctx.over].sort((x, y) => x - y),
        divisionByZero: [...ctx.divZero].sort((x, y) => x - y),
        peakBits: ctx.peak,
      };
    }

    // Delayed reduction: the single normalization step at the end.
    const value: Fraction = frac(raw.n, raw.d);
    return {
      value,
      status: "ok",
      overBudget: [],
      divisionByZero: [],
      peakBits: ctx.peak,
    };
  }

  /**
   * Compare two expressions by cross multiplication under the budget.
   *
   * The raw pairs A = an/ad and B = bn/bd are compared through an*bd vs bn*ad.
   * Only that single cross product's size is budget-checked; no decimal string
   * is expanded and no full normalization is required to decide the order.
   */
  compare(a: number, b: number, budgetBits = 65_536): CompareResult {
    this.checkId(a);
    this.checkId(b);
    this.cache.clear();
    const ctx = { over: new Set<number>(), divZero: new Set<number>(), peak: 0 };
    const av = this.evalRaw(a, budgetBits, ctx);
    const bv = this.evalRaw(b, budgetBits, ctx);

    if (av === null || bv === null) {
      return {
        result: null,
        status: "incomplete",
        overBudget: [...ctx.over].sort((x, y) => x - y),
        divisionByZero: [...ctx.divZero].sort((x, y) => x - y),
        peakBits: ctx.peak,
      };
    }

    const lhs = av.n * bv.d;
    const rhs = bv.n * av.d;
    const crossBits = Math.max(bitLength(lhs), bitLength(rhs));
    ctx.peak = Math.max(ctx.peak, crossBits);

    if (crossBits > budgetBits) {
      return {
        result: null,
        status: "incomplete",
        overBudget: [...ctx.over].sort((x, y) => x - y),
        divisionByZero: [...ctx.divZero].sort((x, y) => x - y),
        peakBits: ctx.peak,
      };
    }

    return {
      result: lhs === rhs ? 0 : lhs < rhs ? -1 : 1,
      status: "ok",
      overBudget: [],
      divisionByZero: [],
      peakBits: ctx.peak,
    };
  }
}

/**
 * Fully reduced comparison helper retained for callers that already hold
 * canonical Fractions (kept here to document that comparison never needs
 * decimal expansion).
 */
export function compareFractions(a: Fraction, b: Fraction): -1 | 0 | 1 {
  return cmpFrac(a, b);
}

/** Expose gcd-based reduction for tests / callers reducing an explicit raw pair. */
export function reduceRaw(pair: RawPair): Fraction {
  return frac(pair.n, pair.d);
}
