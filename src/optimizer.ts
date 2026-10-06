// Optimizer. Two rewrites, both semantics-preserving:
//
// 1. Constant folding: a subtree that is pure AND total (cannot throw) is
//    replaced by its value. Pure-but-partial subtrees (containing div/mod/sqrt)
//    are never folded, because moving their potential error to optimize time
//    (or dropping it) would change observable behavior. An `if` with a literal
//    condition collapses to the taken branch (the literal has no effects).
//
// 2. Pure-subtree CSE: a pure subtree (total or partial) that occurs 2+ times
//    is wrapped at EVERY occurrence in a `shared(id)` node. Nothing is hoisted:
//    each `shared` body evaluates lazily at its first *dynamic* occurrence and
//    is memoized for the rest of the run. Consequences:
//      - impure subtrees are never shared (they are not pure), so side-effect
//        order and counts are untouched;
//      - a possibly-throwing pure subtree is never evaluated earlier than the
//        original program would evaluate it, so errors surface at the same
//        point, after the same side effects;
//      - occurrences hidden behind a not-taken lazy branch are never executed.

import type { Expr } from './ast.ts';
import { evaluate } from './interpreter.ts';
import { FUNCTIONS, newContext } from './runtime.ts';

export function isPure(e: Expr): boolean {
  switch (e.kind) {
    case 'lit':
      return true;
    case 'call':
      return FUNCTIONS[e.fn].pure && e.args.every(isPure);
    case 'if':
      return isPure(e.cond) && isPure(e.then) && isPure(e.else);
    case 'shared':
      return isPure(e.expr);
  }
}

export function isTotal(e: Expr): boolean {
  switch (e.kind) {
    case 'lit':
      return true;
    case 'call':
      return FUNCTIONS[e.fn].pure && !FUNCTIONS[e.fn].partial && e.args.every(isTotal);
    case 'if':
      return isTotal(e.cond) && isTotal(e.then) && isTotal(e.else);
    case 'shared':
      return isTotal(e.expr);
  }
}

function fold(e: Expr): Expr {
  switch (e.kind) {
    case 'lit':
      return e;
    case 'shared':
      return e;
    case 'if': {
      const cond = fold(e.cond);
      const then = fold(e.then);
      const els = fold(e.else);
      if (cond.kind === 'lit') return cond.value !== 0 ? then : els;
      if (then.kind === 'lit' && els.kind === 'lit' && then.value === els.value && isTotal(cond)) {
        return then;
      }
      return { kind: 'if', cond, then, else: els };
    }
    case 'call': {
      const node: Expr = { kind: 'call', fn: e.fn, args: e.args.map(fold) };
      if (isTotal(node)) {
        try {
          return { kind: 'lit', value: evaluate(node, newContext()) };
        } catch {
          return node; // defensive: a "total" subtree should never throw
        }
      }
      return node;
    }
  }
}

function keyOf(e: Expr): string {
  switch (e.kind) {
    case 'lit':
      return JSON.stringify(['l', e.value]);
    case 'call':
      return JSON.stringify(['c', e.fn, e.args.map(keyOf)]);
    case 'if':
      return JSON.stringify(['i', keyOf(e.cond), keyOf(e.then), keyOf(e.else)]);
    case 'shared':
      return JSON.stringify(['s', e.id]);
  }
}

function collectCounts(e: Expr, counts: Map<string, number>): void {
  if ((e.kind === 'call' || e.kind === 'if') && isPure(e)) {
    const k = keyOf(e);
    counts.set(k, (counts.get(k) ?? 0) + 1);
  }
  switch (e.kind) {
    case 'call':
      e.args.forEach((a) => collectCounts(a, counts));
      break;
    case 'if':
      collectCounts(e.cond, counts);
      collectCounts(e.then, counts);
      collectCounts(e.else, counts);
      break;
    case 'shared':
      collectCounts(e.expr, counts);
      break;
  }
}

function wrapChildren(e: Expr, counts: Map<string, number>, ids: Map<string, number>, nextId: () => number): Expr {
  switch (e.kind) {
    case 'call':
      return { kind: 'call', fn: e.fn, args: e.args.map((a) => wrapShared(a, counts, ids, nextId)) };
    case 'if':
      return {
        kind: 'if',
        cond: wrapShared(e.cond, counts, ids, nextId),
        then: wrapShared(e.then, counts, ids, nextId),
        else: wrapShared(e.else, counts, ids, nextId),
      };
    default:
      return e;
  }
}

function wrapShared(e: Expr, counts: Map<string, number>, ids: Map<string, number>, nextId: () => number): Expr {
  if (e.kind === 'call' || e.kind === 'if') {
    if (isPure(e)) {
      const k = keyOf(e);
      if ((counts.get(k) ?? 0) >= 2) {
        let id = ids.get(k);
        if (id === undefined) {
          id = nextId();
          ids.set(k, id);
        }
        return { kind: 'shared', id, expr: wrapChildren(e, counts, ids, nextId) };
      }
    }
    return wrapChildren(e, counts, ids, nextId);
  }
  return e;
}

export function optimize(e: Expr): Expr {
  const folded = fold(e);
  const counts = new Map<string, number>();
  collectCounts(folded, counts);
  let counter = 0;
  return wrapShared(folded, counts, new Map(), () => counter++);
}
