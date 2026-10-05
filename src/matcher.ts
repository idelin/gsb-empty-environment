/**
 * Matching engine.
 *
 * For every (node, position) we compute the set of all reachable end
 * positions together with the capture vector of every distinct path that
 * reaches them. Capture vectors are kept whole (never pruned by preference):
 * an enclosing star may overwrite only some groups in a later iteration, so a
 * locally "worse" vector can still lead to the globally preferred result.
 *
 * Zero-length loop safety: a star iteration that matches the empty string
 * terminates that star (it may contribute its captures, but no further
 * iteration is started from it). Non-empty iterations strictly advance the
 * input position, so enumeration always terminates.
 *
 * Complexity note: the number of distinct capture vectors is, in the worst
 * case, exponential in the number of groups (typical for engines with
 * POSIX-style submatch semantics). Typical patterns run near-linear; deeply
 * nested stars over many groups can be expensive. Results are memoized per
 * (node, position) and deduplicated by hash.
 */

import { Node } from './ast';

export interface Span {
  start: number;
  end: number;
}

/** Capture vector; index 0 is unused, groups are 1..groupCount. */
export type Captures = ReadonlyArray<Span | undefined>;

export interface MatchResult {
  start: number;
  end: number;
  captures: Captures;
}

/** end position -> distinct capture vectors reaching it */
type ResultSet = Map<number, Captures[]>;

/** Overlay two capture vectors: entries set in `over` win. */
function mergeCaps(base: Captures, over: Captures): Captures {
  const out = base.slice();
  for (let i = 0; i < over.length; i++) {
    if (over[i] !== undefined) out[i] = over[i];
  }
  return out;
}

function keyOf(end: number, caps: Captures): string {
  let key = end + '|';
  for (let i = 0; i < caps.length; i++) {
    const s = caps[i];
    key += s === undefined ? '-' : s.start + ',' + s.end;
    key += ';';
  }
  return key;
}

/** Collects distinct capture vectors per end position (hash dedup). */
class ResultBuilder {
  private readonly seen = new Set<string>();
  readonly set: ResultSet = new Map();

  add(end: number, caps: Captures): boolean {
    const key = keyOf(end, caps);
    if (this.seen.has(key)) return false;
    this.seen.add(key);
    let list = this.set.get(end);
    if (list === undefined) {
      list = [];
      this.set.set(end, list);
    }
    list.push(caps);
    return true;
  }
}

class Engine {
  private readonly memo = new Map<Node, Map<number, ResultSet>>();

  constructor(private readonly input: string, private readonly groupCount: number) {}

  private emptyCaps(): Captures {
    return new Array<Span | undefined>(this.groupCount + 1).fill(undefined);
  }

  matches(node: Node, pos: number): ResultSet {
    let byPos = this.memo.get(node);
    if (byPos === undefined) {
      byPos = new Map();
      this.memo.set(node, byPos);
    }
    const hit = byPos.get(pos);
    if (hit !== undefined) return hit;
    const rs = this.compute(node, pos);
    byPos.set(pos, rs);
    return rs;
  }

