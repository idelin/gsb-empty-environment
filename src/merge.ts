/**
 * Three-way merge of two independent branches over the same base.
 *
 * Identity model: base nodes keep b#<n>; new nodes are <branch>:<opIndex>:<k>.
 * Each branch's result is an identity tree, so the merger distinguishes
 * repeated equal values and tracks array insert/remove/move by identity
 * rather than by numeric indices.
 */

import { deepEqual, type Json } from './json.ts';
import { apply, type Op } from './patch.ts';
import {
  baseIdOrdinal,
  buildBaseTree,
  cloneINode,
  collectIds,
  detach,
  findById,
  isBaseId,
  materialize,
  type INode,
  type NodeId,
} from './idtree.ts';
import { applyBranch, type BranchId, type OpLog } from './branch.ts';
import { generatePatch } from './rebasegen.ts';

export interface OpRef {
  branch: BranchId;
  opIndex: number;
  op: Op;
}

export interface CycleEdge {
  from: NodeId;
  to: NodeId;
  /** Ops responsible for this constraint; [] means preserved base order. */
  sources: OpRef[];
}

export type ConflictType =
  | 'value-conflict'
  | 'delete-modify'
  | 'move-conflict'
  | 'key-conflict'
  | 'order-cycle';

export interface MergeConflict {
  type: ConflictType;
  /** Stable-sorted involved node ids (base ids by ordinal first). */
  nodes: NodeId[];
  left: OpRef[];
  right: OpRef[];
  /** Present for order-cycle: the directed cycle with branch op provenance. */
  cycle?: CycleEdge[];
}

export type MergeResult =
  | {
      ok: true;
      merged: Json;
      rebasedLeft: Op[];
      rebasedRight: Op[];
      inverseLeft: Op[];
      inverseRight: Op[];
    }
  | { ok: false; conflicts: MergeConflict[] };

interface InsertInfo {
  parentId: NodeId | null;
  key: string | null;
  node: INode;
  opIndex: number;
  replacesId: NodeId | null;
}

interface MoveInfo {
  destParentId: NodeId | null;
  destKey: string | null;
  intoArray: boolean;
  opIndex: number;
}

interface BranchInfo {
  id: BranchId;
  root: INode;
  log: OpLog[];
  deleted: Set<NodeId>;
  /** base id -> first op that removed it */
  deleteOp: Map<NodeId, number>;
  replaced: Map<NodeId, { value: Json; opIndex: number }>;
  moves: Map<NodeId, MoveInfo>;
  inserts: InsertInfo[];
  slots: Map<string, { nodeId: NodeId; opIndex: number; replacesId: NodeId | null }>;
}

const ROOT_SLOT = '\0ROOT';

function refOf(b: BranchInfo, opIndex: number): OpRef {
  return { branch: b.id, opIndex, op: b.log[opIndex]!.op };
}

function sortIds(ids: Iterable<NodeId>): NodeId[] {
  return [...new Set(ids)].sort((a, b) => {
    const ba = isBaseId(a);
    const bb = isBaseId(b);
    if (ba && bb) return baseIdOrdinal(a) - baseIdOrdinal(b);
    if (ba !== bb) return ba ? -1 : 1;
    return a < b ? -1 : a > b ? 1 : 0;
  });
}

