// Layout node tree, provided by fixtures in real usage.

export type TrackSize =
  | { kind: "fixed"; px: number }
  | { kind: "content" }
  | { kind: "fraction"; weight: number; min?: number };

export interface LayoutItem {
  id: string;
  // 0-based column start and span (span >= 1).
  column: number;
  columnSpan: number;
  text: string;
  fontSize: number;
}

export interface LayoutContainer {
  // Available inline size of the container in px.
  width: number;
  // Gap between adjacent columns in px.
  gap: number;
  columns: TrackSize[];
  items: LayoutItem[];
}

export function validateContainer(container: LayoutContainer): void {
  if (!Number.isFinite(container.width) || container.width < 0) {
    throw new Error(`container width must be a finite non-negative number, got ${container.width}`);
  }
  if (!Number.isFinite(container.gap) || container.gap < 0) {
    throw new Error(`gap must be a finite non-negative number, got ${container.gap}`);
  }
  const columnCount = container.columns.length;
  if (columnCount === 0) {
    throw new Error("container must declare at least one column");
  }
  for (const [index, track] of container.columns.entries()) {
    if (track.kind === "fixed" && (!Number.isFinite(track.px) || track.px < 0)) {
      throw new Error(`column ${index}: fixed size must be finite and non-negative`);
    }
    if (track.kind === "fraction") {
      if (!Number.isFinite(track.weight) || track.weight < 0) {
        throw new Error(`column ${index}: fraction weight must be finite and non-negative`);
      }
      if (track.min !== undefined && (!Number.isFinite(track.min) || track.min < 0)) {
        throw new Error(`column ${index}: fraction min must be finite and non-negative`);
      }
    }
  }
  for (const item of container.items) {
    if (!Number.isInteger(item.column) || item.column < 0 || item.column >= columnCount) {
      throw new Error(`item ${item.id}: column ${item.column} out of range`);
    }
    if (!Number.isInteger(item.columnSpan) || item.columnSpan < 1) {
      throw new Error(`item ${item.id}: columnSpan must be a positive integer`);
    }
    if (item.column + item.columnSpan > columnCount) {
      throw new Error(`item ${item.id}: span exceeds column count`);
    }
    if (!Number.isFinite(item.fontSize) || item.fontSize <= 0) {
      throw new Error(`item ${item.id}: fontSize must be positive`);
    }
  }
}
