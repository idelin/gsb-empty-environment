import type { GlyphMetrics } from './measure.ts';
import { columnWidth } from './measure.ts';
import { countVisibleLeaves } from './derive.ts';
import type {
  HeaderDoc,
  HeaderNode,
  HeaderSnapshot,
  HeaderCell,
  ColumnInfo,
  NavLinks,
} from './types.ts';

interface FlatEntry {
  key: string;
  kind: 'leaf' | 'group-summary';
  nodeId: string;
  title: string;
  /** Ids of the expanded ancestor groups above this terminal, top-down. */
  path: string[];
}

/**
 * Independent reference implementation: instead of recursively emitting the
 * tree, flatten the document into the ordered visible leaf sequence (with
 * group paths), then rebuild the whole grid by merging consecutive entries
 * that share a group id at each level. Used to differentially check any
 * edit sequence against the main derivation.
 */
export function rebuildFromLeafOrder(
  doc: HeaderDoc,
  metrics: GlyphMetrics,
  version: number,
): HeaderSnapshot {
  const entries: FlatEntry[] = [];
  const flatten = (node: HeaderNode, path: string[]): void => {
    if (node.kind === 'leaf') {
      if (!doc.hidden.has(node.id)) {
        entries.push({ key: `leaf:${node.id}`, kind: 'leaf', nodeId: node.id, title: node.name, path });
      }
      return;
    }
    if (node.id !== doc.root.id) {
      if (countVisibleLeaves(node, doc.hidden) === 0) return;
      if (doc.collapsed.has(node.id)) {
        entries.push({ key: `group:${node.id}`, kind: 'group-summary', nodeId: node.id, title: node.name, path });
        return;
      }
      path = [...path, node.id];
    }
    for (const child of node.children) flatten(child, path);
  };
  flatten(doc.root, []);

  let depth = 1;
  for (const entry of entries) depth = Math.max(depth, entry.path.length + 1);

  const columns: ColumnInfo[] = entries.map((entry, index) => ({
    key: entry.key,
    kind: entry.kind,
    nodeId: entry.nodeId,
    title: entry.title,
    dataIndex: index,
    width: columnWidth(entry.title, metrics),
  }));

  const cells: HeaderCell[] = [];
  for (let level = 0; level < depth; level++) {
    let i = 0;
    while (i < entries.length) {
      const entry = entries[i];
      if (entry.path.length < level) {
        i += 1;
        continue;
      }
      if (entry.path.length === level) {
        cells.push({
          key: entry.key,
          nodeId: entry.nodeId,
          kind: entry.kind,
          title: entry.title,
          row: level,
          col: i,
          rowspan: depth - level,
          colspan: 1,
          width: columns[i].width,
        });
        i += 1;
        continue;
      }
      const groupId = entry.path[level];
      let j = i;
      while (j < entries.length && entries[j].path.length > level && entries[j].path[level] === groupId) {
        j += 1;
      }
      let width = 0;
      for (let k = i; k < j; k++) width += columns[k].width;
      cells.push({
        key: `group:${groupId}`,
        nodeId: groupId,
        kind: 'group',
        title: groupTitle(doc, groupId),
        row: level,
        col: i,
        rowspan: 1,
        colspan: j - i,
        width,
      });
      i = j;
    }
  }

  const nav: Record<string, NavLinks> = {};
  const occupancy: (string | null)[][] = [];
  for (let r = 0; r < depth; r++) occupancy.push(new Array<string | null>(entries.length).fill(null));
  for (const cell of cells) {
    for (let r = cell.row; r < cell.row + cell.rowspan; r++) {
      for (let c = cell.col; c < cell.col + cell.colspan; c++) occupancy[r][c] = cell.key;
    }
  }
  for (const cell of cells) {
    nav[cell.key] = {
      up: cell.row > 0 ? occupancy[cell.row - 1][cell.col] : null,
      down: cell.row + cell.rowspan < depth ? occupancy[cell.row + cell.rowspan][cell.col] : null,
      left: cell.col > 0 ? occupancy[cell.row][cell.col - 1] : null,
      right: cell.col + cell.colspan < entries.length ? occupancy[cell.row][cell.col + cell.colspan] : null,
    };
  }

  const dataIndexByKey: Record<string, number> = {};
  for (const column of columns) dataIndexByKey[column.key] = column.dataIndex;

  const leafOwner: Record<string, string[]> = {};
  const own = (node: HeaderNode, path: string[]): void => {
    if (node.kind === 'leaf') {
      leafOwner[node.id] = path;
      return;
    }
    const next = node.id === doc.root.id ? path : [...path, node.id];
    for (const child of node.children) own(child, next);
  };
  own(doc.root, []);

  return {
    version,
    depth,
    width: columns.reduce((sum, column) => sum + column.width, 0),
    columns,
    cells,
    nav,
    dataIndexByKey,
    leafOwner,
  };
}

