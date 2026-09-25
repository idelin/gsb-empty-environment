// Core model types. A header is a tree: groups contain groups or leaf
// references. Leaf columns live in a registry keyed by column id, so two
// columns may share a label while keeping distinct identities.

export interface LeafColumn {
  id: string;
  label: string;
  width: number; // glyph-metric width supplied by fixtures, never measured via DOM
}

export interface LeafNode {
  kind: 'leaf';
  columnId: string;
}

export interface GroupNode {
  kind: 'group';
  id: string;
  label: string;
  children: HeaderNode[];
}

export type HeaderNode = LeafNode | GroupNode;

/** Immutable-ish document state. Every commit produces a fresh value. */
export interface HeaderState {
  root: GroupNode;
  columns: ReadonlyMap<string, LeafColumn>;
  hidden: ReadonlySet<string>; // column ids
  collapsed: ReadonlySet<string>; // group ids
}

export interface HeaderCell {
  id: string; // "c:<columnId>" for leaves, "g:<groupId>" for groups
  kind: 'leaf' | 'group';
  label: string;
  row: number;
  col: number;
  rowSpan: number;
  colSpan: number;
  columnId?: string; // set for leaf cells
  groupId?: string; // set for group cells
  collapsed?: boolean; // collapsed group renders as a single column
  /** Leaf column ids this cell stands for (1 for leaves, N for collapsed groups). */
  coveredColumnIds: string[];
}

export interface VisibleColumn {
  dataIndex: number; // position in the data row array
  columnId: string; // representative column (first visible leaf for collapsed groups)
  cellId: string; // header cell owning this data column
  ownerGroupId: string; // explicit ownership: parent group, or the collapsed group itself
  collapsedGroupId?: string;
  coveredColumnIds: string[];
}

export interface KeyboardEdge {
  up: string | null;
  down: string | null;
  left: string | null;
  right: string | null;
}

/**
 * Atomically published view of one version: spans, keyboard tree and the
 * leaf->data-column mapping are always derived together from one state.
 */
export interface HeaderSnapshot {
  version: number;
  rows: number;
  columnCount: number; // === visibleColumns.length === number of data columns
  cells: HeaderCell[];
  /** grid[row][col] -> cell id occupying that slot (every slot is covered). */
  grid: string[][];
  keyboard: Record<string, KeyboardEdge>;
  visibleColumns: VisibleColumn[];
  dataIndexByColumnId: ReadonlyMap<string, number>;
}
