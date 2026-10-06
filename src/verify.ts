import type { BindingGraph } from './binder.ts';

export type IsomorphismResult =
  | { ok: true }
  | { ok: false; refId: number; fromDecl: number | null; toDecl: number | null };

// Two binding graphs are isomorphic when they have the same shape (same
// number of declarations and references, paired by source-order ordinal) and
// every reference resolves to the same declaration ordinal in both graphs.
// Null targets (unbound references) must also match.
export function checkIsomorphic(before: BindingGraph, after: BindingGraph): IsomorphismResult {
  if (before.decls.length !== after.decls.length || before.refs.length !== after.refs.length) {
    throw new Error('binding graphs are not structurally comparable');
  }
  for (const ref of before.refs) {
    const other = after.refs[ref.id];
    if (ref.declId !== other.declId) {
      return { ok: false, refId: ref.id, fromDecl: ref.declId, toDecl: other.declId };
    }
  }
  return { ok: true };
}