function groupTitle(doc: HeaderDoc, groupId: string): string {
  const stack: HeaderNode[] = [doc.root];
  while (stack.length > 0) {
    const node = stack.pop()!;
    if (node.kind === 'group' && node.id === groupId) return node.name;
    if (node.kind === 'group') stack.push(...node.children);
  }
  throw new Error(`unknown group: ${groupId}`);
}

function cellShape(cell: HeaderCell): unknown[] {
  return [cell.kind, cell.title, cell.row, cell.col, cell.rowspan, cell.colspan, cell.width];
}

/** Structural comparison of two snapshots; returns a list of mismatches. */
export function diffSnapshots(a: HeaderSnapshot, b: HeaderSnapshot): string[] {
  const problems: string[] = [];
  if (a.depth !== b.depth) problems.push(`depth: ${a.depth} != ${b.depth}`);
  if (a.width !== b.width) problems.push(`width: ${a.width} != ${b.width}`);
  if (JSON.stringify(a.columns) !== JSON.stringify(b.columns)) {
    problems.push(`columns: ${JSON.stringify(a.columns)} != ${JSON.stringify(b.columns)}`);
  }
  const shapeMap = (cells: HeaderCell[]): Record<string, string> => {
    const map: Record<string, string> = {};
    for (const cell of cells) map[cell.key] = JSON.stringify(cellShape(cell));
    return map;
  };
  const shapesA = shapeMap(a.cells);
  const shapesB = shapeMap(b.cells);
  for (const key of new Set([...Object.keys(shapesA), ...Object.keys(shapesB)])) {
    if (shapesA[key] !== shapesB[key]) problems.push(`cell ${key}: ${shapesA[key]} != ${shapesB[key]}`);
  }
  const sortedNav = (nav: HeaderSnapshot['nav']): string =>
    JSON.stringify(
      Object.keys(nav)
        .sort()
        .map((key) => [key, nav[key]]),
    );
  if (sortedNav(a.nav) !== sortedNav(b.nav)) problems.push('nav trees differ');
  if (JSON.stringify(a.dataIndexByKey) !== JSON.stringify(b.dataIndexByKey)) {
    problems.push('data mappings differ');
  }
  if (JSON.stringify(a.leafOwner) !== JSON.stringify(b.leafOwner)) problems.push('leaf owners differ');
  return problems;
}

/** Cross-cutting invariants that must hold for every published snapshot. */
export function checkInvariants(snapshot: HeaderSnapshot): string[] {
  const problems: string[] = [];
  snapshot.columns.forEach((column, index) => {
    if (column.dataIndex !== index) {
      problems.push(`column ${column.key} dataIndex ${column.dataIndex} != position ${index}`);
    }
    if (snapshot.dataIndexByKey[column.key] !== index) {
      problems.push(`dataIndexByKey[${column.key}] != ${index}`);
    }
  });
  for (const cell of snapshot.cells) {
    if (cell.rowspan < 1 || cell.colspan < 1) {
      problems.push(`cell ${cell.key} has invalid span ${cell.rowspan}x${cell.colspan}`);
    }
  }
  for (let i = 0; i < snapshot.columns.length; i++) {
    const terminals = snapshot.cells.filter(
      (cell) => cell.kind !== 'group' && cell.col <= i && i < cell.col + cell.colspan,
    );
    if (terminals.length !== 1 || terminals[0].key !== snapshot.columns[i].key) {
      problems.push(`column ${i} is not covered by exactly its own terminal cell`);
    }
  }
  const cellByKey = new Map(snapshot.cells.map((cell) => [cell.key, cell]));
  for (const [key, links] of Object.entries(snapshot.nav)) {
    const origin = cellByKey.get(key);
    if (origin === undefined) {
      problems.push(`nav entry ${key} has no cell`);
      continue;
    }
    const checks: Array<[keyof NavLinks, (target: HeaderCell) => boolean, string]> = [
      ['left', (t) => t.col + t.colspan <= origin.col, 'must end left of'],
      ['right', (t) => t.col >= origin.col + origin.colspan, 'must start right of'],
      ['up', (t) => t.row + t.rowspan <= origin.row, 'must end above'],
      ['down', (t) => t.row >= origin.row + origin.rowspan, 'must start below'],
    ];
    for (const [dir, ok, message] of checks) {
      const next = links[dir];
      if (next === null) continue;
      const target = cellByKey.get(next);
      if (target === undefined) {
        problems.push(`nav ${key}.${dir} points at unknown cell ${next}`);
      } else if (!ok(target)) {
        problems.push(`nav ${key}.${dir} = ${next} ${message} ${key}`);
      }
    }
  }
  return problems;
}
