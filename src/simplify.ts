import { allocId, condToIntervals, structuralKey, type Expr } from "./ast.ts";
import {
  complement,
  coversDomain,
  intersect,
  normalize,
  union,
  type Interval,
} from "./interval.ts";

/** Declared finite integer domain of one attribute. */
export interface AttrDomain {
  readonly min: number;
  readonly max: number;
  /** required = always present; optional attributes may be missing (-> unknown). */
  readonly required: boolean;
}

export type Domains = Readonly<Record<string, AttrDomain>>;

function unionFrom(...parts: readonly (readonly number[])[]): number[] {
  return [...new Set(parts.flat())].sort((a, b) => a - b);
}

function constNode(value: boolean, from: readonly number[]): Expr {
  return { kind: "const", id: allocId(), value, from: [...from] };
}

function condNode(attr: string, intervals: readonly Interval[], from: readonly number[]): Expr {
  return {
    kind: "cond",
    id: allocId(),
    attr,
    cond: { type: "intervals", intervals: normalize(intervals) },
    from: [...from],
  };
}

/**
 * Normalize a condition against its declared domain.
 * Collapsing to a constant is only sound when the attribute is required:
 * for an optional attribute an empty interval still means "false if present,
 * unknown if missing", which is NOT the constant false.
 */
function normCond(
  attr: string,
  intervals: readonly Interval[],
  from: readonly number[],
  domains: Domains,
): Expr {
  const n = normalize(intervals);
  const d = domains[attr];
  if (d !== undefined && d.required) {
    if (n.length === 0) return constNode(false, from);
    if (coversDomain(n, d.min, d.max)) return constNode(true, from);
  }
  return condNode(attr, n, from);
}

type CondExpr = Extract<Expr, { kind: "cond" }>;

/**
 * Semantics-preserving simplification for Kleene three-valued logic.
 *
 * Applied (sound) laws:
 *   false AND x -> false        true OR x -> true      (domination)
 *   true AND x  -> x            false OR x -> x        (identity)
 *   NOT (NOT x) -> x                                 (involution)
 *   x AND x -> x                x OR x -> x            (idempotence)
 *   NOT cond    -> complemented interval condition
 *   conditions on the same attribute merge via interval intersection (AND)
 *   or union (OR); empty/full results collapse to constants only for
 *   required attributes.
 *
 * Deliberately NOT applied (unsound when attributes may be missing):
 *   x OR NOT x  -> true   (excluded middle: unknown OR NOT unknown = unknown)
 *   x AND NOT x -> false  (contradiction: unknown AND NOT unknown = unknown)
 */
export function simplify(expr: Expr, domains: Domains = {}): Expr {
  switch (expr.kind) {
    case "const":
      return expr;
    case "cond":
      return normCond(expr.attr, condToIntervals(expr.cond), expr.from, domains);
    case "not": {
      const child = simplify(expr.child, domains);
      const from = unionFrom([expr.id], child.from);
      if (child.kind === "const") return constNode(!child.value, from);
      if (child.kind === "not") {
        return { ...child.child, from: unionFrom(from, child.child.from) };
      }
      if (child.kind === "cond") {
        return normCond(child.attr, complement(condToIntervals(child.cond)), from, domains);
      }
      return { kind: "not", id: expr.id, child, from };
    }
    case "and":
    case "or": {
      const isAnd = expr.kind === "and";
      const zero = !isAnd; // short-circuit value: AND -> false, OR -> true
      const simplified = expr.args.map((a) => simplify(a, domains));

      // flatten nested nodes of the same operator (associativity)
      const flat: Expr[] = [];
      const foldedFrom: number[] = [expr.id];
      for (const a of simplified) {
        if (a.kind === expr.kind) {
          flat.push(...a.args);
          foldedFrom.push(a.id);
        } else {
          flat.push(a);
        }
      }

      // domination: false AND ... -> false / true OR ... -> true
      // dropped branches contribute nothing, so their ids leave the source map
      const shorts = flat.filter((a) => a.kind === "const" && a.value === zero);
      if (shorts.length > 0) {
        return constNode(zero, unionFrom(foldedFrom, ...shorts.map((s) => s.from)));
      }

      // identity: drop `true` from AND / `false` from OR
      const kept: Expr[] = [];
      const identityFrom: number[][] = [];
      for (const a of flat) {
        if (a.kind === "const") identityFrom.push(a.from);
        else kept.push(a);
      }

      // idempotence: deduplicate structurally identical args
      const byKey = new Map<string, Expr>();
      for (const a of kept) {
        const key = structuralKey(a);
        const prev = byKey.get(key);
        byKey.set(key, prev === undefined ? a : { ...prev, from: unionFrom(prev.from, a.from) });
      }

      // interval algebra: merge conditions that share an attribute
      const condsByAttr = new Map<string, CondExpr[]>();
      const others: Expr[] = [];
      for (const a of byKey.values()) {
        if (a.kind === "cond") {
          const list = condsByAttr.get(a.attr) ?? [];
          list.push(a);
          condsByAttr.set(a.attr, list);
        } else {
          others.push(a);
        }
      }
      const merged: Expr[] = [];
      for (const attr of [...condsByAttr.keys()].sort()) {
        const list = condsByAttr.get(attr) as CondExpr[];
        let ivs: Interval[] = condToIntervals(list[0].cond);
        for (const c of list.slice(1)) {
          const next = condToIntervals(c.cond);
          ivs = isAnd ? intersect(ivs, next) : union(ivs, next);
        }
        merged.push(normCond(attr, ivs, unionFrom(...list.map((c) => c.from)), domains));
      }

      // a merged condition may have collapsed to a constant (required attribute)
      const all = [...others, ...merged];
      const lateShorts = all.filter((a) => a.kind === "const" && a.value === zero);
      if (lateShorts.length > 0) {
        return constNode(zero, unionFrom(foldedFrom, ...lateShorts.map((s) => s.from)));
      }
      const finalArgs = all.filter((a) => {
        if (a.kind === "const") {
          identityFrom.push(a.from);
          return false;
        }
        return true;
      });
      finalArgs.sort((a, b) => (structuralKey(a) < structuralKey(b) ? -1 : 1));

      const baseFrom = unionFrom(foldedFrom, ...identityFrom);
      if (finalArgs.length === 0) return constNode(isAnd, baseFrom); // all identities
      if (finalArgs.length === 1) {
        return { ...finalArgs[0], from: unionFrom(baseFrom, finalArgs[0].from) };
      }
      return {
        kind: expr.kind,
        id: expr.id,
        args: finalArgs,
        from: unionFrom(baseFrom, ...finalArgs.map((a) => a.from)),
      };
    }
  }
}
