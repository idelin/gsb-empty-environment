/**
 * Independent brute-force oracle used to cross-check the engine.
 *
 * It enumerates every distinct match OUTCOME (end position + full capture
 * vector) with a plain recursive walk over the AST - no memoization across
 * nodes, no preference-based pruning - and then picks the winner with its
 * own comparison logic. Exact duplicate outcomes are dropped (keeping them
 * could never change the winner); that is the only concession to
 * tractability, and it is provably outcome-neutral.
 *
 * The oracle deliberately represents non-participating groups as `null`
 * (the engine uses `undefined`) and shares nothing with the engine but the
 * AST type and the parser. It is NOT derived from the host RegExp engine:
 * host regexes use leftmost-first backtracking semantics, which differ from
 * the leftmost-longest + group-priority semantics specified here.
 */

import { Node } from '../src/ast';
import { Span } from '../src/matcher';

export interface OracleMatch {
  start: number;
  end: number;
  /** groups[0] unused; groups[i] is null when group i did not participate. */
  groups: Array<Span | null>;
}

type Path = [number, Array<Span | null>];

function fresh(n: number): Array<Span | null> {
  return new Array<Span | null>(n + 1).fill(null);
}

function overlay(base: Array<Span | null>, over: Array<Span | null>): Array<Span | null> {
  const out = base.slice();
  for (let i = 0; i < over.length; i++) {
    if (over[i] !== null) out[i] = over[i];
  }
  return out;
}

function keyOf(pos: number, g: Array<Span | null>): string {
  let key = pos + '|';
  for (let i = 0; i < g.length; i++) {
    const s = g[i];
    key += s === null ? '-' : s.start + ',' + s.end;
    key += ';';
  }
  return key;
}

function pushNew(states: Path[], seen: Set<string>, end: number, g: Array<Span | null>): void {
  const key = keyOf(end, g);
  if (seen.has(key)) return;
  seen.add(key);
  states.push([end, g]);
}

function* walk(node: Node, input: string, pos: number, n: number): Generator<Path, void, undefined> {
  switch (node.kind) {
    case 'empty':
      yield [pos, fresh(n)];
      return;
    case 'char':
      if (pos < input.length && input[pos] === node.ch) yield [pos + 1, fresh(n)];
      return;
    case 'group':
      for (const [end, g] of walk(node.child, input, pos, n)) {
        const g2 = g.slice();
        g2[node.index] = { start: pos, end };
        yield [end, g2];
      }
      return;
    case 'alt':
      for (const branch of node.branches) yield* walk(branch, input, pos, n);
      return;
    case 'concat': {
      let states: Path[] = [[pos, fresh(n)]];
      for (const part of node.parts) {
        const next: Path[] = [];
        const seen = new Set<string>();
        for (const [p, g] of states) {
          for (const [end, g2] of walk(part, input, p, n)) {
            pushNew(next, seen, end, overlay(g, g2));
          }
        }
        states = next;
      }
      yield* states;
      return;
    }
    case 'star':
      yield* walkStar(node.child, input, pos, fresh(n), n, new Set<string>());
      return;
  }
}

function* walkStar(
  child: Node,
  input: string,
  pos: number,
  acc: Array<Span | null>,
  n: number,
  seen: Set<string>,
): Generator<Path, void, undefined> {
  // Path 1: stop iterating here.
  yield [pos, acc];
  for (const [end, g] of walk(child, input, pos, n)) {
    const merged = overlay(acc, g);
    if (end === pos) {
      // Path 2: a zero-width iteration is allowed only as the final one,
      // which keeps enumeration finite.
      yield [end, merged];
    } else {
      // Path 3: a consuming iteration, then continue iterating. Identical
      // (position, captures) states are expanded only once.
      const key = keyOf(end, merged);
      if (seen.has(key)) continue;
      seen.add(key);
      yield* walkStar(child, input, end, merged, n, seen);
    }
  }
}

/** Independently written group-priority preference: is `a` better than `b`? */
function preferred(a: Array<Span | null>, b: Array<Span | null>): boolean {
  for (let i = 1; i < a.length; i++) {
    const x = a[i];
    const y = b[i];
    if (x === null && y === null) continue;
    if (x === null) return false; // participating beats absent
    if (y === null) return true;
    const la = x.end - x.start;
    const lb = y.end - y.start;
    if (la !== lb) return la > lb;
    if (x.start !== y.start) return x.start < y.start;
  }
  return false;
}

export function oracleSearch(root: Node, input: string, groupCount: number): OracleMatch | null {
  for (let start = 0; start <= input.length; start++) {
    let bestEnd = -1;
    const seen = new Set<string>();
    const winners: Array<Array<Span | null>> = [];
    for (const [end, g] of walk(root, input, start, groupCount)) {
      if (end < bestEnd) continue;
      const key = keyOf(end, g);
      if (end === bestEnd && seen.has(key)) continue;
      if (end > bestEnd) {
        bestEnd = end;
        seen.clear();
        winners.length = 0;
      }
      seen.add(key);
      winners.push(g);
    }
    if (bestEnd < 0) continue;
    let best = winners[0];
    for (const g of winners) {
      if (preferred(g, best)) best = g;
    }
    return { start, end: bestEnd, groups: best };
  }
  return null;
}

/** Full outcome sets per start position, for differential set comparison. */
export function oracleEnumerateAll(
  root: Node,
  input: string,
  groupCount: number,
): Array<Array<{ end: number; groups: Array<Span | null> }>> {
  const out: Array<Array<{ end: number; groups: Array<Span | null> }>> = [];
  for (let start = 0; start <= input.length; start++) {
    const seen = new Set<string>();
    const list: Array<{ end: number; groups: Array<Span | null> }> = [];
    for (const [end, g] of walk(root, input, start, groupCount)) {
      const key = keyOf(end, g);
      if (seen.has(key)) continue;
      seen.add(key);
      list.push({ end, groups: g });
    }
    out.push(list);
  }
  return out;
}
