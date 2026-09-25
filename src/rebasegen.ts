/**
 * Identity-aware patch generation: given two identity trees that share node
 * ids (source and target), emit a legal, ordered JSON Patch that transforms
 * source into target. Used to produce rebasedLeft/rebasedRight.
 *
 * Method (see README "重新生成合法顺序补丁的方法"):
 *  - Work on a mutable clone W of the source id-tree.
 *  - Reconcile top-down; at every position the W node and target node have
 *    the same id on entry.
 *  - Objects: drop keys absent in target, then for each target key either
 *    recurse (same id), move the node in from its current location (id exists
 *    elsewhere in W), or add a fresh subtree (new id).
 *  - Arrays: first remove elements whose id is not in the target array
 *    (descending index), then walk target positions left to right; positions
 *    0..i-1 are already final, so a move/add at index i lands exactly right.
 *  - Fresh subtrees are materialized with any descendant whose id still
 *    exists in W omitted; the subsequent reconcile moves those in by identity.
 *  - A node whose id lives elsewhere in the target is never duplicated: it is
 *    moved when reachable, otherwise removed here and re-added at its target
 *    position from target content (results stay JSON-equal).
 */

import { deepClone, formatPointer, type Json } from './json.ts';
import type { Op } from './patch.ts';
import {
  cloneINode,
  collectIds,
  detach,
  findById,
  materialize,
  type INode,
  type NodeId,
} from './idtree.ts';

export function generatePatch(source: INode, target: INode): Op[] {
  const ops: Op[] = [];
  let root = cloneINode(source);

  const emit = (op: Op): void => {
    ops.push(op);
  };

  /** JSON of node, omitting descendants whose id is in `skip`. */
  const prunedMaterialize = (node: INode, skip: Set<NodeId>): Json => {
    if (node.kind === 'prim') return deepClone(node.value);
    if (node.kind === 'arr') {
      const out: Json[] = [];
      for (const it of node.items) if (!skip.has(it.id)) out.push(prunedMaterialize(it, skip));
      return out;
    }
    const out: { [key: string]: Json } = {};
    for (const [k, v] of node.children) {
      if (!skip.has(v.id)) out[k] = prunedMaterialize(v, skip);
    }
    return out;
  };

  /** Clone of node keeping target ids, omitting descendants in `skip`. */
  const prunedClone = (node: INode, skip: Set<NodeId>): INode => {
    if (node.kind === 'prim') return { kind: 'prim', id: node.id, value: node.value };
    if (node.kind === 'arr') {
      const items: INode[] = [];
      for (const it of node.items) if (!skip.has(it.id)) items.push(prunedClone(it, skip));
      return { kind: 'arr', id: node.id, items };
    }
    const children = new Map<string, INode>();
    for (const [k, v] of node.children) {
      if (!skip.has(v.id)) children.set(k, prunedClone(v, skip));
    }
    return { kind: 'obj', id: node.id, children };
  };

  /** Place target node `t` at object key / array index of `wParent`. */
  const placeFresh = (t: INode, path: string): INode => {
    const skip = collectIds(root);
    emit({ op: 'add', path, value: prunedMaterialize(t, skip) });
    return prunedClone(t, skip);
  };

  /** Move the existing W node with id t.id to `path`; returns it. */
  const moveExisting = (t: INode, path: string): INode => {
    const loc = findById(root, t.id);
    if (!loc) throw new Error(`internal: node ${t.id} not found in working tree`);
    emit({ op: 'move', from: loc.path, path });
    detach(loc);
    return loc.node;
  };

  const reconcile = (w: INode, t: INode, path: string): void => {
    // Precondition: w.id === t.id, hence same kind.
    if (t.kind === 'prim') {
      if (w.kind === 'prim' && w.value !== t.value) {
        emit({ op: 'replace', path, value: deepClone(t.value) });
        (w as { value: Json }).value = t.value;
      }
      return;
    }
    if (t.kind === 'obj') {
      const wObj = w as Extract<INode, { kind: 'obj' }>;
      for (const k of [...wObj.children.keys()]) {
        if (!t.children.has(k)) {
          emit({ op: 'remove', path: `${path}/${escape(k)}` });
          wObj.children.delete(k);
        }
      }
      for (const [k, tc] of t.children) {
        const childPath = `${path}/${escape(k)}`;
        const wc = wObj.children.get(k);
        if (wc && wc.id === tc.id) {
          reconcile(wc, tc, childPath);
          continue;
        }
        if (wc) {
          emit({ op: 'remove', path: childPath });
          wObj.children.delete(k);
        }
        let placed: INode;
        if (findById(root, tc.id)) placed = moveExisting(tc, childPath);
        else placed = placeFresh(tc, childPath);
        wObj.children.set(k, placed);
        reconcile(placed, tc, childPath);
      }
      return;
    }
    // Arrays.
    const wArr = w as Extract<INode, { kind: 'arr' }>;
    const tArr = t as Extract<INode, { kind: 'arr' }>;
    const wanted = new Set<NodeId>(tArr.items.map((it) => it.id));
    for (let i = wArr.items.length - 1; i >= 0; i--) {
      if (!wanted.has(wArr.items[i]!.id)) {
        emit({ op: 'remove', path: `${path}/${i}` });
        wArr.items.splice(i, 1);
      }
    }
    for (let i = 0; i < tArr.items.length; i++) {
      const tc = tArr.items[i]!;
      const childPath = `${path}/${i}`;
      const wc = wArr.items[i];
      if (wc && wc.id === tc.id) {
        reconcile(wc, tc, childPath);
        continue;
      }
      let placed: INode;
      if (findById(root, tc.id)) placed = moveExisting(tc, childPath);
      else placed = placeFresh(tc, childPath);
      wArr.items.splice(i, 0, placed);
      reconcile(placed, tc, childPath);
    }
  };

  if (root.id !== target.id) {
    const loc = findById(root, target.id);
    if (loc) {
      emit({ op: 'move', from: loc.path, path: '' });
      detach(loc);
      root = loc.node;
    } else {
      const skip = collectIds(root);
      emit({ op: 'replace', path: '', value: prunedMaterialize(target, skip) });
      root = prunedClone(target, skip);
    }
  }
  reconcile(root, target, '');
  return ops;
}

function escape(token: string): string {
  return token.replace(/~/g, '~0').replace(/\//g, '~1');
}

// Re-exported for tests: apply generated ops to source JSON must yield target JSON.
export { materialize, formatPointer };
