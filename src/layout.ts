/** Pure layout + hit-testing computed from fixture glyph metrics.
 *  No browser layout engine is involved: row geometry derives only
 *  from the metrics fixture and the tree shape. */
import type { Location, NodeId, Tree } from './types.ts';
import { depthOf, flatten, subtreeIds } from './tree.ts';

export interface GlyphMetrics {
  charWidth: number;
  rowHeight: number;
  indentWidth: number;
}

export interface RowRect {
  id: NodeId;
  depth: number;
  x: number;
  y: number;
  width: number;
  height: number;
}

export function computeLayout(tree: Tree, metrics: GlyphMetrics): RowRect[] {
  return flatten(tree).map((row, i) => ({
    id: row.id,
    depth: row.depth,
    x: row.depth * metrics.indentWidth,
    y: i * metrics.rowHeight,
    width: row.depth * metrics.indentWidth + tree.nodes[row.id].label.length * metrics.charWidth,
    height: metrics.rowHeight,
  }));
}

/** Which node row contains the point, if any. */
export function hitTestNode(layout: RowRect[], x: number, y: number): NodeId | null {
  for (const r of layout) {
    if (y >= r.y && y < r.y + r.height && x >= r.x && x <= r.x + r.width) return r.id;
  }
  return null;
}

/** Map a pointer position to a drop location.
 *  Returns a location in *pre-removal* coordinates (the state machine
 *  normalizes them), or null when no valid parent exists at that depth. */
export function dropLocationAt(
  tree: Tree,
  metrics: GlyphMetrics,
  x: number,
  y: number,
): (Location & { depth: number }) | null {
  const rows = flatten(tree);
  const rowCount = rows.length;
  if (rowCount === 0) return { parentId: tree.rootId, index: 0, depth: 0 };
  let row = Math.floor(y / metrics.rowHeight);
  row = Math.max(0, Math.min(row, rowCount)); // insertion happens *before* this row
  const maxDepth = rows.reduce((m, r) => Math.max(m, r.depth), 0);
  const depth = Math.max(0, Math.min(Math.round(x / metrics.indentWidth), maxDepth + 1));

  let parentId: NodeId;
  if (depth === 0) {
    parentId = tree.rootId;
  } else {
    parentId = '';
    for (let i = Math.min(row, rowCount) - 1; i >= 0; i--) {
      if (rows[i].depth === depth - 1) { parentId = rows[i].id; break; }
    }
    if (parentId === '') return null;
  }
  let index = 0;
  for (let i = 0; i < row; i++) {
    if (tree.nodes[rows[i].id].parentId === parentId) index += 1;
  }
  return { parentId, index, depth };
}

/** Inverse of dropLocationAt: a pointer position that maps back to the
 *  given location. `loc` is in post-removal coordinates; `draggedId`
 *  identifies the subtree hidden from the coordinate space (pass an
 *  unknown id when nothing is being dragged). Used by tests and by the
 *  keyboard/mouse parity checks. */
export function pointForLocation(
  tree: Tree,
  metrics: GlyphMetrics,
  loc: Location,
  draggedId: NodeId,
): { x: number; y: number } {
  const flat = flatten(tree);
  const parent = tree.nodes[loc.parentId];
  const siblings = parent.children.filter((id) => id !== draggedId);
  const depth = loc.parentId === tree.rootId ? 0 : depthOf(tree, loc.parentId) + 1;
  let row: number;
  if (loc.index < siblings.length) {
    row = flat.findIndex((r) => r.id === siblings[loc.index]);
  } else if (loc.parentId === tree.rootId) {
    row = flat.length;
  } else {
    const parentRow = flat.findIndex((r) => r.id === loc.parentId);
    row = parentRow + subtreeIds(tree, loc.parentId).length;
  }
  return { x: depth * metrics.indentWidth, y: row * metrics.rowHeight + 1 };
}