function analyze(baseRoot: INode, ops: readonly Op[], id: BranchId): BranchInfo {
  const applied = applyBranch(baseRoot, ops, id);
  const resultIds = collectIds(applied.root);
  const baseIds = collectIds(baseRoot);

  const deleted = new Set<NodeId>();
  for (const bid of baseIds) if (!resultIds.has(bid)) deleted.add(bid);

  const deleteOp = new Map<NodeId, number>();
  for (const e of applied.log) {
    for (const rid of e.removedIds) {
      if (isBaseId(rid) && !deleteOp.has(rid)) deleteOp.set(rid, e.opIndex);
    }
  }

  const createdOp = new Map<NodeId, number>();
  for (const e of applied.log) {
    if (e.createdRootId !== null) createdOp.set(e.createdRootId, e.opIndex);
  }

  // Insert roots: non-base nodes whose result parent is a base node or root.
  const inserts: InsertInfo[] = [];
  const walk = (node: INode, parent: INode | null, key: string | number | null): void => {
    if (!isBaseId(node.id)) {
      if (parent === null || isBaseId(parent.id)) {
        const opIndex = createdOp.get(node.id);
        if (opIndex !== undefined) {
          let replacesId: NodeId | null = null;
          let destKey: string | null = null;
          if (parent && parent.kind === 'obj' && typeof key === 'string') {
            destKey = key;
            const baseLoc = findById(baseRoot, parent.id);
            if (baseLoc && baseLoc.node.kind === 'obj') {
              const old = baseLoc.node.children.get(key);
              if (old) replacesId = old.id;
            }
          }
          inserts.push({
            parentId: parent ? parent.id : null,
            key: destKey,
            node,
            opIndex,
            replacesId,
          });
        }
        return; // nested new nodes are carried with this root
      }
    }
    if (node.kind === 'arr') node.items.forEach((c, i) => walk(c, node, i));
    if (node.kind === 'obj') for (const [k, c] of node.children) walk(c, node, k);
  };
  walk(applied.root, null, null);

  const replaced = new Map<NodeId, { value: Json; opIndex: number }>();
  const moves = new Map<NodeId, MoveInfo>();
  for (const e of applied.log) {
    if (e.replacedId !== null && isBaseId(e.replacedId) && deleted.has(e.replacedId)) {
      // Record only if the replacement node survived the whole branch.
      if (e.createdRootId !== null && resultIds.has(e.createdRootId)) {
        const loc = findById(applied.root, e.createdRootId)!;
        replaced.set(e.replacedId, { value: materialize(loc.node), opIndex: e.opIndex });
      }
    }
    if (e.movedId !== null && isBaseId(e.movedId) && resultIds.has(e.movedId)) {
      const loc = findById(applied.root, e.movedId)!;
      moves.set(e.movedId, {
        destParentId: loc.parent ? loc.parent.id : null,
        destKey: loc.parent && loc.parent.kind === 'obj' ? (loc.key as string) : null,
        intoArray: loc.parent !== null && loc.parent.kind === 'arr',
        opIndex: e.opIndex,
      });
    }
  }

  // Slots: object-key destinations and the root destination.
  const slots = new Map<string, { nodeId: NodeId; opIndex: number; replacesId: NodeId | null }>();
  const claim = (
    parentId: NodeId | null,
    key: string | null,
    nodeId: NodeId,
    opIndex: number,
    replacesId: NodeId | null,
  ): void => {
    slots.set(parentId === null ? ROOT_SLOT : `${parentId}\0${key}`, { nodeId, opIndex, replacesId });
  };
  for (const ins of inserts) {
    if (ins.parentId === null || ins.key !== null) {
      claim(ins.parentId, ins.key, ins.node.id, ins.opIndex, ins.replacesId);
    }
  }
  for (const [nodeId, mv] of moves) {
    if (!mv.intoArray) claim(mv.destParentId, mv.destKey, nodeId, mv.opIndex, null);
  }

  return { id, root: applied.root, log: applied.log, deleted, deleteOp, replaced, moves, inserts, slots };
}

function mkConflict(
  type: ConflictType,
  nodes: NodeId[],
  left: OpRef[],
  right: OpRef[],
  cycle?: CycleEdge[],
): MergeConflict {
  const c: MergeConflict = { type, nodes: sortIds(nodes), left, right };
  if (cycle) c.cycle = cycle;
  return c;
}

