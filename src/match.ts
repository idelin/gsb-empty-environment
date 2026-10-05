import { termEq } from "./term.ts";
import type { Term, TermFactory, Pat } from "./term.ts";

export type Bindings = ReadonlyMap<string, Term>;

/**
 * Match a pattern against a term, extending `bind`.
 * Repeated variables must match structurally equal subterms.
 * Returns true on success; `bind` is only mutated on the success path
 * of each individual call frame (callers pass a fresh/working map).
 */
export function match(pat: Pat, term: Term, bind: Map<string, Term>): boolean {
  if (pat.kind === "var") {
    const prev = bind.get(pat.name);
    if (prev === undefined) {
      bind.set(pat.name, term);
      return true;
    }
    return termEq(prev, term);
  }
  if (pat.op !== term.op || pat.args.length !== term.args.length) return false;
  for (let i = 0; i < pat.args.length; i++) {
    if (!match(pat.args[i], term.args[i], bind)) return false;
  }
  return true;
}

export function matchPattern(pat: Pat, term: Term): Bindings | null {
  const bind = new Map<string, Term>();
  return match(pat, term, bind) ? bind : null;
}

/**
 * Instantiate a replacement template under the given bindings.
 * Subterms substituted for variables are shared by reference (logical
 * copies); since terms are immutable this can never alias-mutate.
 */
export function instantiate(
  tpl: Pat,
  bind: Bindings,
  fac: TermFactory,
): Term {
  if (tpl.kind === "var") {
    const t = bind.get(tpl.name);
    if (t === undefined) {
      throw new Error(`unbound variable in template: ${tpl.name}`);
    }
    return t;
  }
  return fac.mk(tpl.op, tpl.args.map((a) => instantiate(a, bind, fac)));
}
