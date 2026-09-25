/**
 * Apply one branch's patch to a cloned identity tree, recording a per-op log
 * with resolved node identities. The log is what the merger uses to map
 * paths back to stable identities (see README "路径到内部身份的映射").
 */

import { parsePointer, arrayIndex, formatPointer, PointerError, type Json } from './json.ts';
import { PatchError, type Op } from './patch.ts';
import {
  cloneINode,
  collectIds,
  materialize,
  type INode,
  type NodeId,
} from './idtree.ts';

export type BranchId = 'L' | 'R';

export interface OpLog {
  branch: BranchId;
  opIndex: number;
  op: Op;
  /** Ids removed by this op (removed/replaced/overwritten node + descendants). */
  removedIds: NodeId[];
  /** Root id of a subtree created by add/replace, if any. */
  createdRootId: NodeId | null;
  /** Op index that created createdRootId (== opIndex; kept for clarity). */
  createdBy: number | null;
  /** For move: id of the relocated node. */
  movedId: NodeId | null;
  /** For move/add: destination parent id (null = document root). */
  destParentId: NodeId | null;
  /** For move/add into an object: the key. */
  destKey: string | null;
  /** For replace/overwrite-add: the base id that was substituted, if any. */
  replacedId: NodeId | null;
}

export interface BranchResult {
  root: INode;
  log: OpLog[];
}

interface Ctx {
  branch: BranchId;
  root: { node: INode };
  opIndex: number;
  newCounter: number;
}

function lookupNode(root: INode, tokens: string[]): INode | null {
  let cur: INode = root;
  for (const t of tokens) {
    if (cur.kind === 'arr') {
      let idx: number | '-';
      try {
        idx = arrayIndex(t, false);
      } catch {
        return null;
      }
      if (idx === '-' || idx >= cur.items.length) return null;
      cur = cur.items[idx]!;
    } else if (cur.kind === 'obj') {
      const next = cur.children.get(t);
      if (next === undefined) return null;
      cur = next;
    } else {
      return null;
    }
  }
  return cur;
}

function lookupParent(
  ctx: Ctx,
  path: string,
  tokens: string[],
): { parent: INode; key: string } {
  const parent = lookupNode(ctx.root.node, tokens.slice(0, -1));
  if (!parent || parent.kind === 'prim') {
    throw new PatchError(ctx.opIndex, path, 'parent path does not exist or is not a container');
  }
  return { parent, key: tokens[tokens.length - 1]! };
}

function buildFresh(json: Json, ctx: Ctx): INode {
  const mk = (v: Json): INode => {
    const id: NodeId = `${ctx.branch}:${ctx.opIndex}:${ctx.newCounter++}`;
    if (v === null || typeof v !== 'object') return { kind: 'prim', id, value: v };
    if (Array.isArray(v)) return { kind: 'arr', id, items: v.map(mk) };
    const children = new Map<string, INode>();
    for (const k of Object.keys(v)) children.set(k, mk(v[k]!));
    return { kind: 'obj', id, children };
  };
  return mk(json);
}

function parseOrThrow(opIndex: number, path: string): string[] {
  try {
    return parsePointer(path);
  } catch (e) {
    throw new PatchError(opIndex, path, e instanceof PointerError ? e.message : String(e));
  }
}