function detectConflicts(L: BranchInfo, R: BranchInfo, baseIds: Set<NodeId>): MergeConflict[] {
  const conflicts: MergeConflict[] = [];
  const delRef = (b: BranchInfo, id: NodeId): OpRef[] => {
    const opIndex = b.deleteOp.get(id);
    return opIndex === undefined ? [] : [refOf(b, opIndex)];
  };

  for (const bid of sortIds(baseIds)) {
    const lRep = L.replaced.get(bid);
    const rRep = R.replaced.get(bid);
    const lMov = L.moves.get(bid);
    const rMov = R.moves.get(bid);
    const lDel = L.deleted.has(bid);
    const rDel = R.deleted.has(bid);

    if (lRep && rRep) {
      if (!deepEqual(lRep.value, rRep.value)) {
        conflicts.push(
          mkConflict('value-conflict', [bid], [refOf(L, lRep.opIndex)], [refOf(R, rRep.opIndex)]),
        );
      }
      continue;
    }
    if (lMov && rMov) {
      const sameDest =
        lMov.destParentId === rMov.destParentId &&
        (lMov.intoArray || rMov.intoArray ? lMov.intoArray && rMov.intoArray : lMov.destKey === rMov.destKey);
      if (!sameDest) {
        conflicts.push(
          mkConflict('move-conflict', [bid], [refOf(L, lMov.opIndex)], [refOf(R, rMov.opIndex)]),
        );
      }
      continue;
    }
    if ((lRep && rMov) || (rRep && lMov)) {
      const lRefs = lRep ? [refOf(L, lRep.opIndex)] : [refOf(L, lMov!.opIndex)];
      const rRefs = rRep ? [refOf(R, rRep.opIndex)] : [refOf(R, rMov!.opIndex)];
      conflicts.push(mkConflict('delete-modify', [bid], lRefs, rRefs));
      continue;
    }
    if (lDel && (rRep || rMov)) {
      const rRefs = rRep ? [refOf(R, rRep.opIndex)] : [refOf(R, rMov!.opIndex)];
      conflicts.push(mkConflict('delete-modify', [bid], delRef(L, bid), rRefs));
      continue;
    }
    if (rDel && (lRep || lMov)) {
      const lRefs = lRep ? [refOf(L, lRep.opIndex)] : [refOf(L, lMov!.opIndex)];
      conflicts.push(mkConflict('delete-modify', [bid], lRefs, delRef(R, bid)));
      continue;
    }
  }

  // Object-key / root slot collisions.
  for (const [slotKey, lClaim] of L.slots) {
    const rClaim = R.slots.get(slotKey);
    if (!rClaim || lClaim.nodeId === rClaim.nodeId) continue;
    if (lClaim.replacesId !== null && lClaim.replacesId === rClaim.replacesId) {
      continue; // same base child replaced twice: handled as value-conflict/agreement
    }
    if (lClaim.replacesId === null && rClaim.replacesId === null) {
      const li = L.inserts.find((x) => x.node.id === lClaim.nodeId);
      const ri = R.inserts.find((x) => x.node.id === rClaim.nodeId);
      if (li && ri && deepEqual(materialize(li.node), materialize(ri.node))) continue;
    }
    conflicts.push(
      mkConflict(
        'key-conflict',
        [lClaim.nodeId, rClaim.nodeId],
        [refOf(L, lClaim.opIndex)],
        [refOf(R, rClaim.opIndex)],
      ),
    );
    void slotKey;
  }

  // Insert/move into a container deleted by the other branch.
  const checkDeletedParent = (branch: BranchInfo, other: BranchInfo): void => {
    const mine = (opIndex: number): OpRef[] => [refOf(branch, opIndex)];
    const wrap = (nodes: NodeId[], opIndex: number): void => {
      conflicts.push(
        mkConflict(
          'delete-modify',
          nodes,
          branch.id === 'L' ? mine(opIndex) : [],
          branch.id === 'R' ? mine(opIndex) : [],
        ),
      );
    };
    for (const ins of branch.inserts) {
      if (ins.parentId !== null && isBaseId(ins.parentId) && other.deleted.has(ins.parentId)) {
        wrap([ins.parentId], ins.opIndex);
      }
    }
    for (const mv of branch.moves.values()) {
      if (mv.destParentId !== null && isBaseId(mv.destParentId) && other.deleted.has(mv.destParentId)) {
        wrap([mv.destParentId], mv.opIndex);
      }
    }
  };
  checkDeletedParent(L, R);
  checkDeletedParent(R, L);

  return conflicts;
}

/** Clone an insert-root subtree, omitting base-id descendants (placed by moves). */
function clonePruningBase(node: INode): INode {
  if (node.kind === 'prim') return { kind: 'prim', id: node.id, value: node.value };
  if (node.kind === 'arr') {
    const items: INode[] = [];
    for (const it of node.items) if (!isBaseId(it.id)) items.push(clonePruningBase(it));
    return { kind: 'arr', id: node.id, items };
  }
  const children = new Map<string, INode>();
  for (const [k, v] of node.children) {
    if (!isBaseId(v.id)) children.set(k, clonePruningBase(v));
  }
  return { kind: 'obj', id: node.id, children };
}

interface OrderEdge {
  from: NodeId;
  to: NodeId;
  sources: OpRef[];
}

function branchPlacing(b: BranchInfo, id: NodeId): number | null {
  const ins = b.inserts.find((i) => i.node.id === id);
  if (ins) return ins.opIndex;
  const mv = b.moves.get(id);
  if (mv) return mv.opIndex;
  return null;
}

