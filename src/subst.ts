import type { Type } from "./types.js";
import { tfun, tlist } from "./types.js";

export type Subst = Map<number, Type>;

export function applySubst(subst: Subst, t: Type): Type {
  if (t.kind === "var") {
    const seen = new Set<number>();
    let cur: Type = t;
    while (cur.kind === "var") {
      if (seen.has(cur.id)) break;
      seen.add(cur.id);
      const next = subst.get(cur.id);
      if (next === undefined) return cur;
      cur = next;
    }
    if (cur.kind === "var") return cur;
    return applySubst(subst, cur);
  }
  if (t.kind === "fun") {
    return tfun(applySubst(subst, t.param), applySubst(subst, t.result));
  }
  if (t.kind === "list") {
    return tlist(applySubst(subst, t.element));
  }
  return t;
}

export function normalizeSubst(subst: Subst): Subst {
  const out: Subst = new Map<number, Type>();
  for (const [id, t] of subst) {
    out.set(id, applySubst(subst, t));
  }
  return out;
}

export function composeSubst(outer: Subst, inner: Subst): Subst {
  const out: Subst = new Map<number, Type>();
  for (const [id, t] of inner) {
    out.set(id, applySubst(outer, t));
  }
  for (const [id, t] of outer) {
    if (!out.has(id)) out.set(id, t);
  }
  return out;
}
