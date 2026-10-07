export interface TreeNode {
  id: string;
  children?: TreeNode[];
  expanded?: boolean;
  disabled?: boolean;
  /** Hint for lazy trees: row can be expanded even before children load. */
  hasChildren?: boolean;
  [key: string]: unknown;
}

export interface ColumnDef {
  key: string;
  disabled?: boolean;
  [key: string]: unknown;
}

export interface CellAddress {
  rowId: string;
  colKey: string;
}

export interface FlatRow {
  id: string;
  depth: number;
  parentId: string | null;
  node: TreeNode;
}

/** The flat projection of the tree: only visible rows, only live columns. */
export interface VisibleGrid {
  rows: FlatRow[];
  columns: ColumnDef[];
}

export type CellDisabledPredicate = (node: TreeNode, column: ColumnDef) => boolean;

export type ChildrenLoader = (node: TreeNode) => Promise<TreeNode[]>;