/** Apply ops to a clone of baseTree; returns the new identity tree and log. */
export function applyBranch(baseTree: INode, ops: readonly Op[], branch: BranchId): BranchResult {
  const ctx: Ctx = { branch, root: { node: cloneINode(baseTree) }, opIndex: 0, newCounter: 0 };
  const log: OpLog[] = [];

  const addAt = (path: string, tokens: string[], node: INode, logEntry: OpLog): void => {
    if (tokens.length === 0) {
      logEntry.removedIds.push(...collectIds(ctx.root.node));
      logEntry.destParentId = null;
      ctx.root.node = node;
      return;
    }
    const { parent, key } = lookupParent(ctx, path, tokens);
    if (parent.kind === 'arr') {
      let idx: number | '-';
      try {
        idx = arrayIndex(key, true);
      } catch (e) {
        throw new PatchError(ctx.opIndex, path, e instanceof PointerError ? e.message : String(e));
      }
      const at = idx === '-' ? parent.items.length : idx;
      if (at > parent.items.length) {
        throw new PatchError(ctx.opIndex, path, `array index ${at} out of bounds (length ${parent.items.length})`);
      }
      parent.items.splice(at, 0, node);
      logEntry.destParentId = parent.id;
      return;
    }
    const existing = parent.children.get(key);
    if (existing) {
      logEntry.removedIds.push(...collectIds(existing));
      logEntry.replacedId = existing.id;
    }
    parent.children.set(key, node);
    logEntry.destParentId = parent.id;
    logEntry.destKey = key;
  };

  const removeAt = (path: string, tokens: string[], logEntry: OpLog): INode => {
    const { parent, key } = lookupParent(ctx, path, tokens);
    if (parent.kind === 'arr') {
      let idx: number | '-';
      try {
        idx = arrayIndex(key, false);
      } catch (e) {
        throw new PatchError(ctx.opIndex, path, e instanceof PointerError ? e.message : String(e));
      }
      if ((idx as number) >= parent.items.length) {
        throw new PatchError(ctx.opIndex, path, `array index ${String(idx)} out of bounds (length ${parent.items.length})`);
      }
      const [node] = parent.items.splice(idx as number, 1);
      logEntry.removedIds.push(...collectIds(node!));
      return node!;
    }
    const node = parent.children.get(key);
    if (!node) throw new PatchError(ctx.opIndex, path, 'path does not exist');
    parent.children.delete(key);
    logEntry.removedIds.push(...collectIds(node));
    return node;
  };

  for (let i = 0; i < ops.length; i++) {
    ctx.opIndex = i;
    const op = ops[i]!;
    const entry: OpLog = {
      branch,
      opIndex: i,
      op,
      removedIds: [],
      createdRootId: null,
      createdBy: null,
      movedId: null,
      destParentId: null,
      destKey: null,
      replacedId: null,
    };
    log.push(entry);
    const path = (op as Op).path;
    if (op === null || typeof op !== 'object' || typeof path !== 'string') {
      throw new PatchError(i, '', 'malformed operation');
    }

    switch (op.op) {
      case 'add': {
        if (!('value' in op)) throw new PatchError(i, path, 'add requires a value');
        const tokens = parseOrThrow(i, path);
        const node = buildFresh(op.value, ctx);
        entry.createdRootId = node.id;
        entry.createdBy = i;
        addAt(path, tokens, node, entry);
        break;
      }
      case 'remove': {
        const tokens = parseOrThrow(i, path);
        if (tokens.length === 0) throw new PatchError(i, path, 'cannot remove the document root');
        removeAt(path, tokens, entry);
        break;
      }
      case 'replace': {
        if (!('value' in op)) throw new PatchError(i, path, 'replace requires a value');
        const tokens = parseOrThrow(i, path);
        const node = buildFresh(op.value, ctx);
        entry.createdRootId = node.id;
        entry.createdBy = i;
        if (tokens.length === 0) {
          entry.removedIds.push(...collectIds(ctx.root.node));
          entry.replacedId = ctx.root.node.id;
          ctx.root.node = node;
          break;
        }
        const { parent, key } = lookupParent(ctx, path, tokens);
        if (parent.kind === 'arr') {
          let idx: number | '-';
          try {
            idx = arrayIndex(key, false);
          } catch (e) {
            throw new PatchError(i, path, e instanceof PointerError ? e.message : String(e));
          }
          if ((idx as number) >= parent.items.length) {
            throw new PatchError(i, path, `array index ${String(idx)} out of bounds (length ${parent.items.length})`);
          }
          const old = parent.items[idx as number]!;
          entry.removedIds.push(...collectIds(old));
          entry.replacedId = old.id;
          parent.items[idx as number] = node;
        } else {
          const old = parent.children.get(key);
          if (!old) throw new PatchError(i, path, 'path does not exist');
          entry.removedIds.push(...collectIds(old));
          entry.replacedId = old.id;
          parent.children.set(key, node);
        }
        entry.destParentId = parent.id;
        break;
      }
      case 'move': {
        if (typeof op.from !== 'string') throw new PatchError(i, path, 'move requires a string "from"');
        const fromTokens = parseOrThrow(i, op.from);
        const toTokens = parseOrThrow(i, path);
        if (
          toTokens.length > fromTokens.length &&
          fromTokens.every((t, k) => toTokens[k] === t)
        ) {
          throw new PatchError(i, path, 'cannot move a node into its own descendant');
        }
        if (fromTokens.length === 0 && toTokens.length === 0) break;
        if (fromTokens.length === 0) throw new PatchError(i, path, 'cannot move the document root');
        const node = removeAt(op.from, fromTokens, entry);
        entry.removedIds.length = 0; // a move relocates; nothing is deleted
        entry.movedId = node.id;
        addAt(path, toTokens, node, entry);
        break;
      }
      case 'test': {
        if (!('value' in op)) throw new PatchError(i, path, 'test requires a value');
        const tokens = parseOrThrow(i, path);
        const target = lookupNode(ctx.root.node, tokens);
        if (!target) throw new PatchError(i, path, 'test failed: path does not exist');
        const { deepEqual } = jsonModule;
        if (!deepEqual(materialize(target), op.value)) {
          throw new PatchError(i, path, 'test failed: values differ');
        }
        break;
      }
      default:
        throw new PatchError(i, path, `unknown op "${String((op as Op).op)}"`);
    }
  }

  return { root: ctx.root.node, log };
}

// Local import indirection to keep the switch readable.
import * as jsonModule from './json.ts';
