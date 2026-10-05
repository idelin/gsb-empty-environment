/**
 * Core types for the ordered syntax-tree differ.
 */

export interface TreeNode {
  type: string;
  value?: string;
  /** Optional stable identity. Nodes carrying the same id on both sides are
   *  considered the same node (matched with priority). */
  id?: string;
  children: TreeNode[];
}

/** A path addresses a node in the *evolving* tree: child indices from the root. */
export type Path = number[];

export type Op =
  | { op: 'update'; path: Path; type: string; value?: string; id?: string }
  | { op: 'insert'; parent: Path; index: number; node: TreeNode }
  | { op: 'delete'; path: Path }
  | { op: 'move'; from: Path; parent: Path; index: number };

export interface CostModel {
  /** Cost of one update op (type and/or value changed on a matched node). */
  update: number;
  /** Cost per inserted node (inserting a subtree of k nodes costs k*insert). */
  insert: number;
  /** Cost per deleted node (deleting a subtree of k nodes costs k*delete). */
  delete: number;
  /** Cost of relocating one subtree (flat, independent of subtree size). */
  move: number;
}

export const DEFAULT_COSTS: CostModel = { update: 1, insert: 1, delete: 1, move: 1 };

export interface DiffOptions {
  /**
   * Search budget: maximum number of branch-and-bound states expanded while
   * trying to prove / improve the edit script. When the budget is exhausted
   * the best complete patch found so far is returned with `optimal: false`.
   * Defaults to 4000. Set to 0 to skip optimization (greedy patch only).
   */
  budget?: number;
  costs?: Partial<CostModel>;
}

export interface DiffResult {
  /** Complete, applicable edit script. Paths refer to the evolving tree. */
  patch: Op[];
  /** Total weighted cost of `patch` under the cost model. */
  cost: number;
  /**
   * True when the search space was fully explored within budget and `cost`
   * is provably minimal over all matchings. False when the budget interrupted
   * the search: the patch is still complete and usable, just not proven optimal.
   */
  optimal: boolean;
  /** Number of search states expanded (diagnostics). */
  expanded: number;
}
