import type { Document, GlyphMetrics } from "./model.ts";
import { breakLines, footnoteHeight } from "./measure.ts";

export type Item =
  | {
      type: "line";
      nodeId: string;
      lineIndex: number;
      lineCount: number;
      height: number;
      footnoteRefs: string[];
    }
  | { type: "heading"; nodeId: string; height: number }
  | { type: "block"; nodeId: string; height: number };

export const HEADING_HEIGHT_LINES = 2;

export function flatten(doc: Document, metrics: GlyphMetrics, lineWidth: number): Item[] {
  const items: Item[] = [];
  for (const node of doc.nodes) {
    if (node.kind === "paragraph") {
      const lines = breakLines(node.spans, metrics, lineWidth);
      lines.forEach((line, idx) =>
        items.push({
          type: "line",
          nodeId: node.id,
          lineIndex: idx,
          lineCount: lines.length,
          height: line.height,
          footnoteRefs: line.footnoteRefs,
        }),
      );
    } else if (node.kind === "heading") {
      items.push({
        type: "heading",
        nodeId: node.id,
        height: metrics.lineHeight * HEADING_HEIGHT_LINES,
      });
    } else {
      items.push({ type: "block", nodeId: node.id, height: node.height });
    }
  }
  return items;
}

export function footnoteHeights(
  doc: Document,
  metrics: GlyphMetrics,
  lineWidth: number,
): Map<string, number> {
  const map = new Map<string, number>();
  for (const fn of doc.footnotes) {
    map.set(fn.id, footnoteHeight(fn.text, metrics, lineWidth));
  }
  return map;
}