  private compute(node: Node, pos: number): ResultSet {
    switch (node.kind) {
      case 'empty': {
        const out = new ResultBuilder();
        out.add(pos, this.emptyCaps());
        return out.set;
      }
      case 'char': {
        const out = new ResultBuilder();
        if (pos < this.input.length && this.input[pos] === node.ch) {
          out.add(pos + 1, this.emptyCaps());
        }
        return out.set;
      }
      case 'group': {
        // The child already yields distinct vectors per end position, and
        // stamping this group's span cannot make two of them equal.
        const out = new ResultBuilder();
        for (const [end, list] of this.matches(node.child, pos)) {
          for (const caps of list) {
            const stamped = caps.slice();
            stamped[node.index] = { start: pos, end };
            out.add(end, stamped);
          }
        }
        return out.set;
      }
      case 'alt': {
        const out = new ResultBuilder();
        for (const branch of node.branches) {
          for (const [end, list] of this.matches(branch, pos)) {
            for (const caps of list) out.add(end, caps);
          }
        }
        return out.set;
      }
      case 'concat': {
        let states: Array<readonly [number, Captures]> = [[pos, this.emptyCaps()]];
        for (const part of node.parts) {
          const seen = new Set<string>();
          const next: Array<readonly [number, Captures]> = [];
          for (const [p, caps] of states) {
            for (const [end, list] of this.matches(part, p)) {
              for (const c of list) {
                const merged = mergeCaps(caps, c);
                const key = keyOf(end, merged);
                if (!seen.has(key)) {
                  seen.add(key);
                  next.push([end, merged]);
                }
              }
            }
          }
          states = next;
        }
        const out = new ResultBuilder();
        for (const [end, caps] of states) out.add(end, caps);
        return out.set;
      }
      case 'star': {
        const out = new ResultBuilder();
        const seed = this.emptyCaps();
        out.add(pos, seed);
        const queue: Array<readonly [number, Captures]> = [[pos, seed]];
        // `out.seen` already contains the seed; a vector that was recorded
        // once never needs to be expanded again, because its continuations
        // depend only on (position, captures).
        for (let head = 0; head < queue.length; head++) {
          const [p, caps] = queue[head];
          for (const [end, list] of this.matches(node.child, p)) {
            for (const c of list) {
              const merged = mergeCaps(caps, c);
              if (!out.add(end, merged)) continue;
              if (end > p) {
                queue.push([end, merged]);
              }
              // end === p: zero-width iteration terminates this star.
            }
          }
        }
        return out.set;
      }
    }
  }
}

/**
 * Group-priority comparison of two capture vectors that belong to matches
 * with the same overall span. Returns a positive number if `a` is preferred,
 * negative if `b` is preferred, 0 if equivalent.
 *
 * For groups 1..n in order: a participating group beats a non-participating
 * one; among participating groups the longer capture wins; ties are broken by
 * earlier start (determinism only).
 */
export function compareCaptures(a: Captures, b: Captures): number {
  for (let i = 1; i < a.length; i++) {
    const x = a[i];
    const y = b[i];
    if (x === undefined && y === undefined) continue;
    if (x === undefined) return -1;
    if (y === undefined) return 1;
    const lenX = x.end - x.start;
    const lenY = y.end - y.start;
    if (lenX !== lenY) return lenX - lenY;
    if (x.start !== y.start) return y.start - x.start;
  }
  return 0;
}

/**
 * Leftmost-longest search: smallest start position with any match; at that
 * start, the longest overall match; among those, the capture vector preferred
 * by group order (see compareCaptures).
 */
export function search(root: Node, input: string, groupCount: number): MatchResult | null {
  const engine = new Engine(input, groupCount);
  for (let start = 0; start <= input.length; start++) {
    const rs = engine.matches(root, start);
    if (rs.size === 0) continue;
    let bestEnd = -1;
    for (const end of rs.keys()) {
      if (end > bestEnd) bestEnd = end;
    }
    const list = rs.get(bestEnd) as Captures[];
    let best = list[0];
    for (const caps of list) {
      if (compareCaptures(caps, best) > 0) best = caps;
    }
    return { start, end: bestEnd, captures: best };
  }
  return null;
}

/**
 * Test/support helper: for every start position, the full set of distinct
 * match outcomes (end position + capture vector). This is the raw material
 * that leftmost-longest selection picks from.
 */
export function enumerateAll(
  root: Node,
  input: string,
  groupCount: number,
): Array<Array<{ end: number; captures: Captures }>> {
  const engine = new Engine(input, groupCount);
  const out: Array<Array<{ end: number; captures: Captures }>> = [];
  for (let start = 0; start <= input.length; start++) {
    const rs = engine.matches(root, start);
    const list: Array<{ end: number; captures: Captures }> = [];
    for (const [end, capsList] of rs) {
      for (const captures of capsList) list.push({ end, captures });
    }
    out.push(list);
  }
  return out;
}
