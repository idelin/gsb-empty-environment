import type { TreeIndex } from './tree.ts';
import type { Matching } from './match.ts';

/**
 * Cycle normalization.
 *
 * A matching tells every matched source node where it ends up: under the
 * correspondent of its target partner's parent. Chasing those "final parent"
 * pointers can yield a cycle (e.g. a is matched inside b's subtree while b is
 * matched inside a's subtree) — realizing such a matching would require
 * moving a node into its own descendant, i.e. an ancestor cycle, which is
 * impossible in a tree.
 *
 * This function removes pairs until the final-parent graph is acyclic.
 * It prefers to drop pairs that are not id-forced, then pairs with the
 * smallest source subtree (cheapest to re-create via delete+insert).
 * Deterministic.
 */
export function normalizeMatching(
  matching: Matching,
  src: TreeIndex,
  dst: TreeIndex,
  forced: number[],
): Matching {
  const m: Matching = new Map(matching);
  const forcedSet = new Set<number>();
  for (let t = 0; t < forced.length; t++) {
    if (forced[t] >= 0) forcedSet.add(forced[t]);
  }
  // The root pair is never removed; the root has no parent and can never be
  // part of a final-parent cycle.
  for (let guard = 0; guard <= m.size; guard++) {
    const cycle = findCycle(m, dst);
    if (cycle === null) return m;
    // cycle: list of matched source indices participating in the cycle.
    let victim = -1;
    let victimScore: [number, number, number] | null = null;
    for (const s of cycle) {
      // Prefer non-forced (0 before 1), then smallest subtree, then deepest
      // index (most local). Lexicographic comparison, lower wins.
      const score: [number, number, number] = [forcedSet.has(s) ? 1 : 0, src.size[s], -s];
      if (victimScore === null || compareScore(score, victimScore) < 0) {
        victimScore = score;
        victim = s;
      }
    }
    m.delete(victim);
  }
  return m; // unreachable in practice; loop always terminates
}

function compareScore(a: [number, number, number], b: [number, number, number]): number {
  for (let i = 0; i < 3; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return 0;
}

/**
 * Builds the final-parent graph and finds one cycle of matched source nodes.
 *
 * Graph nodes are identified by strings: `s<i>` for a matched source node,
 * `t<j>` for an inserted (unmatched) target node. Edges point from a node to
 * its final parent. Unmatched source nodes are deleted and absent; the
 * matched root has no outgoing edge.
 *
 * Returns the matched source indices on one cycle, or null if acyclic.
 */
function findCycle(m: Matching, dst: TreeIndex): number[] | null {
  const partnerT = new Map<number, number>(); // tgtIdx -> srcIdx
  for (const [s, t] of m) partnerT.set(t, s);

  const parentKey = (key: string): string | null => {
    // key is `s<i>` (matched source i, partner t) or `t<j>` (inserted target j)
    const tIdx = key.charCodeAt(0) === 115 ? m.get(Number(key.slice(1)))! : Number(key.slice(1));
    const p = dst.parent[tIdx];
    if (p < 0) return null; // target root: no parent
    const sp = partnerT.get(p);
    return sp === undefined ? `t${p}` : `s${sp}`;
  };

  const keys: string[] = [];
  for (const [s] of m) keys.push(`s${s}`);
  for (let t = 0; t < dst.nodes.length; t++) {
    if (!partnerT.has(t) && dst.parent[t] >= 0) keys.push(`t${t}`);
  }

  // Each node has at most one outgoing edge: detect cycles by walking.
  const state = new Map<string, number>(); // 0 = unvisited, 1 = on stack, 2 = done
  for (const start of keys) {
    if (state.get(start) === 2) continue;
    const trail: string[] = [];
    let cur: string | null = start;
    while (cur !== null && state.get(cur) !== 2) {
      if (state.get(cur) === 1) {
        // Found a cycle: extract the matched source nodes on it.
        const loop = trail.slice(trail.indexOf(cur));
        const matched: number[] = [];
        for (const k of loop) {
          if (k.charCodeAt(0) === 115) matched.push(Number(k.slice(1)));
        }
        // A cycle always contains at least one matched source node (inserted
        // nodes alone chain strictly upward along target ancestry).
        return matched;
      }
      state.set(cur, 1);
      trail.push(cur);
      cur = parentKey(cur);
    }
    for (const k of trail) state.set(k, 2);
  }
  return null;
}
