/**
 * Layout node tree. Fixtures hand us this tree; the track sizing engine
 * never touches a DOM or a browser layout engine.
 */

import { measureText, type GlyphMetricsTable } from "./glyph-metrics.ts";

export interface LayoutNode {
  readonly id: string;
  /** 0-based column index the node starts in. */
  readonly column: number;
  /** Number of columns the node spans. Defaults to 1. */
  readonly columnSpan?: number;
  /** Text content; its measured width becomes the node's minimum width. */
  readonly text?: string;
  /** Explicit minimum width override (wins over text measurement). */
  readonly minWidth?: number;
  /** Horizontal padding added around text content. */
  readonly padding?: number;
  readonly children?: readonly LayoutNode[];
}

/** A flattened grid item: the unit the track sizing algorithm reasons about. */
export interface GridItem {
  readonly id: string;
  readonly columnStart: number;
  readonly columnSpan: number;
  readonly minWidth: number;
}

function nodeMinWidth(node: LayoutNode, metrics: GlyphMetricsTable): number {
  if (node.minWidth !== undefined) return node.minWidth;
  const padding = node.padding ?? 0;
  if (node.text !== undefined) {
    return measureText(node.text, metrics) + padding * 2;
  }
  return padding * 2;
}

/** Depth-first flattening of the fixture tree into grid items. */
export function collectItems(
  root: LayoutNode,
  metrics: GlyphMetricsTable,
): GridItem[] {
  const items: GridItem[] = [];
  const visit = (node: LayoutNode): void => {
    items.push({
      id: node.id,
      columnStart: node.column,
      columnSpan: node.columnSpan ?? 1,
      minWidth: nodeMinWidth(node, metrics),
    });
    for (const child of node.children ?? []) visit(child);
  };
  visit(root);
  return items;
}
