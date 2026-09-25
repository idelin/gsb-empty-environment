export interface LeafNode {
  kind: 'leaf';
  id: string;
  name: string;
}

export interface GroupNode {
  kind: 'group';
  id: string;
  name: string;
  children: HeaderNode[];
}

export type HeaderNode = LeafNode | GroupNode;

/** The editable document: ownership lives in the tree; hidden/collapsed are facets. */
export interface HeaderDoc {
  root: GroupNode;
  hidden: Set<string>;
  collapsed: Set<string>;
}

export type CellKind = 'group' | 'leaf' | 'group-summary';

export interface HeaderCell {
  key: string;
  nodeId: string;
  kind: CellKind;
  title: string;
  row: number;
  col: number;
  rowspan: number;
  colspan: number;
  width: number;
}

export interface ColumnInfo {
  key: string;
  kind: 'leaf' | 'group-summary';
  nodeId: string;
  title: string;
  dataIndex: number;
  width: number;
}

export type Direction = 'up' | 'down' | 'left' | 'right';

export interface NavLinks {
  up: string | null;
  down: string | null;
  left: string | null;
  right: string | null;
}

/** Immutable, atomically published view: spans + keyboard tree + data mapping. */
export interface HeaderSnapshot {
  version: number;
  depth: number;
  width: number;
  columns: ColumnInfo[];
  cells: HeaderCell[];
  nav: Record<string, NavLinks>;
  dataIndexByKey: Record<string, number>;
  leafOwner: Record<string, string[]>;
}
