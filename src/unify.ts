import { Type, TVar, showType, varName } from "./types";
import { Subst, emptySubst, bindVar, resolve } from "./subst";

/** An equality constraint between two types. */
export interface Constraint {
  readonly left: Type;
  readonly right: Type;
}

/**
 * One link of the chain reported when the occurs check fails: `variable`
 * was replaced by `unfoldsTo`, and following the replacements eventually
 * leads back to the variable being bound.
 */
export interface ChainStep {
  readonly variable: TVar;
  readonly unfoldsTo: Type;
}

export type UnifyError =
  | { readonly kind: "occurs"; readonly variable: TVar; readonly chain: readonly ChainStep[] }
  | { readonly kind: "mismatch"; readonly left: Type; readonly right: Type };

export type UnifyResult =
  | { readonly ok: true; readonly subst: Subst }
  | { readonly ok: false; readonly error: UnifyError };

export type UnifyAllResult =
  | { readonly ok: true; readonly subst: Subst }
  | { readonly ok: false; readonly error: UnifyError; readonly constraint: Constraint; readonly index: number };

/**
 * Does `target` occur in the full expansion of `t` under `subst`?
 * If so, return the substitution chain that leads from `t` back to
 * `target`; otherwise null. The chain contains one step per binding
 * unfolded on the way to the recursive occurrence.
 */
export function occursChain(target: number, t: Type, subst: Subst): ChainStep[] | null {
  return find(target, t, subst, [], new Set());
}

function find(
  target: number,
  t: Type,
  subst: Subst,
  path: ChainStep[],
  visited: Set<number>,
): ChainStep[] | null {
  switch (t.kind) {
    case "var": {
      if (t.id === target) return path;
      if (visited.has(t.id)) return null;
      const bound = subst.get(t.id);
      if (bound === undefined) return null;
      visited.add(t.id);
      return find(target, bound, subst, [...path, { variable: t, unfoldsTo: bound }], visited);
    }
    case "con":
      return null;
    case "fun":
      return (
        find(target, t.arg, subst, path, visited) ??
        find(target, t.ret, subst, path, visited)
      );
    case "list":
      return find(target, t.elem, subst, path, visited);
  }
}

function bind(subst: Subst, v: TVar, t: Type): UnifyResult {
  const rest = occursChain(v.id, t, subst);
  if (rest !== null) {
    return {
      ok: false,
      error: {
        kind: "occurs",
        variable: v,
        chain: [{ variable: v, unfoldsTo: t }, ...rest],
      },
    };
  }
  return { ok: true, subst: bindVar(subst, v.id, t) };
}

/** Unify two types under an existing substitution. */
export function unify(t1: Type, t2: Type, subst: Subst = emptySubst): UnifyResult {
  const a = resolve(subst, t1);
  const b = resolve(subst, t2);

  if (a.kind === "var" && b.kind === "var" && a.id === b.id) {
    return { ok: true, subst };
  }
  if (a.kind === "var") return bind(subst, a, b);
  if (b.kind === "var") return bind(subst, b, a);

  if (a.kind !== b.kind) {
    return { ok: false, error: { kind: "mismatch", left: a, right: b } };
  }
  switch (a.kind) {
    case "con":
      return a.name === (b as typeof a).name
        ? { ok: true, subst }
        : { ok: false, error: { kind: "mismatch", left: a, right: b } };
    case "fun": {
      const bf = b as Extract<Type, { kind: "fun" }>;
      const first = unify(a.arg, bf.arg, subst);
      if (!first.ok) return first;
      return unify(a.ret, bf.ret, first.subst);
    }
    case "list":
      return unify(a.elem, (b as Extract<Type, { kind: "list" }>).elem, subst);
  }
}

/** Solve a list of equality constraints left to right. */
export function unifyAll(constraints: readonly Constraint[]): UnifyAllResult {
  let subst: Subst = emptySubst;
  for (let i = 0; i < constraints.length; i++) {
    const c = constraints[i]!;
    const r = unify(c.left, c.right, subst);
    if (!r.ok) return { ok: false, error: r.error, constraint: c, index: i };
    subst = r.subst;
  }
  return { ok: true, subst };
}

export function showUnifyError(e: UnifyError): string {
  switch (e.kind) {
    case "occurs": {
      const lines = e.chain.map((step, i) => {
        const marker = i === e.chain.length - 1 ? "   <- recursive" : "";
        return `  ${varName(step.variable.id)} ~ ${showType(step.unfoldsTo)}${marker}`;
      });
      return [
        `occurs check failed: cannot construct the infinite type of ${varName(e.variable.id)}`,
        ...lines,
      ].join("\n");
    }
    case "mismatch":
      return `type mismatch: ${showType(e.left)} vs ${showType(e.right)}`;
  }
}
