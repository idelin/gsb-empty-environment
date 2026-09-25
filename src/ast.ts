import {
  cmpToIntervals,
  normalize,
  setToIntervals,
  type CmpOp,
  type Interval,
} from "./interval.ts";

/** Three-valued (Kleene) truth value: missing data yields "unknown". */
export type Tri = "true" | "false" | "unknown";

export type Cond =
  | { readonly type: "in"; readonly values: readonly number[] }
  | { readonly type: "intervals"; readonly intervals: readonly Interval[] };

/**
 * Boolean expression over integer attribute conditions.
 * Every node carries a unique `id`; `from` is the source map: the ids of the
 * original (pre-simplification) nodes that materially produced this node.
 */
export type Expr =
  | { readonly kind: "const"; readonly id: number; readonly value: boolean; readonly from: readonly number[] }
  | { readonly kind: "not"; readonly id: number; readonly child: Expr; readonly from: readonly number[] }
  | { readonly kind: "and"; readonly id: number; readonly args: readonly Expr[]; readonly from: readonly number[] }
  | { readonly kind: "or"; readonly id: number; readonly args: readonly Expr[]; readonly from: readonly number[] }
  | { readonly kind: "cond"; readonly id: number; readonly attr: string; readonly cond: Cond; readonly from: readonly number[] };

let nextId = 1;

/** Reset the id counter (mainly for deterministic tests). */
export function resetIds(): void {
  nextId = 1;
}

/** Allocate a fresh node id (also used for nodes synthesized by the simplifier). */
export function allocId(): number {
  return nextId++;
}

function leaf<T extends Omit<Expr, "id" | "from">>(node: T): Expr {
  const id = allocId();
  return { ...node, id, from: [id] } as Expr;
}

export function lit(value: boolean): Expr {
  return leaf({ kind: "const", value });
}

export function not(child: Expr): Expr {
  return leaf({ kind: "not", child });
}

export function and(...args: readonly Expr[]): Expr {
  return leaf({ kind: "and", args });
}

export function or(...args: readonly Expr[]): Expr {
  return leaf({ kind: "or", args });
}

export function cond(attr: string, c: Cond): Expr {
  return leaf({ kind: "cond", attr, cond: c });
}

/** Integer comparison condition, e.g. cmp("age", ">=", 18). */
export function cmp(attr: string, op: CmpOp, value: number): Expr {
  return cond(attr, { type: "intervals", intervals: cmpToIntervals(op, value) });
}

/** Finite-set membership condition, e.g. inSet("dept", [101, 102]). */
export function inSet(attr: string, values: readonly number[]): Expr {
  return cond(attr, { type: "in", values });
}

/** Canonical interval view of any condition. */
export function condToIntervals(c: Cond): Interval[] {
  return c.type === "in" ? setToIntervals(c.values) : normalize(c.intervals);
}

/** Structural key ignoring id/from; used for deduplication. */
export function structuralKey(e: Expr): string {
  switch (e.kind) {
    case "const":
      return `c:${e.value}`;
    case "not":
      return `n(${structuralKey(e.child)})`;
    case "and":
      return `a(${e.args.map(structuralKey).join(",")})`;
    case "or":
      return `o(${e.args.map(structuralKey).join(",")})`;
    case "cond":
      return `k(${e.attr}:${JSON.stringify(condToIntervals(e.cond))})`;
  }
}

export function show(e: Expr): string {
  switch (e.kind) {
    case "const":
      return e.value ? "TRUE" : "FALSE";
    case "not":
      return `NOT ${paren(e.child)}`;
    case "and":
      return e.args.map(paren).join(" AND ");
    case "or":
      return e.args.map(paren).join(" OR ");
    case "cond":
      return showCond(e.attr, e.cond);
  }
}

function paren(e: Expr): string {
  return e.kind === "and" || e.kind === "or" || e.kind === "not"
    ? `(${show(e)})`
    : show(e);
}

function showCond(attr: string, c: Cond): string {
  if (c.type === "in") {
    const values = [...c.values].sort((a, b) => a - b).join(", ");
    return `${attr} IN {${values}}`;
  }
  if (c.intervals.length === 0) return `${attr} IN ∅`;
  return `${attr} IN ${c.intervals.map(showInterval).join(" ∪ ")}`;
}

function showInterval(iv: Interval): string {
  const lo = iv.lo === -Infinity ? "-∞" : String(iv.lo);
  const hi = iv.hi === Infinity ? "+∞" : String(iv.hi);
  return `[${lo}, ${hi}]`;
}
