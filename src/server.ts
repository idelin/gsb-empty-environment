/** Authoritative server: owns the canonical tree and its version.
 *  - Move commits are validated against the target AND the source version.
 *  - opId deduplication makes client retries idempotent.
 *  - Applied changes are broadcast to every other connected client. */
import type {
  CommitOp, CommitResult, InsertPatch, MovePatch, NodeId, Patch,
  RemovePatch, ServerMessage, Tree, TreeNode,
} from './types.ts';
import { applyMove, insertNode, locate, removeSubtree, subtreeIds, validateMove } from './tree.ts';

export interface InsertOp { opId: string; node: TreeNode; parentId: NodeId; index: number }
export interface RemoveOp { opId: string; nodeId: NodeId }
export type OpResult =
  | { ok: true; opId: string; version: number; patch: Patch; deduplicated: boolean }
  | { ok: false; opId: string; reason: string };

export class Server {
  tree: Tree;
  version = 0;
  private applied = new Map<string, { version: number; patch: Patch }>();
  private listeners = new Map<string, (msg: ServerMessage) => void>();

  constructor(tree: Tree) {
    this.tree = tree;
  }

  register(clientId: string, cb: (msg: ServerMessage) => void): void {
    this.listeners.set(clientId, cb);
  }

  unregister(clientId: string): void {
    this.listeners.delete(clientId);
  }

  private broadcast(exceptId: string, msg: ServerMessage): void {
    for (const [id, cb] of this.listeners) {
      if (id !== exceptId) cb(msg);
    }
  }

  commit(clientId: string, op: CommitOp): CommitResult {
    const prev = this.applied.get(op.opId);
    if (prev) {
      return { ok: true, opId: op.opId, version: prev.version, patch: prev.patch as MovePatch, deduplicated: true };
    }
    if (op.baseVersion !== this.version) {
      return { ok: false, opId: op.opId, reason: `stale source version: client=${op.baseVersion} server=${this.version}` };
    }
    const { nodeId, from, to } = op.patch;
    if (!this.tree.nodes[nodeId]) {
      return { ok: false, opId: op.opId, reason: `source node missing: ${nodeId}` };
    }
    const actual = locate(this.tree, nodeId);
    if (actual.parentId !== from.parentId || actual.index !== from.index) {
      return { ok: false, opId: op.opId, reason: `source mismatch for ${nodeId}` };
    }
    const invalid = validateMove(this.tree, nodeId, to);
    if (invalid) {
      return { ok: false, opId: op.opId, reason: `invalid target: ${invalid}` };
    }
    const { tree, patch } = applyMove(this.tree, nodeId, to);
    this.tree = tree;
    this.version += 1;
    this.applied.set(op.opId, { version: this.version, patch });
    this.broadcast(clientId, { type: 'reorder', opId: op.opId, version: this.version, patch });
    return { ok: true, opId: op.opId, version: this.version, patch, deduplicated: false };
  }

  insert(clientId: string, op: InsertOp): OpResult {
    const prev = this.applied.get(op.opId);
    if (prev) return { ok: true, opId: op.opId, version: prev.version, patch: prev.patch, deduplicated: true };
    if (!this.tree.nodes[op.parentId]) {
      return { ok: false, opId: op.opId, reason: `unknown parent: ${op.parentId}` };
    }
    if (this.tree.nodes[op.node.id]) {
      return { ok: false, opId: op.opId, reason: `duplicate node: ${op.node.id}` };
    }
    this.tree = insertNode(this.tree, op.node, op.parentId, op.index);
    this.version += 1;
    const patch: InsertPatch = { kind: 'insert', node: op.node, parentId: op.parentId, index: op.index };
    this.applied.set(op.opId, { version: this.version, patch });
    this.broadcast(clientId, { type: 'insert', opId: op.opId, version: this.version, patch });
    return { ok: true, opId: op.opId, version: this.version, patch, deduplicated: false };
  }

  remove(clientId: string, op: RemoveOp): OpResult {
    const prev = this.applied.get(op.opId);
    if (prev) return { ok: true, opId: op.opId, version: prev.version, patch: prev.patch, deduplicated: true };
    if (!this.tree.nodes[op.nodeId] || op.nodeId === this.tree.rootId) {
      return { ok: false, opId: op.opId, reason: `unknown node: ${op.nodeId}` };
    }
    const patch: RemovePatch = {
      kind: 'remove',
      nodeId: op.nodeId,
      from: locate(this.tree, op.nodeId),
      removedIds: subtreeIds(this.tree, op.nodeId),
    };
    this.tree = removeSubtree(this.tree, op.nodeId);
    this.version += 1;
    this.applied.set(op.opId, { version: this.version, patch });
    this.broadcast(clientId, { type: 'remove', opId: op.opId, version: this.version, patch });
    return { ok: true, opId: op.opId, version: this.version, patch, deduplicated: false };
  }
}
