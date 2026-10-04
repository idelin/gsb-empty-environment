import type { PlanOp } from './plan.ts';
import { normalizeSnapshot } from './snapshot.ts';
import type { NodeKey, NormalizedNode, SnapshotNode } from './types.ts';

export class ApplyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ApplyError';
  }
}

const ROOT_SLOT = '<root>';

/**
 * 独立计划应用器：在一棵内存树上顺序执行计划，逐步核对前置状态，
 * 并用临时路径解决目标槽位冲突（如 a↔b 互换改名、新节点占用待删节点的名字）。
 * 应用完成后可用 verifyAgainst 核对整棵目标树。
 */
export class TreeApplier {
  private nodes = new Map<NodeKey, NormalizedNode>();
  private parentOf = new Map<NodeKey, NodeKey | null>();
  private childrenOf = new Map<NodeKey, Set<NodeKey>>();
  /** `(父 key, 名称)` 槽位 -> 占用者 key，用于检测路径冲突。 */
  private slotIndex = new Map<string, NodeKey>();
  /** 被临时挪走的节点：其后续操作跳过“原始位置”前置校验。 */
  private tempRelocated = new Set<NodeKey>();
  /** 计划中稍后会腾空当前槽位的节点（将被改名/移动/删除）。 */
  private willVacate = new Set<NodeKey>();
  private tempSeq = 0;

  constructor(source: SnapshotNode[]) {
    const index = normalizeSnapshot(source, 'source');
    for (const [key, node] of index.nodes) {
      const parentKey = index.parentKeyOf.get(key) ?? null;
      this.nodes.set(key, { ...node });
      this.parentOf.set(key, parentKey);
      this.slotIndex.set(TreeApplier.slotKey(parentKey, node.name), key);
      if (parentKey !== null) {
        let siblings = this.childrenOf.get(parentKey);
        if (!siblings) {
          siblings = new Set();
          this.childrenOf.set(parentKey, siblings);
        }
        siblings.add(key);
      }
    }
  }

  private static slotKey(parentKey: NodeKey | null, name: string): string {
    return `${parentKey ?? ROOT_SLOT}${name}`;
  }

  /** 应用整个计划；任何一步前置校验失败都会抛 ApplyError 并中止。 */
  apply(ops: readonly PlanOp[]): void {
    this.willVacate = new Set(
      ops
        .filter((op) => op.kind === 'rename' || op.kind === 'move' || op.kind === 'remove')
        .map((op) => op.key),
    );
    ops.forEach((op, i) => this.applyOne(op, i));
  }

  /** 应用计划并核对最终树与目标快照完全一致。 */
  applyAndVerify(ops: readonly PlanOp[], target: SnapshotNode[]): void {
    this.apply(ops);
    const mismatches = this.verifyAgainst(target);
    if (mismatches.length > 0) {
      throw new ApplyError(`应用结果与目标树不一致:\n  ${mismatches.join('\n  ')}`);
    }
  }

  /** 核对当前树与目标快照，返回不一致项（空数组 = 完全一致）。 */
  verifyAgainst(target: SnapshotNode[]): string[] {
    const index = normalizeSnapshot(target, 'target');
    const mismatches: string[] = [];
    for (const [key, want] of index.nodes) {
      const got = this.nodes.get(key);
      if (!got) {
        mismatches.push(`缺失节点 ${key}`);
        continue;
      }
      if (got.type !== want.type) mismatches.push(`${key}: 类型 ${got.type} != ${want.type}`);
      if (got.name !== want.name) mismatches.push(`${key}: 名称 ${got.name} != ${want.name}`);
      if (got.digest !== want.digest) mismatches.push(`${key}: 摘要不一致`);
      const gotParent = this.parentOf.get(key) ?? null;
      const wantParent = index.parentKeyOf.get(key) ?? null;
      if (gotParent !== wantParent) {
        mismatches.push(`${key}: 父节点 ${gotParent} != ${wantParent}`);
      }
    }
    for (const key of this.nodes.keys()) {
      if (!index.nodes.has(key)) mismatches.push(`多余节点 ${key}`);
    }
    return mismatches;
  }

  /** 导出当前树（归一化节点列表）。 */
  toSnapshot(): NormalizedNode[] {
    return [...this.nodes.values()].map((n) => ({ ...n }));
  }

  private applyOne(op: PlanOp, index: number): void {
    try {
      switch (op.kind) {
        case 'mkdir':
        case 'create':
          this.applyAdd(op.key, op.node, op.parentKey);
          break;
        case 'rename':
          this.applyRename(op, index);
          break;
        case 'move':
          this.applyMove(op, index);
          break;
        case 'update':
          this.applyUpdate(op);
          break;
        case 'remove':
          this.applyRemove(op);
          break;
      }
    } catch (err) {
      if (err instanceof ApplyError) {
        throw new ApplyError(`第 ${index} 步 [${op.kind} ${op.key}] 失败: ${err.message}`);
      }
      throw err;
    }
  }

  private requireNode(key: NodeKey): NormalizedNode {
    const node = this.nodes.get(key);
    if (!node) throw new ApplyError(`节点不存在: ${key}`);
    return node;
  }

  private requireDir(key: NodeKey): void {
    const node = this.requireNode(key);
    if (node.type !== 'dir') throw new ApplyError(`${key} 不是目录`);
  }

