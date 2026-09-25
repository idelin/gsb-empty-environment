/** Any value representable in JSON. Object key order is not meaningful. */
export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

/**
 * A node in the input tree. `id` is the stable identity used by the diff.
 * `attrs` holds unordered attributes; `children` is an ordered list.
 */
export interface TreeNode {
  id: string;
  attrs?: { [key: string]: JsonValue };
  children?: TreeNode[];
}

export interface InsertOp {
  op: 'insert';
  id: string;
  /** `null` means the node becomes the root. */
  parentId: string | null;
  /** Final position within the parent's children in the new tree. */
  index: number;
  attrs: { [key: string]: JsonValue };
}

export interface UpdateOp {
  op: 'update';
  id: string;
  /** Attributes added or changed. */
  set: { [key: string]: JsonValue };
  /** Attribute keys removed, sorted ascending. */
  unset: string[];
}

export interface MoveOp {
  op: 'move';
  id: string;
  parentId: string | null;
  /** Final position within the parent's children in the new tree. */
  index: number;
}

export interface RemoveOp {
  op: 'remove';
  id: string;
}

/**
 * Canonical patch order: inserts (parents before children), then updates,
 * then moves, then removes. Indexes always refer to final positions in the
 * new tree, so a patch never contains dangling references when applied in
 * array order.
 */
export type Op = InsertOp | UpdateOp | MoveOp | RemoveOp;
