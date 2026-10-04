/** 节点类型：文件或目录。 */
export type NodeType = 'file' | 'dir';

/**
 * 快照节点输入。
 * - `id`：节点身份标识，搬移/改名/内容变化时保持不变。
 * - `generation`：代际。同一 id 被回收重建时必须递增，
 *   用于把“id 复用”与“同一节点被修改”区分开。
 * - `digest`：文件内容摘要；目录通常为 null。
 */
export interface SnapshotNode {
  id: string;
  type: NodeType;
  parentId: string | null;
  name: string;
  digest?: string | null;
  generation?: number;
}

/** 归一化后的节点（generation / digest 已填默认值）。 */
export interface NormalizedNode {
  id: string;
  type: NodeType;
  parentId: string | null;
  name: string;
  digest: string | null;
  generation: number;
}

/** 节点键：`${id}@${generation}`，代际是身份的一部分。 */
export type NodeKey = string;

/** 归一化 + 校验后的快照索引。 */
export interface SnapshotIndex {
  /** key -> 节点 */
  nodes: Map<NodeKey, NormalizedNode>;
  /** key -> 父节点 key（顶层节点为 null） */
  parentKeyOf: Map<NodeKey, NodeKey | null>;
}
