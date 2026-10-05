import { subtreeHash } from './tree.ts';
import type { TreeIndex } from './tree.ts';

/**
 * A matching pairs source preorder indices with target preorder indices.
 * It is the decision variable of the optimizer: every matching induces a
 * complete edit script (see script.ts) whose cost we minimize.
 */
export type Matching = Map<number, number>; // srcIdx -> tgtIdx

export const UNMATCHED = -2; // target node decided as "insert" (no partner)
export const UNDECIDED = -1;

/**
 * Two nodes may be paired only if they have the same type and compatible
 * stable ids (equal ids, or both absent). A type change is therefore always
 * expressed as delete+insert; an id that disappears is a deletion, not a
 * rename. The roots are exempt (always paired; see forcedPairs).
 */
export function pairable(s: TreeNode, t: TreeNode): boolean {
  return s.type === t.type && s.id === t.id;
}

/**
 * Forced pairs from stable ids: when an id occurs on both sides, the k-th
 * occurrence (preorder) on the source is bound to the k-th on the target.
 * The root pair (0,0) always takes precedence and is itself forced.
 * Returns an array indexed by target preorder index: forced partner source
 * index, or -1 when the target node is free.
 */
export function forcedPairs(src: TreeIndex, dst: TreeIndex): number[] {
  const forced = new Array<number>(dst.nodes.length).fill(-1);
  const srcById = new Map<string, number[]>();
  const dstById = new Map<string, number[]>();
  for (let i = 0; i < src.nodes.length; i++) {
    const id = src.nodes[i].id;
    if (id !== undefined) {
      const arr = srcById.get(id) ?? [];
      arr.push(i);
      srcById.set(id, arr);
    }
  }
  for (let j = 0; j < dst.nodes.length; j++) {
    const id = dst.nodes[j].id;
    if (id !== undefined) {
      const arr = dstById.get(id) ?? [];
      arr.push(j);
      dstById.set(id, arr);
    }
  }
  for (const [id, ts] of dstById) {
    const ss = srcById.get(id);
    if (!ss) continue;
    for (let k = 0; k < Math.min(ss.length, ts.length); k++) {
      if (ss[k] === 0 || ts[k] === 0) continue; // root handled below
      forced[ts[k]] = ss[k];
    }
  }
  forced[0] = 0; // roots are always matched to each other
  return forced;
}

/**
 * Greedy seed matching, used as the incumbent for branch-and-bound and as
 * the guaranteed-complete fallback when the budget interrupts the search.
 *
 *  1. forced pairs (stable ids + roots)
 *  2. exact-subtree hash pass, largest subtrees first
 *  3. top-down pass: children of matched parents pair by pairability
 *  4. sweep: any remaining pairable nodes, preorder x preorder
 */
export function greedyMatch(src: TreeIndex, dst: TreeIndex, forced: number[]): Matching {
  const m: Matching = new Map();
  const usedT = new Array<boolean>(dst.nodes.length).fill(false);
  const usedS = new Array<boolean>(src.nodes.length).fill(false);
  const rev = new Array<number>(dst.nodes.length).fill(-1); // tgtIdx -> srcIdx
  const pair = (s: number, t: number): void => {
    m.set(s, t);
    usedS[s] = true;
    usedT[t] = true;
    rev[t] = s;
  };
  for (let t = 0; t < forced.length; t++) {
    if (forced[t] >= 0 && !usedS[forced[t]] && !usedT[t]) pair(forced[t], t);
  }

  // Hash pass: group equal subtrees, pair largest groups first.
  const srcGroups = new Map<string, number[]>();
  const dstGroups = new Map<string, number[]>();
  for (let i = 0; i < src.nodes.length; i++) {
    const h = subtreeHash(src.nodes[i]);
    const arr = srcGroups.get(h) ?? [];
    arr.push(i);
    srcGroups.set(h, arr);
  }
  for (let j = 0; j < dst.nodes.length; j++) {
    const h = subtreeHash(dst.nodes[j]);
    const arr = dstGroups.get(h) ?? [];
    arr.push(j);
    dstGroups.set(h, arr);
  }
  const hashes = [...srcGroups.keys()].filter((h) => dstGroups.has(h));
  hashes.sort((a, b) => {
    const sa = src.size[srcGroups.get(a)![0]];
    const sb = src.size[srcGroups.get(b)![0]];
    if (sa !== sb) return sb - sa; // largest subtrees first
    return a < b ? -1 : a > b ? 1 : 0; // deterministic tie-break
  });
  for (const h of hashes) {
    const ss = srcGroups.get(h)!;
    const ts = dstGroups.get(h)!;
    let k = 0;
    for (const s of ss) {
      if (usedS[s]) continue;
      while (k < ts.length && usedT[ts[k]]) k++;
      if (k >= ts.length) break;
      pair(s, ts[k]);
    }
  }

  // Top-down pass: match unmatched children of matched parents.
  for (const t of dst.bfs) {
    if (usedT[t]) continue;
    const p = dst.parent[t];
    if (p < 0) continue;
    const sp = rev[p];
    if (sp < 0) continue;
    for (const s of src.childrenIdx[sp]) {
      if (!usedS[s] && pairable(src.nodes[s], dst.nodes[t])) {
        pair(s, t);
        break;
      }
    }
  }

  // Final sweep: pair any remaining compatible nodes in preorder order.
  for (let t = 0; t < dst.nodes.length; t++) {
    if (usedT[t]) continue;
    for (let s = 0; s < src.nodes.length; s++) {
      if (!usedS[s] && pairable(src.nodes[s], dst.nodes[t])) {
        pair(s, t);
        break;
      }
    }
  }
  return m;
}
