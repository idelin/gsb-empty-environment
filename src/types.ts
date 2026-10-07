/** 表格中的一行，高度以排版单位（如 pt / px）计。 */
export interface TableRow {
  id: string;
  height: number;
}

/**
 * 行组：因合并单元格跨行而不可拆分的最小排版单元。
 * 一个行组要么整体落在某页，要么整体移到下一页，绝不跨页拆分。
 */
export interface RowGroup {
  id: string;
  rows: TableRow[];
}

export interface PaginateOptions {
  /** 整页可用高度（含表头与尾注）。 */
  pageHeight: number;
  /** 表头高度，每页重复占用。 */
  headerHeight: number;
  /** 尾注高度，仅在最后一页占用。缺省为 0。 */
  footnoteHeight?: number;
}

export interface Page {
  /** 从 0 开始的页码。 */
  index: number;
  /** 本页包含的行组（保持原始顺序的连续片段）。 */
  groups: RowGroup[];
  /** 本页行组高度之和。 */
  contentHeight: number;
  /** 本页表头占用高度（每页都有）。 */
  headerHeight: number;
  /** 本页尾注占用高度（仅最后一页非 0）。 */
  footnoteHeight: number;
  /** headerHeight + contentHeight + footnoteHeight。 */
  usedHeight: number;
  isLast: boolean;
}
