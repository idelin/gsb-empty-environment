import type { ApplyResult, MoveOp, NodeId, TreeState } from './types.ts';
import { applyMove, isValidMove, removeSubtree, renameNode, reorderChildren } from './tree.ts';

/**
 * 模拟服务器：持有权威树，校验源版本与目标版本，
 * 用操作ID去重保证重试幂等。
 */
export class Server {
  tree: TreeState;
  private opLog = new Map<string, ApplyResult>();

  constructor(initial: TreeState) {
    this.tree = initial;
  }

  applyMove(op: MoveOp): ApplyResult {
    const seen = this.opLog.get(op.opId);
    if (seen) return { ...seen, deduplicated: true };
    const result = this.evaluate(op);
    this.opLog.set(op.opId, result);
    return result;
  }

  private evaluate(op: MoveOp): ApplyResult {
    if (op.sourceVersion !== this.tree.version) {
      return { status: 'rejected', opId: op.opId, reason: 'stale-source-version', version: this.tree.version };
    }
    const target = this.tree.nodes[op.targetId];
    if (!target) {
      return { status: 'rejected', opId: op.opId, reason: 'target-missing', version: this.tree.version };
    }
    if (target.revision !== op.targetRevision) {
      return { status: 'rejected', opId: op.opId, reason: 'stale-target-revision', version: this.tree.version };
    }
    if (!this.tree.nodes[op.sourceId]) {
      return { status: 'rejected', opId: op.opId, reason: 'source-missing', version: this.tree.version };
    }
    if (!isValidMove(this.tree, op)) {
      return { status: 'rejected', opId: op.opId, reason: 'invalid-move', version: this.tree.version };
    }
    this.tree = applyMove(this.tree, op);
    return { status: 'applied', opId: op.opId, version: this.tree.version, tree: this.tree };
  }

  /** 服务器端重排（回包会推送给客户端） */
  reorder(parentId: NodeId | null, orderedChildIds: NodeId[]): TreeState {
    this.tree = reorderChildren(this.tree, parentId, orderedChildIds);
    return this.tree;
  }

  remove(id: NodeId): TreeState {
    this.tree = removeSubtree(this.tree, id);
    return this.tree;
  }

  rename(id: NodeId, label: string): TreeState {
    this.tree = renameNode(this.tree, id, label);
    return this.tree;
  }

  appliedOpCount(): number {
    return this.opLog.size;
  }
}
