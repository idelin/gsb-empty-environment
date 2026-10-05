import type { Op, CostModel, Path } from './types.ts';
import type { TreeIndex } from './tree.ts';
import type { Matching } from './match.ts';
import { normalizeMatching } from './normalize.ts';

/**
 * Edit-script generation.
 *
 * Given a matching, produces a complete, ordered edit script that transforms
 * the source tree into the target tree. The matching is first normalized so
 * that no move can create an ancestor cycle. Generation follows a
 * Chawathe-style pass: breadth-first over the target, inserting unmatched
 * nodes, updating and re-parenting matched ones, aligning sibling order via
 * a longest-common-subsequence step, and finally deleting leftover source
 * subtrees.
 *
 * Op paths always refer to the evolving tree, with this convention for move:
 * first detach the node at `from`, then resolve `parent` in the resulting
 * tree and insert at `index`. The applier (apply.ts) mirrors it exactly.
 */

interface WNode {
  type: string;
  value?: string;
  id?: string;
  children: WNode[];
  parent: WNode | null;
  /** >= 0: preorder index of the originating source node; -1: inserted. */
  sIdx: number;
  /** For inserted nodes: the target preorder index they correspond to. */
  tRef: number;
}

export interface GeneratedScript {
  ops: Op[];
  cost: number;
  /** The normalized matching actually realized by the script. */
  matching: Matching;
}

