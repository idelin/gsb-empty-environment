/**
 * Pattern-matrix usefulness algorithm (Maranget, "Warnings for pattern
 * matching"), extended to return a counterexample pattern vector of minimal
 * constructor depth.
 *
 * A pattern vector q is "useful" w.r.t. a matrix P iff some value vector
 * matches q but matches no row of P. Exhaustiveness of rows P is decided by
 * usefulness of the all-wildcard vector; redundancy of row i is decided by
 * usefulness of row i w.r.t. the rows above it.
 *
 * Termination on recursive types: the algorithm only ever specializes along
 * constructors that literally occur in the (finite) patterns, and every
 * recursive call strictly decreases the lexicographic measure
 * (number of columns, total pattern size). Types are never unfolded, so
 * recursive types cannot cause infinite expansion.
 */

import { Ctx } from "./ctx";
import { Pattern, wild } from "./pattern";

export type PatternVector = Pattern[];
export type Matrix = PatternVector[];

/** Specialize the matrix on constructor `c` of the given arity (column 0). */
function specialize(P: Matrix, c: string, arity: number): Matrix {
  const out: Matrix = [];
  for (const row of P) {
    const head = row[0]!;
    if (head.kind === "ctor") {
      if (head.ctor === c) out.push([...head.args, ...row.slice(1)]);
    } else {
      const fill: Pattern[] = [];
      for (let i = 0; i < arity; i++) fill.push(wild());
      out.push([...fill, ...row.slice(1)]);
    }
  }
  return out;
}

/** Default matrix: rows whose head is not a constructor, head dropped. */
function defaultMatrix(P: Matrix): Matrix {
  const out: Matrix = [];
  for (const row of P) {
    if (row[0]!.kind !== "ctor") out.push(row.slice(1));
  }
  return out;
}

/** Depth of the shallowest value matched by pattern `p` at type `t`. */
export function patternDepth(ctx: Ctx, p: Pattern, t: string): number {
  if (p.kind !== "ctor") return ctx.minDepth.get(t)!;
  const info = ctx.ctorInfo(p.ctor);
  let worst = 0;
  for (let i = 0; i < p.args.length; i++) {
    worst = Math.max(worst, patternDepth(ctx, p.args[i]!, info.def.fields[i]!));
  }
  return 1 + worst;
}

export function vectorDepth(ctx: Ctx, pats: PatternVector, ts: string[]): number {
  let worst = 0;
  for (let i = 0; i < pats.length; i++) {
    worst = Math.max(worst, patternDepth(ctx, pats[i]!, ts[i]!));
  }
  return worst;
}

/**
 * If q is useful w.r.t. P, return a witness pattern vector (wildcards where
 * the shape is unconstrained) whose shallowest matched value vector has
 * minimal constructor depth; otherwise return null.
 *
 * `ts[i]` is the type of column i; every row of P and q must have the same
 * length as `ts`.
 */
export function useful(
  ctx: Ctx,
  P: Matrix,
  q: PatternVector,
  ts: string[]
): PatternVector | null {
  if (ts.length === 0) return P.length === 0 ? [] : null;
  const t = ts[0]!;
  const trest = ts.slice(1);
  const q1 = q[0]!;

  if (q1.kind === "ctor") {
    const info = ctx.ctorInfo(q1.ctor);
    const arity = info.def.fields.length;
    const r = useful(
      ctx,
      specialize(P, q1.ctor, arity),
      [...q1.args, ...q.slice(1)],
      [...info.def.fields, ...trest]
    );
    if (!r) return null;
    return [
      { kind: "ctor", ctor: q1.ctor, args: r.slice(0, arity) },
      ...r.slice(arity),
    ];
  }

  // q1 is a wildcard or variable.
  if (!ctx.inhabited(t)) return null;
  const used = new Set<string>();
  for (const row of P) {
    const head = row[0]!;
    if (head.kind === "ctor") used.add(head.ctor);
  }
  const allCtors = ctx.env[t]!.ctors;
  // Only constructors that can actually build a finite value matter;
  // uninhabited constructors can never start a counterexample.
  const missing = allCtors.filter(
    (c) => !used.has(c.name) && c.fields.every((f) => ctx.inhabited(f))
  );

  if (missing.length > 0) {
    const r = useful(ctx, defaultMatrix(P), q.slice(1), trest);
    if (!r) return null;
    let bestPat: Pattern | null = null;
    let bestDepth = Infinity;
    for (const c of missing) {
      let worst = 0;
      for (const f of c.fields) worst = Math.max(worst, ctx.minDepth.get(f)!);
      const d = 1 + worst;
      if (d < bestDepth) {
        bestDepth = d;
        bestPat = { kind: "ctor", ctor: c.name, args: c.fields.map(() => wild()) };
      }
    }
    return [bestPat!, ...r];
  }

  // Complete signature: every inhabitable constructor appears in column 0.
  let best: PatternVector | null = null;
  let bestDepth = Infinity;
  for (const c of allCtors) {
    if (!used.has(c.name)) continue;
    const arity = c.fields.length;
    const fill: Pattern[] = [];
    for (let i = 0; i < arity; i++) fill.push(wild());
    const r = useful(
      ctx,
      specialize(P, c.name, arity),
      [...fill, ...q.slice(1)],
      [...c.fields, ...trest]
    );
    if (r) {
      const vec: PatternVector = [
        { kind: "ctor", ctor: c.name, args: r.slice(0, arity) },
        ...r.slice(arity),
      ];
      const d = vectorDepth(ctx, vec, ts);
      if (d < bestDepth) {
        bestDepth = d;
        best = vec;
      }
    }
  }
  return best;
}
