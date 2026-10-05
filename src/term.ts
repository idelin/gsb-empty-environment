/**
 * Immutable, hash-consed term trees (DAGs with maximal structural sharing).
 *
 * Nodes are frozen and never mutated. Rewriting performs path copying:
 * only the spine from the root to the rewritten node is rebuilt, every
 * other subtree is shared by reference. Because nodes are immutable, a
 * shared subtree can be "logically copied" for free — it is impossible to
 * pollute other positions through in-place modification.
 */

export interface Term {
  readonly id: number;
  readonly op: string;
  readonly args: readonly Term[];
}

/** Hash-consing factory: structurally equal terms are the same object. */
export class TermFactory {
  private table = new Map<string, Term>();
  private nextId = 0;

  mk(op: string, args: readonly Term[] = []): Term {
    const key = `${op}/${args.length}(${args.map((a) => a.id).join(",")})`;
    const hit = this.table.get(key);
    if (hit) return hit;
    const term: Term = Object.freeze({
      id: this.nextId++,
      op,
      args: Object.freeze(args.slice()),
    });
    this.table.set(key, term);
    return term;
  }
}

/** Canonical structural serialization (independent of hash-consing ids). */
export function canon(t: Term): string {
  if (t.args.length === 0) return t.op;
  return `${t.op}(${t.args.map(canon).join(",")})`;
}

/** Structural equality. With hash-consing this is reference equality. */
export function termEq(a: Term, b: Term): boolean {
  return a === b || canon(a) === canon(b);
}

export function show(t: Term): string {
  return canon(t);
}

/** Patterns / replacement templates. Variables only occur here, never in terms. */
export type Pat =
  | { readonly kind: "var"; readonly name: string }
  | { readonly kind: "op"; readonly op: string; readonly args: readonly Pat[] };

export function v(name: string): Pat {
  return { kind: "var", name };
}

export function p(op: string, ...args: Pat[]): Pat {
  return { kind: "op", op, args };
}

/** JSON serialization for snapshots (structural, sharing-agnostic). */
export interface TermJSON {
  op: string;
  args: TermJSON[];
}

export function termToJSON(t: Term): TermJSON {
  return { op: t.op, args: t.args.map(termToJSON) };
}

export function termFromJSON(j: TermJSON, fac: TermFactory): Term {
  return fac.mk(j.op, j.args.map((c) => termFromJSON(c, fac)));
}
