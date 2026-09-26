export type RowId = string;

/**
 * A scroll anchor: a stable row id plus the pixel offset from the row's
 * top edge to the top of the viewport. Survives inserts, deletes, moves
 * and height corrections as long as the row itself stays visible.
 */
export interface Anchor {
  id: RowId;
  offset: number;
}

export interface VisibleRange {
  startIndex: number;
  endIndex: number; // exclusive
  ids: RowId[];
}

/**
 * Content tree used for measurement. Heights are computed purely from
 * glyph metrics and the container width; no browser layout engine is
 * involved.
 */
export type ContentNode =
  | { kind: 'text'; text: string }
  | { kind: 'image'; width: number; height: number }
  | { kind: 'spacer'; height: number }
  | { kind: 'stack'; children: ContentNode[]; gap: number };
