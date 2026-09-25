import type { GlyphMetrics } from './measure.ts';
import { columnWidth } from './measure.ts';
import type {
  HeaderDoc,
  HeaderNode,
  HeaderSnapshot,
  HeaderCell,
  ColumnInfo,
  NavLinks,
} from './types.ts';

interface VisibleNode {
  id: string;
  kind: 'group' | 'leaf' | 'group-summary';
  title: string;
  children: VisibleNode[];
}

export function countVisibleLeaves(node: HeaderNode, hidden: ReadonlySet<string>): number {
  if (node.kind === 'leaf') return hidden.has(node.id) ? 0 : 1;
  let count = 0;
  for (const child of node.children) count += countVisibleLeaves(child, hidden);
  return count;
}

/**
 * Prune the document tree into the visible tree.
 * A group with zero visible leaves disappears entirely (no invalid spans).
 * A collapsed group becomes a terminal "group-summary" column.
 */
function buildVisible(node: HeaderNode, doc: HeaderDoc): VisibleNode | null {
  if (node.kind === 'leaf') {
    if (doc.hidden.has(node.id)) return null;
    return { id: node.id, kind: 'leaf', title: node.name, children: [] };
  }
  if (countVisibleLeaves(node, doc.hidden) === 0) return null;
  if (doc.collapsed.has(node.id)) {
    return { id: node.id, kind: 'group-summary', title: node.name, children: [] };
  }
  const children: VisibleNode[] = [];
  for (const child of node.children) {
    const visible = buildVisible(child, doc);
    if (visible !== null) children.push(visible);
  }
  return { id: node.id, kind: 'group', title: node.name, children };
}

function maxTerminalRow(node: VisibleNode, row: number): number {
  if (node.kind !== 'group') return row;
  let max = 0;
  for (const child of node.children) max = Math.max(max, maxTerminalRow(child, row + 1));
  return max;
}

export function computeLeafOwner(doc: HeaderDoc): Record<string, string[]> {
  const owner: Record<string, string[]> = {};
  const walk = (node: HeaderNode, path: string[]): void => {
    if (node.kind === 'leaf') {
      owner[node.id] = path;
      return;
    }
    const next = node.id === doc.root.id ? path : [...path, node.id];
    for (const child of node.children) walk(child, next);
  };
  walk(doc.root, []);
  return owner;
}

export function buildNav(
  cells: HeaderCell[],
  depth: number,
  columnCount: number,
): Record<string, NavLinks> {
  const grid: (string | null)[][] = [];
  for (let r = 0; r < depth; r++) grid.push(new Array<string | null>(columnCount).fill(null));
  for (const cell of cells) {
    for (let r = cell.row; r < cell.row + cell.rowspan; r++) {
      for (let c = cell.col; c < cell.col + cell.colspan; c++) grid[r][c] = cell.key;
    }
  }
  const nav: Record<string, NavLinks> = {};
  for (const cell of cells) {
    nav[cell.key] = {
      up: cell.row > 0 ? grid[cell.row - 1][cell.col] : null,
      down: cell.row + cell.rowspan < depth ? grid[cell.row + cell.rowspan][cell.col] : null,
      left: cell.col > 0 ? grid[cell.row][cell.col - 1] : null,
      right: cell.col + cell.colspan < columnCount ? grid[cell.row][cell.col + cell.colspan] : null,
    };
  }
  return nav;
}

/** Main derivation: recursive tree walk emitting cells, columns, keyboard tree, mapping. */
export function derive(doc: HeaderDoc, version: number, metrics: GlyphMetrics): HeaderSnapshot {
  const visible: VisibleNode[] = [];
  for (const child of doc.root.children) {
    const node = buildVisible(child, doc);
    if (node !== null) visible.push(node);
  }

  let depth = 1;
  for (const node of visible) depth = Math.max(depth, maxTerminalRow(node, 0) + 1);

  const cells: HeaderCell[] = [];
  const columns: ColumnInfo[] = [];
  let col = 0;

  const emit = (node: VisibleNode, row: number): void => {
    if (node.kind === 'group') {
      const start = col;
      for (const child of node.children) emit(child, row + 1);
      cells.push({
        key: `group:${node.id}`,
        nodeId: node.id,
        kind: 'group',
        title: node.title,
        row,
        col: start,
        rowspan: 1,
        colspan: col - start,
        width: 0,
      });
    } else {
      const key = node.kind === 'leaf' ? `leaf:${node.id}` : `group:${node.id}`;
      const width = columnWidth(node.title, metrics);
      cells.push({
        key,
        nodeId: node.id,
        kind: node.kind,
        title: node.title,
        row,
        col,
        rowspan: depth - row,
        colspan: 1,
        width,
      });
      columns.push({ key, kind: node.kind, nodeId: node.id, title: node.title, dataIndex: col, width });
      col += 1;
    }
  };
  for (const node of visible) emit(node, 0);

  for (const cell of cells) {
    if (cell.kind === 'group') {
      let width = 0;
      for (let i = cell.col; i < cell.col + cell.colspan; i++) width += columns[i].width;
      cell.width = width;
    }
  }

  const dataIndexByKey: Record<string, number> = {};
  for (const column of columns) dataIndexByKey[column.key] = column.dataIndex;

  return {
    version,
    depth,
    width: columns.reduce((sum, column) => sum + column.width, 0),
    columns,
    cells,
    nav: buildNav(cells, depth, columns.length),
    dataIndexByKey,
    leafOwner: computeLeafOwner(doc),
  };
}
