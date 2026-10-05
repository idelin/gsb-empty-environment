import {
  DiffOptions,
  DiffResult,
  DiffStats,
  PatchOp,
  TreeNode,
  TreeValue,
  UpdateOp,
} from './types';

export class TreeDiffError extends Error {}

/** Thrown internally when the matching budget is exhausted. */
class BudgetExceeded extends Error {}

const INF = Number.MAX_SAFE_INTEGER / 4;

interface Ctx {
  budget: number;
}

function spend(ctx: Ctx, n: number): void {
  if (ctx.budget === Infinity) return;
  ctx.budget -= n;
  if (ctx.budget < 0) throw new BudgetExceeded();
}

interface TreeInfo {
  parent: Map<TreeNode, TreeNode>;
  size: Map<TreeNode, number>;
  tin: Map<TreeNode, number>;
  tout: Map<TreeNode, number>;
  preorder: TreeNode[];
  ids: Map<string, TreeNode>;
}

function validateNodeShape(node: TreeNode, label: string, path: string): void {
  if (typeof node !== 'object' || node === null || Array.isArray(node)) {
    throw new TreeDiffError(label + ': node at ' + path + ' is not an object');
  }
  if (typeof node.type !== 'string') {
    throw new TreeDiffError(label + ': node at ' + path + ' has a non-string type');
  }
  if (node.id !== undefined && typeof node.id !== 'string') {
    throw new TreeDiffError(label + ': node at ' + path + ' has a non-string id');
  }
  if (node.value !== undefined) {
    const t = typeof node.value;
    if (!(t === 'string' || t === 'number' || t === 'boolean' || node.value === null)) {
      throw new TreeDiffError(label + ': node at ' + path + ' has a non-scalar value');
    }
  }
  if (!Array.isArray(node.children)) {
    throw new TreeDiffError(label + ': node at ' + path + ' has non-array children');
  }
}

function analyze(root: TreeNode, label: string): TreeInfo {
  const parent = new Map<TreeNode, TreeNode>();
  const size = new Map<TreeNode, number>();
  const tin = new Map<TreeNode, number>();
  const tout = new Map<TreeNode, number>();
  const preorder: TreeNode[] = [];
  const ids = new Map<string, TreeNode>();
  let clock = 0;
  interface Frame {
    node: TreeNode;
    p: TreeNode | null;
    entered: boolean;
  }
  const stack: Frame[] = [{ node: root, p: null, entered: false }];
  while (stack.length > 0) {
    const top = stack[stack.length - 1];
    if (!top.entered) {
      const node = top.node;
      validateNodeShape(node, label, '[' + preorder.map(() => '.').join(',') + ']');
      if (top.p !== null) parent.set(node, top.p);
      tin.set(node, clock++);
      preorder.push(node);
      if (node.id !== undefined) {
        if (ids.has(node.id)) {
          throw new TreeDiffError(label + ': duplicate id "' + node.id + '"');
        }
        ids.set(node.id, node);
      }
      top.entered = true;
      for (let i = node.children.length - 1; i >= 0; i--) {
        stack.push({ node: node.children[i], p: node, entered: false });
      }
    } else {
      stack.pop();
      const node = top.node;
      tout.set(node, clock++);
      let s = 1;
      for (const c of node.children) s += size.get(c) as number;
      size.set(node, s);
    }
  }
  return { parent, size, tin, tout, preorder, ids };
}

function valuesEqual(a: TreeValue | undefined, b: TreeValue | undefined): boolean {
  return Object.is(a, b);
}

/**
 * Computes a minimal-cost sequential patch transforming `oldTree` into `newTree`.
 *
 * Matching strategy:
 *  1. Nodes with equal stable `id` are paired first.
 *  2. The roots are always paired with each other.
 *  3. Remaining nodes are paired top-down: for every matched pair, the two child
 *     sequences are aligned with a min-cost DP (update cost 0/1/2 per node,
 *     insert/delete cost = subtree size, matched-elsewhere nodes move for free).
 *  4. Pairs whose matched ancestors do not form a closed chain to the root are
 *     dropped; those nodes fall back to delete+insert. (Closure is exactly what
 *     makes move generation cycle-free: when a subtree is relocated, the target
 *     parent is already placed, and its ancestor chain leads to the root, which
 *     can never lie inside the moved subtree.)
 *
 * Ties in the DP are broken canonically (diagonal > delete > insert, backtracked
 * from the end), so equal-cost instances always yield the same patch.
 */
