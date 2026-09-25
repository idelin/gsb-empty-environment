// Derivation: from one HeaderState compute spans, the keyboard tree and the
// leaf->data mapping in a single pass set, so a snapshot is always
// internally consistent. No DOM, no layout engine — pure tree math.
import type {
  GroupNode,
  HeaderCell,
  HeaderSnapshot,
  HeaderState,
  KeyboardEdge,
  VisibleColumn,
} from './types.ts';

interface VisibleGroup {
  kind: 'group';
  id: string;
  label: string;
  children: VisibleNode[];
}
interface VisibleLeaf {
  kind: 'leaf';
  columnId: string;
}
type VisibleNode = VisibleGroup | VisibleLeaf;

/**
 * Prune hidden leaves and any group left without visible leaves. An empty
 * group disappears entirely, so it can never leave a zero-colspan cell.
 */
function prune(state: HeaderState): VisibleGroup | null {
  const walk = (node: GroupNode): VisibleGroup | null => {
    const children: VisibleNode[] = [];
    for (const child of node.children) {
      if (child.kind === 'leaf') {
        if (!state.hidden.has(child.columnId)) {
          children.push({ kind: 'leaf', columnId: child.columnId });
        }
      } else {
        const pruned = walk(child);
        if (pruned) children.push(pruned);
      }
    }
    return children.length > 0
      ? { kind: 'group', id: node.id, label: node.label, children }
      : null;
  };
  return walk(state.root);
}

function visibleLeavesUnder(state: HeaderState, node: VisibleNode): string[] {
  if (node.kind === 'leaf') return [node.columnId];
  const ids: string[] = [];
  for (const child of node.children) ids.push(...visibleLeavesUnder(state, child));
  return ids;
}

/** Height of a subtree: leaves and collapsed groups have height 0. */
function heightOf(state: HeaderState, node: VisibleNode): number {
  if (node.kind === 'leaf') return 0;
  if (state.collapsed.has(node.id)) return 0;
  let max = 0;
  for (const child of node.children) {
    max = Math.max(max, heightOf(state, child));
  }
  return max + 1;
}

export function deriveSnapshot(state: HeaderState, version: number): HeaderSnapshot {
  const cells: HeaderCell[] = [];
  const visibleColumns: VisibleColumn[] = [];
  const dataIndexByColumnId = new Map<string, number>();

  const root = prune(state);
  // The root itself is not rendered; its height is exactly the row count.
  const rows = root ? heightOf(state, root) : 0;

  const emitColumn = (
    columnId: string,
    ownerGroupId: string,
    coveredColumnIds: string[],
    collapsedGroupId?: string,
  ): void => {
    const dataIndex = visibleColumns.length;
    visibleColumns.push({
      dataIndex,
      columnId,
      cellId: collapsedGroupId ? `g:${collapsedGroupId}` : `c:${columnId}`,
      ownerGroupId,
      collapsedGroupId,
      coveredColumnIds,
    });
    for (const id of coveredColumnIds) dataIndexByColumnId.set(id, dataIndex);
  };

  const walk = (
    node: VisibleNode,
    row: number,
    col: number,
    ownerGroupId: string,
  ): number => {
    if (node.kind === 'leaf') {
      const column = state.columns.get(node.columnId);
      if (!column) throw new Error(`column not registered: ${node.columnId}`);
      cells.push({
        id: `c:${node.columnId}`,
        kind: 'leaf',
        label: column.label,
        row,
        col,
        rowSpan: rows - row,
        colSpan: 1,
        columnId: node.columnId,
        coveredColumnIds: [node.columnId],
      });
      emitColumn(node.columnId, ownerGroupId, [node.columnId]);
      return 1;
    }
    if (state.collapsed.has(node.id)) {
      const covered = visibleLeavesUnder(state, node);
      cells.push({
        id: `g:${node.id}`,
        kind: 'group',
        label: node.label,
        row,
        col,
        rowSpan: rows - row,
        colSpan: 1,
        groupId: node.id,
        collapsed: true,
        coveredColumnIds: covered,
      });
      emitColumn(covered[0], node.id, covered, node.id);
      return 1;
    }
    let width = 0;
    const childRow = row + 1;
    for (const child of node.children) {
      width += walk(child, childRow, col + width, node.id);
    }
    cells.push({
      id: `g:${node.id}`,
      kind: 'group',
      label: node.label,
      row,
      col,
      rowSpan: 1,
      colSpan: width,
      groupId: node.id,
      collapsed: false,
      coveredColumnIds: visibleLeavesUnder(state, node),
    });
    return width;
  };

  let columnCount = 0;
  if (root) {
    for (const child of root.children) {
      columnCount += walk(child, 0, columnCount, root.id);
    }
  }

  // Occupancy grid: every (row, col) slot belongs to exactly one cell.
  const grid: string[][] = Array.from({ length: rows }, () =>
    Array.from({ length: columnCount }, () => ''),
  );
  for (const cell of cells) {
    for (let r = cell.row; r < cell.row + cell.rowSpan; r++) {
      for (let c = cell.col; c < cell.col + cell.colSpan; c++) {
        grid[r][c] = cell.id;
      }
    }
  }

  // Keyboard tree: geometric neighbours on the occupancy grid.
  const keyboard: Record<string, KeyboardEdge> = {};
  const at = (r: number, c: number): string | null =>
    r >= 0 && r < rows && c >= 0 && c < columnCount ? grid[r][c] : null;
  for (const cell of cells) {
    const up = at(cell.row - 1, cell.col);
    const down = at(cell.row + cell.rowSpan, cell.col);
    const left = at(cell.row, cell.col - 1);
    const right = at(cell.row, cell.col + cell.colSpan);
    keyboard[cell.id] = {
      up: up === cell.id ? null : up,
      down: down === cell.id ? null : down,
      left: left === cell.id ? null : left,
      right: right === cell.id ? null : right,
    };
  }

  return {
    version,
    rows,
    columnCount,
    cells,
    grid,
    keyboard,
    visibleColumns,
    dataIndexByColumnId,
  };
}
