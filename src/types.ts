export interface RowSpec {
  id: string;
  /** Row height in line-height units (any consistent unit works). */
  height: number;
}

/**
 * An atomic block of one or more rows. A multi-row block represents rows
 * glued together by a merged cell and is never split across pages.
 */
export interface BlockSpec {
  id: string;
  rows: RowSpec[];
}

export interface TableSpec {
  /** Header height; the header is repeated at the top of every page. */
  headerHeight: number;
  blocks: BlockSpec[];
}

export interface PaginateOptions {
  pageHeight: number;
  /** Reserved at the bottom of the last page only. Defaults to 0. */
  footnoteHeight?: number;
}

export interface PlacedRow {
  id: string;
  height: number;
  /** Offset from the top of the page. */
  offsetY: number;
}

export interface PlacedBlock {
  id: string;
  height: number;
  /** Offset from the top of the page (below the repeated header). */
  offsetY: number;
  rows: PlacedRow[];
}

export interface PageLayout {
  index: number;
  headerHeight: number;
  blocks: PlacedBlock[];
  /** Sum of block heights on this page (excludes header and footnote). */
  contentHeight: number;
  /** Non-zero only on the last page. */
  footnoteHeight: number;
  /** headerHeight + contentHeight + footnoteHeight. */
  usedHeight: number;
}

export interface PaginationResult {
  pages: PageLayout[];
  pageCount: number;
  pageHeight: number;
  /** Usable height for blocks on a non-last page: pageHeight - headerHeight. */
  contentCapacity: number;
  /** Usable height for blocks on the last page (footnote reserved). */
  lastPageContentCapacity: number;
}