function edgeSources(b: BranchInfo, u: NodeId, v: NodeId): OpRef[] {
  const out: OpRef[] = [];
  for (const id of [u, v]) {
    const opIndex = branchPlacing(b, id);
    if (opIndex !== null && !out.some((r) => r.opIndex === opIndex)) out.push(refOf(b, opIndex));
  }
  return out;
}

/** Deterministic tie-break key: base position, then (branch, opIndex). */
function sortKeyOf(id: NodeId, baseIndex: Map<NodeId, number>, L: BranchInfo, R: BranchInfo): (number | string)[] {
  const bi = baseIndex.get(id);
  if (bi !== undefined) return [0, bi];
  const lp = branchPlacing(L, id);
  if (lp !== null) return [1, 0, lp, id];
  const rp = branchPlacing(R, id);
  if (rp !== null) return [1, 1, rp, id];
  return [2, 0, 0, id];
}

function cmpKeys(a: (number | string)[], b: (number | string)[]): number {
  for (let i = 0; i < Math.min(a.length, b.length); i++) {
    const x = a[i]!;
    const y = b[i]!;
    if (x === y) continue;
    if (typeof x === 'number' && typeof y === 'number') return x - y;
    return String(x) < String(y) ? -1 : 1;
  }
  return a.length - b.length;
}

function seqOf(root: INode, arrId: NodeId, inSet: Set<NodeId>): NodeId[] | null {
  const loc = findById(root, arrId);
  if (!loc || loc.node.kind !== 'arr') return null;
  return loc.node.items.map((n) => n.id).filter((id) => inSet.has(id));
}

type Opinion = { first: NodeId; second: NodeId };

function opinions(seq: NodeId[] | null): Map<string, Opinion> {
  const map = new Map<string, Opinion>();
  if (!seq) return map;
  for (let i = 0; i + 1 < seq.length; i++) {
    const first = seq[i]!;
    const second = seq[i + 1]!;
    const key = first < second ? `${first}${second}` : `${second}${first}`;
    map.set(key, { first, second });
  }
  return map;
}

function sameDir(a: Opinion, b: Opinion): boolean {
  return a.first === b.first && a.second === b.second;
}

/**
 * Order one array of the merged tree by combining per-branch adjacency
 * opinions with base order into a directed constraint graph. Returns an
 * order-cycle conflict (with a witness cycle) when constraints are cyclic.
 */
