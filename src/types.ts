/** Shared domain types for the sortable hierarchical list. */

export type NodeId = string;

export interface TreeNode {
  id: NodeId;
  parentId: NodeId | null;
  label: string;
  children: NodeId[];
}

/** Canonical, authoritative tree. Never mutated by drag previews. */
export interface Tree {
  rootId: NodeId;
  nodes: Record<NodeId, TreeNode>;
}

/** A drop position. `index` is expressed in post-removal coordinates:
 *  the dragged node is considered already removed from its old parent. */
export interface Location {
  parentId: NodeId;
  index: number;
}

export interface MovePatch {
  kind: 'move';
  nodeId: NodeId;
  from: Location;
  to: Location;
}

export interface InsertPatch {
  kind: 'insert';
  node: TreeNode;
  parentId: NodeId;
  index: number;
}

export interface RemovePatch {
  kind: 'remove';
  nodeId: NodeId;
  from: Location;
  removedIds: NodeId[];
}

export type Patch = MovePatch | InsertPatch | RemovePatch;

/** A commit request sent to the server. `opId` makes retries idempotent. */
export interface CommitOp {
  opId: string;
  baseVersion: number;
  patch: MovePatch;
}

export type CommitResult =
  | { ok: true; opId: string; version: number; patch: MovePatch; deduplicated: boolean }
  | { ok: false; opId: string; reason: string };

/** Authoritative messages broadcast by the server. */
export type ServerMessage =
  | { type: 'reorder'; opId: string; version: number; patch: MovePatch }
  | { type: 'insert'; opId: string; version: number; patch: InsertPatch }
  | { type: 'remove'; opId: string; version: number; patch: RemovePatch };

/** Logical operation trace entries emitted by the shared state machine.
 *  Mouse and keyboard drivers must produce identical traces for
 *  equivalent gestures. */
export type TraceEntry =
  | { op: 'start'; nodeId: NodeId }
  | { op: 'target'; parentId: NodeId; index: number }
  | { op: 'commit'; patch: MovePatch }
  | { op: 'cancel' }
  | { op: 'terminate'; reason: string };

/** Focus/selection state of the list widget (no DOM involved). */
export interface UiState {
  focusId: NodeId | null;
  selection: NodeId[];
}
