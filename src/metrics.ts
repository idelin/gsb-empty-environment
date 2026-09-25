import type { NodeId, Row } from './types.ts';

/** 字形度量：全部由夹具给出，核心计算不借浏览器布局引擎 */
export interface GlyphMetrics {
  rowHeight: number;
  indentWidth: number;
  labelWidths: Record<NodeId, number>;
}

export interface RowBox {
  id: NodeId;
  depth: number;
  top: number;
  bottom: number;
}

/** 由行高累加得到每行的垂直区间 */
export function layoutRows(rows: Row[], metrics: GlyphMetrics): RowBox[] {
  const boxes: RowBox[] = [];
  let top = 0;
  for (const row of rows) {
    boxes.push({ id: row.id, depth: row.depth, top, bottom: top + metrics.rowHeight });
    top += metrics.rowHeight;
  }
  return boxes;
}

/** 指针 y -> 插入线 gap：行上半部分为行前，下半部分为行后 */
export function gapIndexAt(boxes: RowBox[], y: number): number {
  for (let i = 0; i < boxes.length; i++) {
    const box = boxes[i];
    if (y < box.top) return i;
    if (y < box.bottom) {
      const mid = (box.top + box.bottom) / 2;
      return y < mid ? i : i + 1;
    }
  }
  return boxes.length;
}

/** 指针 x -> 期望缩进层级 */
export function depthAt(x: number, metrics: GlyphMetrics): number {
  return Math.max(0, Math.floor(x / metrics.indentWidth));
}
