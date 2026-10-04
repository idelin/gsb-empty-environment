import { diffIndexes } from './diff.ts';
import type { SnapshotDiff } from './diff.ts';
import { depthOf, normalizeSnapshot } from './snapshot.ts';
import type { NodeKey, NodeType, NormalizedNode, SnapshotNode } from './types.ts';

export class PlanError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PlanError';
  }
}

/** 计划操作。所有引用都携带代际（NodeKey），避免 id 复用时产生歧义。 */
export type PlanOp =
  | { kind: 'mkdir'; key: NodeKey; node: NormalizedNode; parentKey: NodeKey | null }
  | { kind: 'create'; key: NodeKey; node: NormalizedNode; parentKey: NodeKey | null }
  | { kind: 'rename'; key: NodeKey; fromName: string; toName: string }
  | { kind: 'move'; key: NodeKey; fromParentKey: NodeKey | null; toParentKey: NodeKey | null }
  | { kind: 'update'; key: NodeKey; fromDigest: string | null; toDigest: string | null }
  | { kind: 'remove'; key: NodeKey; type: NodeType; name: string; parentKey: NodeKey | null };

export interface Plan {
  diff: SnapshotDiff;
  /** 按拓扑顺序排列，可直接顺序应用。 */
  ops: PlanOp[];
}

/**
 * 由源/目标快照生成应用计划。操作顺序保证：
 * 1. 新增目录（父先子后，按目标树深度排序）；
 * 2. 改名、移动（在旧父目录被删除之前完成 —— 移出待删目录的节点先走）；
 * 3. 内容更新；
 * 4. 新增文件（父目录此时必然存在）；
 * 5. 删除（子先父后，按源树深度倒序）。
 *
 * 移动到“目标快照中不存在（已删除）的父节点”会在快照校验或此处直接报错，需先解决归属。
 */
export function buildPlan(source: SnapshotNode[], target: SnapshotNode[]): Plan {
  const sourceIndex = normalizeSnapshot(source, 'source');
  const targetIndex = normalizeSnapshot(target, 'target');
  const diff = diffIndexes(sourceIndex, targetIndex);

  for (const m of diff.moved) {
    if (m.toParentKey === null) continue; // 移到顶层，无需父节点
    const parent = targetIndex.nodes.get(m.toParentKey);
    if (!parent) {
      throw new PlanError(`节点 ${m.key} 移动目标父节点 ${m.toParentKey} 已删除，需先解决`);
    }
    if (parent.type !== 'dir') {
      throw new PlanError(`节点 ${m.key} 的移动目标 ${m.toParentKey} 不是目录`);
    }
  }

  const byTargetDepth = (a: NormalizedNode, b: NormalizedNode) =>
    depthOf(targetIndex, `${a.id}@${a.generation}`) - depthOf(targetIndex, `${b.id}@${b.generation}`);
  const bySourceDepthDesc = (a: NormalizedNode, b: NormalizedNode) =>
    depthOf(sourceIndex, `${b.id}@${b.generation}`) - depthOf(sourceIndex, `${a.id}@${a.generation}`);

  const dirAdds = diff.added.filter((n) => n.type === 'dir').sort(byTargetDepth);
  const fileAdds = diff.added.filter((n) => n.type === 'file').sort(byTargetDepth);
  const removes = [...diff.removed].sort(bySourceDepthDesc);

  const ops: PlanOp[] = [
    ...dirAdds.map((node): PlanOp => ({
      kind: 'mkdir',
      key: `${node.id}@${node.generation}`,
      node,
      parentKey: targetIndex.parentKeyOf.get(`${node.id}@${node.generation}`) ?? null,
    })),
    ...diff.renamed.map((r): PlanOp => ({
      kind: 'rename',
      key: r.key,
      fromName: r.fromName,
      toName: r.node.name,
    })),
    ...diff.moved.map((m): PlanOp => ({
      kind: 'move',
      key: m.key,
      fromParentKey: m.fromParentKey,
      toParentKey: m.toParentKey,
    })),
    ...diff.contentChanged.map((c): PlanOp => ({
      kind: 'update',
      key: c.key,
      fromDigest: c.fromDigest,
      toDigest: c.node.digest,
    })),
    ...fileAdds.map((node): PlanOp => ({
      kind: 'create',
      key: `${node.id}@${node.generation}`,
      node,
      parentKey: targetIndex.parentKeyOf.get(`${node.id}@${node.generation}`) ?? null,
    })),
    ...removes.map((node): PlanOp => ({
      kind: 'remove',
      key: `${node.id}@${node.generation}`,
      type: node.type,
      name: node.name,
      parentKey: sourceIndex.parentKeyOf.get(`${node.id}@${node.generation}`) ?? null,
    })),
  ];

  return { diff, ops };
}