export function diffTrees(oldTree: TreeNode, newTree: TreeNode, options?: DiffOptions): DiffResult {
  const opts = options || {};
  const infoA = analyze(oldTree, 'oldTree');
  const infoB = analyze(newTree, 'newTree');
  const ctx: Ctx = { budget: opts.budget === undefined ? Infinity : Math.max(0, opts.budget) };
  let optimal = true;

  const matchA = new Map<TreeNode, TreeNode>();
  const matchB = new Map<TreeNode, TreeNode>();
  const pair = (a: TreeNode, b: TreeNode): void => {
    matchA.set(a, b);
    matchB.set(b, a);
  };
  const unpair = (a: TreeNode, b: TreeNode): void => {
    matchA.delete(a);
    matchB.delete(b);
  };

  const aligned = new Set<TreeNode>();
  const queue: Array<[TreeNode, TreeNode]> = [];
  let qhead = 0;

  const forceRootPair = (): void => {
    const ma = matchA.get(oldTree);
    if (ma === newTree) return;
    if (ma !== undefined) unpair(oldTree, ma);
    const mb = matchB.get(newTree);
    if (mb !== undefined) unpair(mb, newTree);
    pair(oldTree, newTree);
  };

  const alignChildren = (a: TreeNode, b: TreeNode): void => {
    const acs = a.children;
    const bcs = b.children;
    const n = acs.length;
    const m = bcs.length;
    if (n === 0 || m === 0) return;
    spend(ctx, 1);
    const subCost = (i: number, j: number): number => {
      const ac = acs[i];
      const bc = bcs[j];
      const ma = matchA.get(ac);
      if (ma !== undefined) return ma === bc ? 0 : INF;
      if (matchB.has(bc)) return INF;
      let c = 0;
      if (ac.type !== bc.type) c += 1;
      if (!valuesEqual(ac.value, bc.value)) c += 1;
      return c;
    };
    const delCost = (i: number): number => (matchA.has(acs[i]) ? 0 : (infoA.size.get(acs[i]) as number));
    const insCost = (j: number): number => (matchB.has(bcs[j]) ? 0 : (infoB.size.get(bcs[j]) as number));
    const width = m + 1;
    const dp: number[] = new Array((n + 1) * width);
    const at = (i: number, j: number): number => dp[i * width + j];
    const set = (i: number, j: number, v: number): void => {
      dp[i * width + j] = v;
    };
    set(0, 0, 0);
    for (let i = 1; i <= n; i++) set(i, 0, at(i - 1, 0) + delCost(i - 1));
    for (let j = 1; j <= m; j++) set(0, j, at(0, j - 1) + insCost(j - 1));
    for (let i = 1; i <= n; i++) {
      for (let j = 1; j <= m; j++) {
        spend(ctx, 1);
        const s = at(i - 1, j - 1) + subCost(i - 1, j - 1);
        const d = at(i - 1, j) + delCost(i - 1);
        const ins = at(i, j - 1) + insCost(j - 1);
        set(i, j, Math.min(s, Math.min(d, ins)));
      }
    }
    let i = n;
    let j = m;
    while (i > 0 || j > 0) {
      if (i > 0 && j > 0) {
        const sc = subCost(i - 1, j - 1);
        if (sc < INF && at(i, j) === at(i - 1, j - 1) + sc) {
          const ac = acs[i - 1];
          const bc = bcs[j - 1];
          if (matchA.get(ac) === undefined && matchB.get(bc) === undefined) {
            pair(ac, bc);
            queue.push([ac, bc]);
          } else if (matchA.get(ac) === bc) {
            queue.push([ac, bc]);
          }
          i--;
          j--;
          continue;
        }
      }
      if (i > 0 && (j === 0 || at(i, j) === at(i - 1, j) + delCost(i - 1))) {
        i--;
        continue;
      }
      j--;
    }
  };

  try {
    infoA.ids.forEach((a, id) => {
      const b = infoB.ids.get(id);
      if (b !== undefined) pair(a, b);
    });
    forceRootPair();
    queue.push([oldTree, newTree]);
    for (const a of infoA.preorder) {
      if (a === oldTree) continue;
      const b = matchA.get(a);
      if (b !== undefined) queue.push([a, b]);
    }
    while (qhead < queue.length) {
      const item = queue[qhead++];
      const a = item[0];
      const b = item[1];
      if (aligned.has(a)) continue;
      if (matchA.get(a) !== b) continue;
      aligned.add(a);
      alignChildren(a, b);
    }
  } catch (e) {
    if (e instanceof BudgetExceeded) {
      optimal = false;
    } else {
      throw e;
    }
  }

  // Keep only pairs whose ancestors are all matched (closure to the root).
  // This is exactly the condition that makes every emitted move cycle-free.
  let changed = true;
  while (changed) {
    changed = false;
    for (const a of infoA.preorder) {
      const b = matchA.get(a);
      if (b === undefined || a === oldTree) continue;
      let ok = true;
      let p: TreeNode | undefined = infoA.parent.get(a);
      while (p !== undefined) {
        if (!matchA.has(p)) {
          ok = false;
          break;
        }
        p = infoA.parent.get(p);
      }
      if (ok) {
        let q: TreeNode | undefined = infoB.parent.get(b);
        while (q !== undefined) {
          if (!matchB.has(q)) {
            ok = false;
            break;
          }
          q = infoB.parent.get(q);
        }
      }
      if (!ok) {
        unpair(a, b);
        changed = true;
      }
    }
  }

  // ---- patch generation against a mutable working clone of oldTree ----
  interface WNode {
    type: string;
    value?: TreeValue;
    id?: string;
    children: WNode[];
    parent: WNode | null;
    src: TreeNode | null;
  }
  const wOf = new Map<TreeNode, WNode>();
  const buildWorking = (a: TreeNode, p: WNode | null, register: boolean): WNode => {
    const w: WNode = { type: a.type, children: [], parent: p, src: register ? a : null };
    if (a.value !== undefined) w.value = a.value;
    if (a.id !== undefined) w.id = a.id;
    if (register) wOf.set(a, w);
    w.children = a.children.map((c) => buildWorking(c, w, register));
    return w;
  };
  buildWorking(oldTree, null, true);

  const cloneData = (b: TreeNode): TreeNode => {
    const node: TreeNode = { type: b.type, children: b.children.map(cloneData) };
    if (b.value !== undefined) node.value = b.value;
    if (b.id !== undefined) node.id = b.id;
    return node;
  };

  const pathOf = (w: WNode): number[] => {
    const parts: number[] = [];
    let cur: WNode | null = w;
    while (cur !== null && cur.parent !== null) {
      const pr: WNode = cur.parent;
      parts.push(pr.children.indexOf(cur));
      cur = pr;
    }
    return parts.reverse();
  };

  const ops: PatchOp[] = [];

  const generate = (a: TreeNode, b: TreeNode): void => {
    const wa = wOf.get(a) as WNode;
    if (a.type !== b.type || !valuesEqual(a.value, b.value) || a.id !== b.id) {
      const op: UpdateOp = { op: 'update', path: pathOf(wa), type: b.type };
      if (b.value !== undefined) op.value = b.value;
      if (b.id !== undefined) op.id = b.id;
      ops.push(op);
      wa.type = b.type;
      if (b.value !== undefined) wa.value = b.value;
      else delete wa.value;
      if (b.id !== undefined) wa.id = b.id;
      else delete wa.id;
    }
    const targets = b.children;
    const L = wa.children;
    // Remove unmatched children first so placement never moves a node
    // around a sibling that is about to be deleted.
    for (let k = L.length - 1; k >= 0; k--) {
      const w = L[k];
      if (w.src !== null && matchA.has(w.src)) continue;
      ops.push({ op: 'delete', path: pathOf(w) });
      L.splice(k, 1);
    }
    for (let i = 0; i < targets.length; i++) {
      const bj = targets[i];
      const aj = matchB.get(bj);
      if (aj === undefined) {
        ops.push({ op: 'insert', parentPath: pathOf(wa), index: i, node: cloneData(bj) });
        const w = buildWorking(bj, wa, false);
        L.splice(i, 0, w);
        continue;
      }
      const wj = wOf.get(aj) as WNode;
      if (L[i] === wj) continue;
      let cur: WNode | null = wa;
      while (cur !== null) {
        if (cur === wj) throw new TreeDiffError('internal error: move would create an ancestor cycle');
        cur = cur.parent;
      }
      ops.push({ op: 'move', from: pathOf(wj), toParentPath: pathOf(wa), toIndex: i });
      const oldParent = wj.parent as WNode;
      oldParent.children.splice(oldParent.children.indexOf(wj), 1);
      L.splice(i, 0, wj);
      wj.parent = wa;
    }
    // Nodes beyond the target length are matched elsewhere and stay put
    // until their destination parent moves them out.
    for (const bj of targets) {
      const aj = matchB.get(bj);
      if (aj !== undefined) generate(aj, bj);
    }
  };
  generate(oldTree, newTree);

  const stats: DiffStats = { matched: matchA.size, updated: 0, inserted: 0, deleted: 0, moved: 0 };
  for (const op of ops) {
    if (op.op === 'update') stats.updated++;
    else if (op.op === 'insert') stats.inserted++;
    else if (op.op === 'delete') stats.deleted++;
    else stats.moved++;
  }
  return { ops, optimal, stats };
}
