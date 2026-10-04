import type { NodeKey, NormalizedNode, SnapshotIndex, SnapshotNode } from './types.ts';

export class SnapshotError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SnapshotError';
  }
}

/** 计算节点键，代际是身份的一部分：同 id 不同代际视为不同节点。 */
export function nodeKey(id: string, generation: number): NodeKey {
  return `${id}@${generation}`;
}

export function keyOf(node: { id: string; generation: number }): NodeKey {
  return nodeKey(node.id, node.generation);
}

/**
 * 归一化并校验快照：
 * - (id, generation) 必须唯一；
 * - parentId 必须指向快照内存在的目录（悬空父引用 = 移动到已删除父节点，直接报错）；
 * - 若同一 id 存在多个代际，则不允许其它节点以该 id 为父（引用有歧义）；
 * - 父子关系不允许成环。
 */
export function normalizeSnapshot(
  nodes: readonly SnapshotNode[],
  label = 'snapshot',
): SnapshotIndex {
  const map = new Map<NodeKey, NormalizedNode>();
  for (const n of nodes) {
    const generation = n.generation ?? 0;
    const key = nodeKey(n.id, generation);
    if (map.has(key)) {
      throw new SnapshotError(`${label}: 节点重复 id=${n.id} generation=${generation}`);
    }
    map.set(key, {
      id: n.id,
      type: n.type,
      parentId: n.parentId,
      name: n.name,
      digest: n.digest ?? null,
      generation,
    });
  }

  const byId = new Map<string, NormalizedNode[]>();
  for (const n of map.values()) {
    const list = byId.get(n.id);
    if (list) list.push(n);
    else byId.set(n.id, [n]);
  }

  const parentKeyOf = new Map<NodeKey, NodeKey | null>();
  for (const [key, n] of map) {
    if (n.parentId === null) {
      parentKeyOf.set(key, null);
      continue;
    }
    const candidates = byId.get(n.parentId);
    if (!candidates || candidates.length === 0) {
      throw new SnapshotError(
        `${label}: 节点 ${key} 的父节点 ${n.parentId} 不存在（目标父节点已删除？需先解决归属再生成快照）`,
      );
    }
    if (candidates.length > 1) {
      throw new SnapshotError(
        `${label}: 父 id ${n.parentId} 同时存在多个代际，节点 ${key} 的父引用有歧义`,
      );
    }
    const parent = candidates[0]!;
    if (parent.type !== 'dir') {
      throw new SnapshotError(`${label}: 节点 ${key} 的父节点 ${n.parentId} 不是目录`);
    }
    parentKeyOf.set(key, keyOf(parent));
  }

  for (const start of parentKeyOf.keys()) {
    const seen = new Set<NodeKey>([start]);
    let cur = parentKeyOf.get(start) ?? null;
    while (cur !== null) {
      if (seen.has(cur)) {
        throw new SnapshotError(`${label}: 父子关系成环，涉及节点 ${cur}`);
      }
      seen.add(cur);
      cur = parentKeyOf.get(cur) ?? null;
    }
  }

  return { nodes: map, parentKeyOf };
}

/** 节点在树中的深度（顶层为 0）。 */
export function depthOf(index: SnapshotIndex, key: NodeKey): number {
  let depth = 0;
  let cur = index.parentKeyOf.get(key) ?? null;
  while (cur !== null) {
    depth += 1;
    cur = index.parentKeyOf.get(cur) ?? null;
  }
  return depth;
}