  /** 目标槽位被占用时：占用者稍后会腾空则先挪到临时路径，否则报冲突。 */
  private freeSlot(parentKey: NodeKey | null, name: string, self: NodeKey): void {
    const slot = TreeApplier.slotKey(parentKey, name);
    const occupant = this.slotIndex.get(slot);
    if (occupant === undefined || occupant === self) return;
    if (!this.willVacate.has(occupant)) {
      throw new ApplyError(`目标路径 ${parentKey ?? ROOT_SLOT}/${name} 被 ${occupant} 占用且其不会腾空`);
    }
    this.relocateToTemp(occupant);
  }

  /** 把节点在同目录下改成唯一临时名，腾出原槽位。 */
  private relocateToTemp(key: NodeKey): void {
    const node = this.requireNode(key);
    const parentKey = this.parentOf.get(key) ?? null;
    let tempName: string;
    do {
      this.tempSeq += 1;
      tempName = `.__tmp_${this.tempSeq}_${node.name}`;
    } while (this.slotIndex.has(TreeApplier.slotKey(parentKey, tempName)));
    this.slotIndex.delete(TreeApplier.slotKey(parentKey, node.name));
    this.slotIndex.set(TreeApplier.slotKey(parentKey, tempName), key);
    node.name = tempName;
    this.tempRelocated.add(key);
  }

  private placeAt(key: NodeKey, parentKey: NodeKey | null, name: string): void {
    const node = this.requireNode(key);
    const oldParent = this.parentOf.get(key) ?? null;
    this.slotIndex.delete(TreeApplier.slotKey(oldParent, node.name));
    if (oldParent !== null) this.childrenOf.get(oldParent)?.delete(key);

    this.freeSlot(parentKey, name, key);

    node.name = name;
    this.parentOf.set(key, parentKey);
    this.slotIndex.set(TreeApplier.slotKey(parentKey, name), key);
    if (parentKey !== null) {
      let siblings = this.childrenOf.get(parentKey);
      if (!siblings) {
        siblings = new Set();
        this.childrenOf.set(parentKey, siblings);
      }
      siblings.add(key);
    }
    this.tempRelocated.delete(key);
  }

  private applyAdd(key: NodeKey, node: NormalizedNode, parentKey: NodeKey | null): void {
    if (this.nodes.has(key)) throw new ApplyError(`节点已存在: ${key}`);
    if (parentKey !== null) this.requireDir(parentKey);
    this.nodes.set(key, { ...node });
    this.parentOf.set(key, parentKey);
    this.freeSlot(parentKey, node.name, key);
    this.slotIndex.set(TreeApplier.slotKey(parentKey, node.name), key);
    if (parentKey !== null) {
      let siblings = this.childrenOf.get(parentKey);
      if (!siblings) {
        siblings = new Set();
        this.childrenOf.set(parentKey, siblings);
      }
      siblings.add(key);
    }
  }

  private applyRename(op: Extract<PlanOp, { kind: 'rename' }>, _index: number): void {
    const node = this.requireNode(op.key);
    if (!this.tempRelocated.has(op.key) && node.name !== op.fromName) {
      throw new ApplyError(`改名前置校验失败: 期望原名 ${op.fromName}，实际 ${node.name}`);
    }
    const parentKey = this.parentOf.get(op.key) ?? null;
    this.placeAt(op.key, parentKey, op.toName);
  }

  private applyMove(op: Extract<PlanOp, { kind: 'move' }>, _index: number): void {
    this.requireNode(op.key);
    if (!this.tempRelocated.has(op.key)) {
      const actual = this.parentOf.get(op.key) ?? null;
      if (actual !== op.fromParentKey) {
        throw new ApplyError(`移动前置校验失败: 期望原父节点 ${op.fromParentKey}，实际 ${actual}`);
      }
    }
    if (op.toParentKey !== null) this.requireDir(op.toParentKey);
    const node = this.requireNode(op.key);
    this.placeAt(op.key, op.toParentKey, node.name);
  }

  private applyUpdate(op: Extract<PlanOp, { kind: 'update' }>): void {
    const node = this.requireNode(op.key);
    if (node.type !== 'file') throw new ApplyError(`只能更新文件内容摘要: ${op.key}`);
    if (node.digest !== op.fromDigest) {
      throw new ApplyError(`内容前置校验失败: 期望摘要 ${op.fromDigest}，实际 ${node.digest}`);
    }
    node.digest = op.toDigest;
  }

  private applyRemove(op: Extract<PlanOp, { kind: 'remove' }>): void {
    const node = this.requireNode(op.key);
    if (!this.tempRelocated.has(op.key)) {
      if (node.name !== op.name) {
        throw new ApplyError(`删除前置校验失败: 期望名称 ${op.name}，实际 ${node.name}`);
      }
      const actualParent = this.parentOf.get(op.key) ?? null;
      if (actualParent !== op.parentKey) {
        throw new ApplyError(`删除前置校验失败: 期望父节点 ${op.parentKey}，实际 ${actualParent}`);
      }
    }
    if (node.type === 'dir' && (this.childrenOf.get(op.key)?.size ?? 0) > 0) {
      throw new ApplyError(`目录非空，无法删除: ${op.key}`);
    }
    const parentKey = this.parentOf.get(op.key) ?? null;
    this.slotIndex.delete(TreeApplier.slotKey(parentKey, node.name));
    if (parentKey !== null) this.childrenOf.get(parentKey)?.delete(op.key);
    this.parentOf.delete(op.key);
    this.childrenOf.delete(op.key);
    this.nodes.delete(op.key);
    this.tempRelocated.delete(op.key);
  }
}