function orderArray(
  arr: INode & { kind: 'arr' },
  L: BranchInfo,
  R: BranchInfo,
  baseRoot: INode,
): MergeConflict | null {
  const inSet = new Set(arr.items.map((n) => n.id));
  const seqL = seqOf(L.root, arr.id, inSet);
  const seqR = seqOf(R.root, arr.id, inSet);
  let seqBase: NodeId[] | null = null;
  const baseIndex = new Map<NodeId, number>();
  if (isBaseId(arr.id)) {
    seqBase = seqOf(baseRoot, arr.id, inSet);
    const full = findById(baseRoot, arr.id);
    if (full && full.node.kind === 'arr') {
      full.node.items.forEach((n, i) => baseIndex.set(n.id, i));
    }
  }
  const lOp = opinions(seqL);
  const rOp = opinions(seqR);
  const bOp = opinions(seqBase);

  const edges: OrderEdge[] = [];
  const keys = new Set([...lOp.keys(), ...rOp.keys(), ...bOp.keys()]);
  for (const key of keys) {
    const l = lOp.get(key);
    const r = rOp.get(key);
    const b = bOp.get(key);
    if (l && r) {
      if (sameDir(l, r)) {
        edges.push({ from: l.first, to: l.second, sources: [...edgeSources(L, l.first, l.second), ...edgeSources(R, l.first, l.second)] });
      } else if (b && sameDir(l, b)) {
        edges.push({ from: r.first, to: r.second, sources: edgeSources(R, r.first, r.second) });
      } else if (b && sameDir(r, b)) {
        edges.push({ from: l.first, to: l.second, sources: edgeSources(L, l.first, l.second) });
      } else {
        // Direct contradiction between branches: a 2-cycle witness.
        const cycle: CycleEdge[] = [
          { from: l.first, to: l.second, sources: edgeSources(L, l.first, l.second) },
          { from: r.first, to: r.second, sources: edgeSources(R, r.first, r.second) },
        ];
        return mkConflict('order-cycle', [l.first, l.second, r.first, r.second], cycle[0]!.sources.filter((s) => s.branch === 'L'), cycle[1]!.sources.filter((s) => s.branch === 'R'), cycle);
      }
    } else {
      const o = l ?? r ?? b!;
      const branch = l ? L : r ? R : null;
      edges.push({ from: o.first, to: o.second, sources: branch ? edgeSources(branch, o.first, o.second) : [] });
    }
  }

  // Kahn's algorithm with deterministic tie-breaking.
  const ids = arr.items.map((n) => n.id);
  const keyOf = (id: NodeId): (number | string)[] => sortKeyOf(id, baseIndex, L, R);
  const indeg = new Map<NodeId, number>(ids.map((id) => [id, 0]));
  const adj = new Map<NodeId, OrderEdge[]>(ids.map((id) => [id, []]));
  for (const e of edges) {
    if (!indeg.has(e.from) || !indeg.has(e.to)) continue;
    indeg.set(e.to, indeg.get(e.to)! + 1);
    adj.get(e.from)!.push(e);
  }
  const ready = ids.filter((id) => indeg.get(id) === 0);
  const order: NodeId[] = [];
  while (ready.length > 0) {
    ready.sort((x, y) => cmpKeys(keyOf(x), keyOf(y)));
    const u = ready.shift()!;
    order.push(u);
    for (const e of adj.get(u)!) {
      const d = indeg.get(e.to)! - 1;
      indeg.set(e.to, d);
      if (d === 0) ready.push(e.to);
    }
  }
  if (order.length < ids.length) {
    // Extract a witness cycle among the remaining nodes.
    const remaining = new Set(ids.filter((id) => !order.includes(id)));
    const color = new Map<NodeId, number>();
    const stack: NodeId[] = [];
    let cycleNodes: NodeId[] = [];
    const dfs = (u: NodeId): boolean => {
      color.set(u, 1);
      stack.push(u);
      for (const e of adj.get(u)!) {
        if (!remaining.has(e.to)) continue;
        const c = color.get(e.to) ?? 0;
        if (c === 1) {
          cycleNodes = [...stack.slice(stack.indexOf(e.to)), e.to];
          return true;
        }
        if (c === 0 && dfs(e.to)) return true;
      }
      stack.pop();
      color.set(u, 2);
      return false;
    };
    for (const id of remaining) {
      if ((color.get(id) ?? 0) === 0 && dfs(id)) break;
    }
    const cycle: CycleEdge[] = [];
    for (let i = 0; i + 1 < cycleNodes.length; i++) {
      const e = edges.find((x) => x.from === cycleNodes[i] && x.to === cycleNodes[i + 1]);
      if (e) cycle.push({ from: e.from, to: e.to, sources: e.sources });
    }
    const left = cycle.flatMap((e) => e.sources.filter((s) => s.branch === 'L'));
    const right = cycle.flatMap((e) => e.sources.filter((s) => s.branch === 'R'));
    return mkConflict('order-cycle', cycleNodes, left, right, cycle);
  }

  const byId = new Map(arr.items.map((n) => [n.id, n]));
  arr.items = order.map((id) => byId.get(id)!);
  return null;
}

function orderAllArrays(root: INode, L: BranchInfo, R: BranchInfo, baseRoot: INode): MergeConflict | null {
  const walk = (node: INode): MergeConflict | null => {
    if (node.kind === 'arr') {
      const conflict = orderArray(node, L, R, baseRoot);
      if (conflict) return conflict;
      for (const it of node.items) {
        const c = walk(it);
        if (c) return c;
      }
    } else if (node.kind === 'obj') {
      for (const v of node.children.values()) {
        const c = walk(v);
        if (c) return c;
      }
    }
    return null;
  };
  return walk(root);
}

