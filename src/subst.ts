import { Type } from "./types";

/**
 * A substitution maps variable ids to types. Bindings may themselves mention
 * variables that are bound again (a "chain"); `applySubst` always follows the
 * chain to the end, so substitution is transitive rather than one level.
 */
export type Subst = ReadonlyMap<number, Type>;

export const emptySubst: Subst = new Map<number, Type>();

export function bindVar(subst: Subst, id: number, t: Type): Subst {
  const next = new Map(subst);
  next.set(id, t);
  return next;
}

/**
 * Follow the head variable chain only: resolve(a) where a -> b, b -> List<c>
 * yields List<c> (whose element is NOT further resolved).
 */
export function resolve(subst: Subst, t: Type): Type {
  let cur = t;
  const seen = new Set<number>();
  while (cur.kind === "var") {
    if (seen.has(cur.id)) break; // defensive: cyclic input
    seen.add(cur.id);
    const bound = subst.get(cur.id);
    if (bound === undefined) return cur;
    cur = bound;
  }
  return cur;
}

/**
 * Fully apply a substitution, transitively. Every variable reachable through
 * arbitrarily long binding chains is replaced, recursively inside function
 * and list structure. The result is normalised: applying the same
 * substitution again is a no-op.
 */
export function applySubst(subst: Subst, t: Type): Type {
  return apply(subst, t, new Set());
}

function apply(subst: Subst, t: Type, active: Set<number>): Type {
  switch (t.kind) {
    case "var": {
      if (active.has(t.id)) return t; // defensive: cyclic input
      const bound = subst.get(t.id);
      if (bound === undefined) return t;
      active.add(t.id);
      const out = apply(subst, bound, active);
      active.delete(t.id);
      return out;
    }
    case "con":
      return t;
    case "fun":
      return {
        kind: "fun",
        arg: apply(subst, t.arg, active),
        ret: apply(subst, t.ret, active),
      };
    case "list":
      return { kind: "list", elem: apply(subst, t.elem, active) };
  }
}

/** Variables bound in the substitution. */
export function domain(subst: Subst): Set<number> {
  return new Set(subst.keys());
}

export function showSubst(subst: Subst, varName: (id: number) => string, showType: (t: Type) => string): string {
  const parts: string[] = [];
  for (const [id, t] of [...subst.entries()].sort((x, y) => x[0] - y[0])) {
    parts.push(`${varName(id)} := ${showType(t)}`);
  }
  return `{ ${parts.join(", ")} }`;
}
