// Optimizer: common-subexpression elimination over pure subtrees only.
//
// Safety invariants:
//  1. Only pure subtrees (no `eff` inside) are ever shared. Side-effecting
//     calls are never deduplicated, moved, or reordered.
//  2. A fallible pure subtree (contains div/mod, may throw) is bound at a
//     site such that, along the path from the site to the first occurrence,
//     every edge is strict and every earlier-evaluated sibling is pure and
//     total. Hence it is never executed earlier across a lazy branch, an
//     effect, or another possibly-throwing expression.
//  3. An infallible pure subtree (pure + total) has no observable behavior,
//     so it may be hoisted to the root and shared globally.
//
// Sharing is materialized as `let name = <subtree> in ...` with `var name`
// at use sites; the interpreter evaluates let strictly, in order.

import {
  Expr, hash, analyze, isPureTotal, children, isStrictChild, withChild,
} from './ast';

type Path = number[]; // child indices from the root, in evaluation order

export function optimize(root: Expr): Expr {
  // Group pure subtrees of the original program by structural hash.
  const counts = new Map<string, { e: Expr; n: number; size: number }>();
  const visit = (e: Expr): void => {
    if (
      (e.tag === 'pure' || e.tag === 'if' || e.tag === 'and' || e.tag === 'or') &&
      !analyze(e).hasEff
    ) {
      const h = hash(e);
      const c = counts.get(h);
      if (c) c.n += 1;
      else counts.set(h, { e, n: 1, size: analyze(e).size });
    }
    children(e).forEach(visit);
  };
  visit(root);

  // Larger subtrees first so outer shares are established before inner ones.
  const classes = [...counts.values()]
    .filter((c) => c.n >= 2)
    .sort((a, b) => b.size - a.size);

  let out = root;
  let fresh = 0;

  for (const c of classes) {
    const h = hash(c.e);
    const fallible = analyze(c.e).hasFallible;

    // Repeatedly bind one group of occurrences until fewer than two remain.
    for (;;) {
      const occs = findOccurrences(out, h);
      if (occs.length < 2) break;

      let rewrote = false;
      for (const occ of occs) {
        const site = bindingSite(out, occ, fallible);
        if (site === null) continue;
        const inside = occs.filter((o) => hasPrefix(o, site));
        if (inside.length < 2) continue;

        const name = `__cse${fresh++}`;
        const bound = getAt(out, inside[0]);
        const atSite = getAt(out, site);
        const body = replaceHash(atSite, h, { tag: 'var', name });
        const letNode: Expr = { tag: 'let', name, value: bound, body };
        out = replaceAt(out, site, letNode);
        rewrote = true;
        break;
      }
      if (!rewrote) break;
    }
  }
  return out;
}

// Highest ancestor of `occ` at which a let-binding for this class may be
// placed without changing observable behavior. Returns null when the
// occurrence cannot be shared with anything above itself.
function bindingSite(root: Expr, occ: Path, fallible: boolean): Path | null {
  if (!fallible) return []; // pure + total: hoist to the root

  // Walk from the root down to the occurrence. A binding site must be a
  // prefix of the path such that every edge below it is strict and every
  // earlier-evaluated sibling along the path is pure and total.
  let iMin = 0;
  let node = root;
  for (let j = 0; j < occ.length; j++) {
    const idx = occ[j];
    let ok = isStrictChild(node, idx);
    if (ok) {
      const kids = children(node);
      for (let k = 0; k < idx; k++) {
        if (isStrictChild(node, k) && !isPureTotal(kids[k])) {
          ok = false;
          break;
        }
      }
    }
    if (!ok) iMin = j + 1; // site must lie strictly below this edge
    node = children(node)[idx];
  }
  if (iMin >= occ.length) return null;
  return occ.slice(0, iMin);
}

function findOccurrences(root: Expr, h: string): Path[] {
  const out: Path[] = [];
  const go = (e: Expr, path: Path): void => {
    if (hash(e) === h) out.push(path);
    children(e).forEach((c, i) => go(c, [...path, i]));
  };
  go(root, []);
  return out;
}

function hasPrefix(path: Path, prefix: Path): boolean {
  return prefix.length <= path.length && prefix.every((v, i) => path[i] === v);
}

function getAt(root: Expr, path: Path): Expr {
  return path.reduce((e, i) => children(e)[i], root);
}

function replaceAt(root: Expr, path: Path, node: Expr): Expr {
  if (path.length === 0) return node;
  const [i, ...rest] = path;
  return withChild(root, i, replaceAt(children(root)[i], rest, node));
}

// Replace every subtree with hash `h` by `repl` (bottom-up).
function replaceHash(e: Expr, h: string, repl: Expr): Expr {
  if (hash(e) === h) return repl;
  const kids = children(e);
  if (kids.length === 0) return e;
  let out = e;
  kids.forEach((k, i) => {
    out = withChild(out, i, replaceHash(k, h, repl));
  });
  return out;
}
