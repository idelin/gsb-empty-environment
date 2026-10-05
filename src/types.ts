/** Scalar payload carried by a tree node. */
export type TreeValue = string | number | boolean | null;

/** A node in an ordered tree. `children` order is significant. */
export interface TreeNode {
  type: string;
  value?: TreeValue;
  id?: string;
  children: TreeNode[];
}

/** Absolute position of a node, as child indices from the root. */
export type Path = number[];

/**
 * Replaces the scalar fields of the node at `path`.
 * `type` is always set. `value`/`id` are set when present and removed when absent.
 */
export interface UpdateOp {
  op: 'update';
  path: Path;
  type: string;
  value?: TreeValue;
  id?: string;
}

/** Inserts `node` (a whole subtree) under `parentPath` at child position `index`. */
export interface InsertOp {
  op: 'insert';
  parentPath: Path;
  index: number;
  node: TreeNode;
}

/** Removes the subtree rooted at `path`. The root itself cannot be deleted. */
export interface DeleteOp {
  op: 'delete';
  path: Path;
}

/**
 * Relocates the subtree at `from` to become the `toIndex`-th child of `toParentPath`.
 * `toIndex` is interpreted after the subtree has been detached (post-removal index).
 * The root cannot be moved, and the target parent must not lie inside the moved subtree.
 */
export interface MoveOp {
  op: 'move';
  from: Path;
  toParentPath: Path;
  toIndex: number;
}

export type PatchOp = UpdateOp | InsertOp | DeleteOp | MoveOp;

export interface DiffOptions {
  /**
   * Maximum matching effort, counted in sequence-alignment DP cells.
   * When the budget is exhausted the matcher stops early and the remaining
   * differences are emitted as plain insert/delete/update operations; the
   * returned patch is still complete and applicable, and `optimal` is false.
   * Defaults to Infinity.
   */
  budget?: number;
}

export interface DiffStats {
  matched: number;
  updated: number;
  inserted: number;
  deleted: number;
  moved: number;
}

export interface DiffResult {
  /** Sequential patch; each op addresses the tree state produced by the previous ops. */
  ops: PatchOp[];
  /** False when the budget interrupted the matching search (optimality unproven). */
  optimal: boolean;
  stats: DiffStats;
}
