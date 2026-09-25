/**
 * Identity tree: every node of a JSON document gets a stable internal id.
 * Base nodes are identified as b#<n> (preorder). Nodes created by a branch
 * are identified as <branch>:<opIndex>:<k>. Identity is structural — it is
 * never derived from serialized content, so duplicate values stay distinct.
 */

import { deepClone, escapeToken, type Json } from './json.ts';

export type NodeId = string;

export type INode =
  | { kind: 'prim'; id: NodeId; value: Json }
  | { kind: 'arr'; id: NodeId; items: INode[] }
  | { kind: 'obj'; id: NodeId; children: Map<string, INode> };

export function isBaseId(id: NodeId): boolean {
  return id.startsWith('b#');
}

export function baseIdOrdinal(id: NodeId): number {
  return isBaseId(id) ? Number(id.slice(2)) : Number.MAX_SAFE_INTEGER;
}

export function buildIdTree(json: Json, nextId: () => NodeId): INode {
  const id = nextId();
  if (json === null || typeof json !== 'object') return { kind: 'prim', id, value: json };
  if (Array.isArray(json)) {
    return { kind: 'arr', id, items: json.map((v) => buildIdTree(v, nextId)) };
  }
  const children = new Map<string, INode>();
  for (const k of Object.keys(json)) children.set(k, buildIdTree(json[k]!, nextId));
  return { kind: 'obj', id, children };
}

/** Assign deterministic base ids b#0, b#1, ... in preorder. */
export function buildBaseTree(json: Json): INode {
  let counter = 0;
  return buildIdTree(json, () => `b#${counter++}`);
}

export function cloneINode(node: INode): INode {
  if (node.kind === 'prim') return { kind: 'prim', id: node.id, value: node.value };
  if (node.kind === 'arr') return { kind: 'arr', id: node.id, items: node.items.map(cloneINode) };
  const children = new Map<string, INode>();
  for (const [k, v] of node.children) children.set(k, cloneINode(v));
  return { kind: 'obj', id: node.id, children };
}

export function materialize(node: INode): Json {
  if (node.kind === 'prim') return deepClone(node.value);
  if (node.kind === 'arr') return node.items.map(materialize);
  const out: { [key: string]: Json } = {};
  for (const [k, v] of node.children) out[k] = materialize(v);
  return out;
}

export interface Location {
  node: INode;
  parent: INode | null;
  /** Key within parent: string for objects, number for arrays, null at root. */
  key: string | number | null;
  /** JSON Pointer path of the node within the searched root. */
  path: string;
}

export function findById(root: INode, id: NodeId): Location | null {
  const walk = (node: INode, parent: INode | null, key: string | number | null, path: string): Location | null => {
    if (node.id === id) return { node, parent, key, path };
    if (node.kind === 'arr') {
      for (let i = 0; i < node.items.length; i++) {
        const hit = walk(node.items[i]!, node, i, `${path}/${i}`);
        if (hit) return hit;
      }
    } else if (node.kind === 'obj') {
      for (const [k, v] of node.children) {
        const hit = walk(v, node, k, `${path}/${escapeToken(k)}`);
        if (hit) return hit;
      }
    }
    return null;
  };
  return walk(root, null, null, '');
}

export function collectIds(node: INode, into: Set<NodeId> = new Set()): Set<NodeId> {
  into.add(node.id);
  if (node.kind === 'arr') for (const it of node.items) collectIds(it, into);
  if (node.kind === 'obj') for (const v of node.children.values()) collectIds(v, into);
  return into;
}

/** Detach the node at loc from its parent. No-op for the root. */
export function detach(loc: Location): void {
  if (loc.parent === null) return;
  if (loc.parent.kind === 'arr') loc.parent.items.splice(loc.key as number, 1);
  else if (loc.parent.kind === 'obj') loc.parent.children.delete(loc.key as string);
}