export function generateScript(
  matching: Matching,
  src: TreeIndex,
  dst: TreeIndex,
  forced: number[],
  costs: CostModel,
): GeneratedScript {
  const m = normalizeMatching(matching, src, dst, forced);

  const partnerT = new Array<number>(src.nodes.length).fill(-1);
  const partnerS = new Array<number>(dst.nodes.length).fill(-1);
  for (const [s, t] of m) {
    partnerT[s] = t;
    partnerS[t] = s;
  }

  // Working tree: a mutable copy of the source.
  const wByS: WNode[] = new Array(src.nodes.length);
  const build = (sIdx: number, parent: WNode | null): WNode => {
    const n = src.nodes[sIdx];
    const w: WNode = { type: n.type, children: [], parent, sIdx, tRef: -1 };
    if (n.value !== undefined) w.value = n.value;
    if (n.id !== undefined) w.id = n.id;
    wByS[sIdx] = w;
    for (const c of src.childrenIdx[sIdx]) w.children.push(build(c, w));
    return w;
  };
  const rootW = build(0, null);

  const wByT: (WNode | null)[] = new Array(dst.nodes.length).fill(null);
  const ops: Op[] = [];
  let cost = 0;

  const pathOf = (w: WNode): Path => {
    const path: number[] = [];
    let cur: WNode | null = w;
    while (cur !== null && cur.parent !== null) {
      path.push(cur.parent.children.indexOf(cur));
      cur = cur.parent;
    }
    return path.reverse();
  };

  /** Target correspondent of a working-tree node, if any. */
  const tOf = (w: WNode): number => {
    if (w.sIdx >= 0) return partnerT[w.sIdx];
    return w.tRef;
  };

  const detach = (w: WNode): void => {
    const p = w.parent;
    if (p === null) throw new Error('internal: cannot detach root');
    p.children.splice(p.children.indexOf(w), 1);
    w.parent = null;
  };

  const insertChild = (parentW: WNode, k: number, w: WNode): void => {
    parentW.children.splice(k, 0, w);
    w.parent = parentW;
  };

  /**
   * Index at which the node for target `tIdx` belongs under `parentW`:
   * right after the last current child whose target correspondent is an
   * older sibling of `tIdx`. Siblings are visited in target order, so every
   * older sibling's correspondent is already present.
   */
  const insertIndex = (parentW: WNode, tIdx: number): number => {
    const p = dst.parent[tIdx];
    const pos = dst.childPos[tIdx];
    let idx = 0;
    for (let i = 0; i < parentW.children.length; i++) {
      const u = tOf(parentW.children[i]);
      if (u >= 0 && dst.parent[u] === p && dst.childPos[u] < pos) idx = i + 1;
    }
    return idx;
  };

  const align = (w: WNode, tIdx: number): void => {
    // Pairs (source child of w, target child of tIdx) currently co-located.
    const aOrder: number[] = []; // sIdx in working-tree order
    for (const c of w.children) {
      if (c.sIdx >= 0) {
        const u = partnerT[c.sIdx];
        if (u >= 0 && dst.parent[u] === tIdx) aOrder.push(c.sIdx);
      }
    }
    const bOrder: number[] = []; // sIdx in target order
    for (const u of dst.childrenIdx[tIdx]) {
      const s = partnerS[u];
      if (s >= 0 && wByS[s].parent === w) bOrder.push(s);
    }
    if (aOrder.length <= 1) return;
    const keep = lcsSet(aOrder, bOrder);
    let lastPlaced: WNode | null = null;
    for (const s of bOrder) {
      const c = wByS[s];
      if (keep.has(s)) {
        lastPlaced = c;
        continue;
      }
      const from = pathOf(c);
      detach(c);
      const k = lastPlaced === null ? 0 : w.children.indexOf(lastPlaced) + 1;
      insertChild(w, k, c);
      ops.push({ op: 'move', from, parent: pathOf(w), index: k });
      cost += costs.move;
      lastPlaced = c;
    }
  };

  for (const tIdx of dst.bfs) {
    const p = dst.parent[tIdx];
    const t = dst.nodes[tIdx];
    const sIdx = partnerS[tIdx];
    if (sIdx < 0) {
      // Unmatched target node: insert a shallow copy; children follow.
      const parentW = wByT[p];
      if (parentW === null || parentW === undefined) {
        throw new Error('internal: insert target has no correspondent parent');
      }
      const k = insertIndex(parentW, tIdx);
      const w: WNode = { type: t.type, children: [], parent: parentW, sIdx: -1, tRef: tIdx };
      if (t.value !== undefined) w.value = t.value;
      if (t.id !== undefined) w.id = t.id;
      parentW.children.splice(k, 0, w);
      const node: TreeNode = { type: t.type, children: [] };
      if (t.value !== undefined) node.value = t.value;
      if (t.id !== undefined) node.id = t.id;
      ops.push({ op: 'insert', parent: pathOf(parentW), index: k, node });
      cost += costs.insert;
      wByT[tIdx] = w;
      continue;
    }

    const w = wByS[sIdx];
    if (w.type !== t.type || w.value !== t.value || w.id !== t.id) {
      ops.push({ op: 'update', path: pathOf(w), type: t.type, value: t.value, id: t.id });
      w.type = t.type;
      if (t.value !== undefined) w.value = t.value; else delete w.value;
      if (t.id !== undefined) w.id = t.id; else delete w.id;
      cost += costs.update;
    }
    if (p >= 0) {
      const shouldParent = wByT[p];
      if (shouldParent !== null && w.parent !== shouldParent) {
        // Cycle safety: normalization guarantees shouldParent is not w nor
        // inside w's subtree.
        const from = pathOf(w);
        const k = insertIndex(shouldParent, tIdx);
        detach(w);
        insertChild(shouldParent, k, w);
        ops.push({ op: 'move', from, parent: pathOf(shouldParent), index: k });
        cost += costs.move;
      }
    }
    wByT[tIdx] = w;
    align(w, tIdx);
  }

  // Delete leftover unmatched source subtrees (topmost first; matched
  // descendants were moved out during the main pass).
  const countW = (w: WNode): number => {
    let n = 1;
    for (const c of w.children) n += countW(c);
    return n;
  };
  const sweep = (w: WNode): void => {
    for (const c of [...w.children]) {
      if (c.sIdx >= 0 && partnerT[c.sIdx] < 0) {
        ops.push({ op: 'delete', path: pathOf(c) });
        cost += costs.delete * countW(c);
        detach(c);
      } else {
        sweep(c);
      }
    }
  };
  sweep(rootW);

  return { ops, cost, matching: m };
}

/** Longest common subsequence of two index sequences; returns the set of
 *  elements kept (members of the LCS). Deterministic tie-breaking. */
function lcsSet(a: number[], b: number[]): Set<number> {
  const n = a.length;
  const m = b.length;
  // dp[i][j] = LCS length of a[i..] and b[j..]
  const dp: number[][] = [];
  for (let i = 0; i <= n; i++) dp.push(new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const keep = new Set<number>();
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      keep.add(a[i]);
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      i++;
    } else {
      j++;
    }
  }
  return keep;
}
