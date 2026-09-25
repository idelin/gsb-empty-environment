export type NodeId = string;
export type Placement = 'before' | 'after' | 'inside';

export interface TreeNode {
  id: NodeId;
  parentId: NodeId | null;
  childIds: NodeId[];
  label: string;
  /** 结构版本：父链/子列表变化时自增，用于提交时校验目标未变 */
  revision: number;
}

export interface TreeState {
  nodes: Record<NodeId, TreeNode>;
  rootIds: NodeId[];
  /** 全树版本：任何结构/内容编辑都会自增，用于乐观并发控制 */
  version: number;
}

/** 逻辑移动描述（鼠标与键盘共用） */
export interface MoveSpec {
  sourceId: NodeId;
  targetId: NodeId;
  placement: Placement;
}

/** 提交到服务器的操作：携带操作ID（幂等）、源版本与目标版本 */
export interface MoveOp extends MoveSpec {
  opId: string;
  sourceVersion: number;
  targetRevision: number;
}

export interface Row {
  id: NodeId;
  depth: number;
}

export type TraceEntry =
  | { kind: 'begin'; sourceId: NodeId }
  | { kind: 'preview'; spec: MoveSpec }
  | { kind: 'commit'; op: MoveOp }
  | { kind: 'cancel' }
  | { kind: 'terminate'; reason: string };

export type ApplyResult =
  | { status: 'applied'; opId: string; version: number; tree: TreeState; deduplicated?: boolean }
  | { status: 'rejected'; opId: string; reason: string; version: number; deduplicated?: boolean };

export function specsEqual(a: MoveSpec, b: MoveSpec): boolean {
  return a.sourceId === b.sourceId && a.targetId === b.targetId && a.placement === b.placement;
}
