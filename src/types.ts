export type NodeType = 'file' | 'dir';

export interface SnapshotNode {
  id: string;
  generation: number;
  type: NodeType;
  parentId: string | null;
  name: string;
  digest: string | null;
}

export interface Snapshot {
  nodes: SnapshotNode[];
}

export interface NodeIdentity {
  id: string;
  generation: number;
}

export type Change =
  | { kind: 'added'; node: SnapshotNode }
  | { kind: 'deleted'; node: SnapshotNode }
  | { kind: 'renamed'; id: string; generation: number; oldName: string; newName: string }
  | {
      kind: 'moved';
      id: string;
      generation: number;
      oldParent: NodeIdentity | null;
      newParent: NodeIdentity | null;
    }
  | {
      kind: 'content-changed';
      id: string;
      generation: number;
      oldDigest: string | null;
      newDigest: string | null;
    };

export type PlanOp =
  | { op: 'create'; node: SnapshotNode }
  | { op: 'delete'; id: string; generation: number }
  | { op: 'move'; id: string; generation: number; toParentId: string | null; toName: string }
  | { op: 'moveToTemp'; id: string; generation: number; tempPath: string }
  | { op: 'updateContent'; id: string; generation: number; digest: string | null };

export interface Plan {
  changes: Change[];
  ops: PlanOp[];
}