function buildMerged(
  baseRoot: INode,
  L: BranchInfo,
  R: BranchInfo,
): { ok: true; root: INode } | { ok: false; conflicts: MergeConflict[] } {
  let root = cloneINode(baseRoot);

  // 1) Union of deletions (idempotent by id lookup).
  for (const id of sortIds([...L.deleted, ...R.deleted])) {
    if (id === baseRoot.id) continue; // root replacement handled below
    const loc = findById(root, id);
    if (loc && loc.parent) detach(loc);
  }

  // 2) Inserts from both branches, deduplicating agreed replacements/adds.
  const inserts: InsertInfo[] = [...L.inserts];
  for (const ri of R.inserts) {
    const dup = L.inserts.some((li) => {
      if (li.replacesId !== null && li.replacesId === ri.replacesId) return true;
      if (li.parentId === ri.parentId && li.key === ri.key) {
        return deepEqual(materialize(li.node), materialize(ri.node));
      }
      return false;
    });
    if (!dup) inserts.push(ri);
  }

  let pendingRoot: INode | null = null;
  for (const ins of inserts) {
    const fresh = clonePruningBase(ins.node);
    if (ins.parentId === null) {
      pendingRoot = fresh;
      continue;
    }
    const loc = findById(root, ins.parentId);
    if (!loc) {
      return { ok: false, conflicts: [mkConflict('delete-modify', [ins.parentId], [], [])] };
    }
    if (loc.node.kind === 'obj' && ins.key !== null) loc.node.children.set(ins.key, fresh);
    else if (loc.node.kind === 'arr') loc.node.items.push(fresh);
  }

  // 3) Moves (union; identical moves deduplicated by node id).
  const moves = new Map<NodeId, MoveInfo>(L.moves);
  for (const [id, mv] of R.moves) if (!moves.has(id)) moves.set(id, mv);
  for (const [id, mv] of [...moves.entries()].sort((a, b) => a[1].opIndex - b[1].opIndex)) {
    const loc = findById(root, id);
    if (!loc) continue; // deleted by the other branch: conflict reported earlier
    detach(loc);
    if (mv.destParentId === null) {
      pendingRoot = loc.node;
      continue;
    }
    const dest = findById(root, mv.destParentId);
    if (!dest) {
      return { ok: false, conflicts: [mkConflict('delete-modify', [mv.destParentId], [], [])] };
    }
    if (mv.intoArray && dest.node.kind === 'arr') dest.node.items.push(loc.node);
    else if (dest.node.kind === 'obj' && mv.destKey !== null) dest.node.children.set(mv.destKey, loc.node);
    else if (dest.node.kind === 'arr') dest.node.items.push(loc.node);
  }

  if (pendingRoot) root = pendingRoot;

  // 4) Array ordering via the combined constraint graph.
  const cycle = orderAllArrays(root, L, R, baseRoot);
  if (cycle) return { ok: false, conflicts: [cycle] };

  return { ok: true, root };
}

function sortConflicts(conflicts: MergeConflict[]): MergeConflict[] {
  return conflicts.sort((a, b) => {
    if (a.type !== b.type) return a.type < b.type ? -1 : 1;
    const an = a.nodes.join('');
    const bn = b.nodes.join('');
    return an < bn ? -1 : an > bn ? 1 : 0;
  });
}

/**
 * Merge two patches produced independently from the same base document.
 * On success returns the merged document plus rebased patches and their
 * inverses such that:
 *   apply(apply(base, left),  rebasedRight) === merged
 *   apply(apply(base, right), rebasedLeft)  === merged
 *   apply(merged, inverseRight) === apply(base, left)
 *   apply(merged, inverseLeft)  === apply(base, right)
 */
export function merge(base: Json, leftPatch: readonly Op[], rightPatch: readonly Op[]): MergeResult {
  const baseTree = buildBaseTree(base);
  const baseIds = collectIds(baseTree);
  const L = analyze(baseTree, leftPatch, 'L');
  const R = analyze(baseTree, rightPatch, 'R');

  const conflicts = detectConflicts(L, R, baseIds);
  if (conflicts.length > 0) return { ok: false, conflicts: sortConflicts(conflicts) };

  const built = buildMerged(baseTree, L, R);
  if (!built.ok) return { ok: false, conflicts: sortConflicts(built.conflicts) };

  const merged = materialize(built.root);
  const rebasedRight = generatePatch(L.root, built.root);
  const rebasedLeft = generatePatch(R.root, built.root);
  const leftDoc = apply(base, leftPatch).doc;
  const rightDoc = apply(base, rightPatch).doc;
  const inverseRight = apply(leftDoc, rebasedRight).inverse;
  const inverseLeft = apply(rightDoc, rebasedLeft).inverse;

  return { ok: true, merged, rebasedLeft, rebasedRight, inverseLeft, inverseRight };
}
