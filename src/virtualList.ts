import { HeightIndex } from './heightIndex.ts';
import { measureNode, type GlyphMetrics } from './metrics.ts';
import type { Anchor, ContentNode, RowId, VisibleRange } from './types.ts';

export interface RowInput {
  id: RowId;
  node: ContentNode;
  groupId?: string | null;
}

interface RowState {
  id: RowId;
  node: ContentNode;
  groupId: string | null;
}

export interface VirtualListOptions {
  estimatedHeight: number;
  viewportHeight: number;
  measureWidth: number;
  metrics: GlyphMetrics;
}

interface SavedAnchor {
  anchor: Anchor;
  visibleIndex: number;
}

/**
 * Variable-height virtual list with anchor-stable scrolling.
 *
 * Public fallback rules (applied in this order after every mutation):
 *  1. If the anchor row is still visible, keep its visual position:
 *     scrollTop = newOffsetOf(anchor.id) + anchor.offset. Inserts,
 *     deletes, reorders and height corrections above the anchor shift
 *     scrollTop by exactly their pixel delta (e.g. +100px inserted above
 *     the anchor moves scrollTop by +100).
 *  2. If the anchor row was deleted or hidden (collapsed group), the
 *     anchor moves to the next surviving visible row (successor); if
 *     none exists, to the previous surviving visible row (predecessor).
 *  3. Otherwise the row that slid into the anchor's old slot is used;
 *     for an empty list scrollTop becomes 0.
 *  4. A measurement is applied only when its epoch token still matches
 *     the row's current epoch and the row still exists. Late (stale)
 *     measurements are discarded and never corrupt the index.
 */
export class VirtualList {
  private rows: RowState[] = [];
  private byId = new Map<RowId, RowState>();
  private collapsedGroups = new Set<string>();
  private visibleIds: RowId[] = [];
  private index: HeightIndex;
  private measured = new Map<RowId, number>();
  private epochs = new Map<RowId, number>();
  private pendingFallback: RowId | null = null;
  private measureWidth: number;
  private readonly metrics: GlyphMetrics;
  readonly viewportHeight: number;
  scrollTop = 0;

  constructor(options: VirtualListOptions, rows: RowInput[] = []) {
    this.index = new HeightIndex(options.estimatedHeight);
    this.viewportHeight = options.viewportHeight;
    this.measureWidth = options.measureWidth;
    this.metrics = options.metrics;
    if (rows.length > 0) this.insertRows(0, rows);
  }

  get version(): number {
    return this.index.version;
  }

  get size(): number {
    return this.rows.length;
  }

  get visibleCount(): number {
    return this.visibleIds.length;
  }

  getRows(): readonly RowState[] {
    return this.rows;
  }

  collapsedGroupIds(): ReadonlySet<string> {
    return this.collapsedGroups;
  }

  measuredHeights(): ReadonlyMap<RowId, number> {
    return this.measured;
  }

  visibleRowIds(): readonly RowId[] {
    return this.visibleIds;
  }

  epochOf(id: RowId): number {
    return this.epochs.get(id) ?? 0;
  }

  totalHeight(): number {
    return this.index.totalHeight();
  }

  /** Top edge of a visible row, or null when the row is not visible. */
  offsetOfId(id: RowId): number | null {
    const i = this.indexOfVisible(id);
    return i === -1 ? null : this.index.offsetOf(i);
  }

  private heightFor(id: RowId): number {
    return this.measured.get(id) ?? this.index.estimatedHeight;
  }

  private isVisible(row: RowState): boolean {
    return row.groupId === null || !this.collapsedGroups.has(row.groupId);
  }

  private indexOfVisible(id: RowId): number {
    return this.visibleIds.indexOf(id);
  }

  private maxScroll(): number {
    return Math.max(0, this.index.totalHeight() - this.viewportHeight);
  }

  private clampScroll(y: number): number {
    return Math.min(Math.max(y, 0), this.maxScroll());
  }

  private captureAnchor(): SavedAnchor | null {
    const i = this.index.indexAtOffset(this.scrollTop);
    if (i === -1) return null;
    return {
      anchor: { id: this.visibleIds[i], offset: this.scrollTop - this.index.offsetOf(i) },
      visibleIndex: i,
    };
  }

  private restoreAnchor(saved: SavedAnchor | null): void {
    if (!saved) {
      this.scrollTop = this.clampScroll(this.scrollTop);
      return;
    }
    let i = this.indexOfVisible(saved.anchor.id);
    if (i === -1 && this.pendingFallback !== null) {
      i = this.indexOfVisible(this.pendingFallback);
    }
    if (i === -1) {
      i = Math.min(saved.visibleIndex, this.index.length - 1);
    }
    if (i < 0) {
      this.scrollTop = 0;
      return;
    }
    const offset = Math.min(saved.anchor.offset, this.index.heightAt(i));
    this.scrollTop = this.clampScroll(this.index.offsetOf(i) + offset);
  }

  private mutate(fn: (saved: SavedAnchor | null) => void): void {
    const saved = this.captureAnchor();
    this.pendingFallback = null;
    fn(saved);
    this.restoreAnchor(saved);
    this.pendingFallback = null;
  }

  /** First surviving visible row after the anchor, else the last one before it. */
  private fallbackForHiddenAnchor(anchorId: RowId, hidden: Set<RowId>): RowId | null {
    const vis = this.visibleIds;
    const at = vis.indexOf(anchorId);
    for (let i = at + 1; i < vis.length; i++) {
      if (!hidden.has(vis[i])) return vis[i];
    }
    for (let i = at - 1; i >= 0; i--) {
      if (!hidden.has(vis[i])) return vis[i];
    }
    return null;
  }

  private visiblePositionOfModelIndex(modelIndex: number): number {
    let pos = 0;
    for (let k = 0; k < modelIndex; k++) {
      if (this.isVisible(this.rows[k])) pos++;
    }
    return pos;
  }

  insertRows(modelIndex: number, inputs: RowInput[]): void {
    if (inputs.length === 0) return;
    for (const input of inputs) {
      if (this.byId.has(input.id)) {
        throw new Error(`duplicate row id: ${input.id}`);
      }
    }
    this.mutate(() => {
      const at = Math.max(0, Math.min(modelIndex, this.rows.length));
      const visPos = this.visiblePositionOfModelIndex(at);
      const states: RowState[] = inputs.map((input) => ({
        id: input.id,
        node: input.node,
        groupId: input.groupId ?? null,
      }));
      this.rows.splice(at, 0, ...states);
      for (const state of states) this.byId.set(state.id, state);
      const fresh = states.filter((state) => this.isVisible(state));
      if (fresh.length > 0) {
        const ids = fresh.map((state) => state.id);
        this.visibleIds.splice(visPos, 0, ...ids);
        this.index.insertRows(visPos, ids.map((id) => this.heightFor(id)));
      }
    });
  }

  removeRows(ids: RowId[]): void {
    const toRemove = new Set(ids.filter((id) => this.byId.has(id)));
    if (toRemove.size === 0) return;
    this.mutate((saved) => {
      if (saved && toRemove.has(saved.anchor.id)) {
        this.pendingFallback = this.fallbackForHiddenAnchor(saved.anchor.id, toRemove);
      }
      const positions: number[] = [];
      this.visibleIds.forEach((id, pos) => {
        if (toRemove.has(id)) positions.push(pos);
      });
      for (let k = positions.length - 1; k >= 0; k--) {
        this.index.removeRows(positions[k], 1);
      }
      this.visibleIds = this.visibleIds.filter((id) => !toRemove.has(id));
      this.rows = this.rows.filter((row) => !toRemove.has(row.id));
      for (const id of toRemove) {
        this.byId.delete(id);
        this.measured.delete(id);
        this.epochs.delete(id);
      }
    });
  }

  /** Reorder: move a row to a new position in model order. */
  moveRow(id: RowId, newModelIndex: number): void {
    const from = this.rows.findIndex((row) => row.id === id);
    if (from === -1) return;
    const to = Math.max(0, Math.min(newModelIndex, this.rows.length - 1));
    if (from === to) return;
    this.mutate(() => {
      const visFrom = this.indexOfVisible(id);
      const [row] = this.rows.splice(from, 1);
      this.rows.splice(to, 0, row);
      const visibleNow = this.isVisible(row);
      if (visFrom !== -1) {
        this.visibleIds.splice(visFrom, 1);
        this.index.removeRows(visFrom, 1);
      }
      if (visibleNow) {
        const visTo = this.visiblePositionOfModelIndex(to);
        this.visibleIds.splice(visTo, 0, id);
        this.index.insertRows(visTo, [this.heightFor(id)]);
      }
    });
  }

  setCollapsed(groupId: string, collapsed: boolean): void {
    if (this.collapsedGroups.has(groupId) === collapsed) return;
    this.mutate((saved) => {
      if (collapsed) {
        const hidden = new Set(
          this.rows.filter((row) => row.groupId === groupId).map((row) => row.id),
        );
        if (hidden.size === 0) {
          this.collapsedGroups.add(groupId);
          return;
        }
        if (saved && hidden.has(saved.anchor.id)) {
          this.pendingFallback = this.fallbackForHiddenAnchor(saved.anchor.id, hidden);
        }
        const positions: number[] = [];
        this.visibleIds.forEach((id, pos) => {
          if (hidden.has(id)) positions.push(pos);
        });
        for (let k = positions.length - 1; k >= 0; k--) {
          this.index.removeRows(positions[k], 1);
        }
        this.visibleIds = this.visibleIds.filter((id) => !hidden.has(id));
        this.collapsedGroups.add(groupId);
      } else {
        this.collapsedGroups.delete(groupId);
        for (let m = 0; m < this.rows.length; m++) {
          const row = this.rows[m];
          if (row.groupId !== groupId) continue;
          const visPos = this.visiblePositionOfModelIndex(m);
          this.visibleIds.splice(visPos, 0, row.id);
          this.index.insertRows(visPos, [this.heightFor(row.id)]);
        }
      }
    });
  }

  /** Begin a measurement cycle for a row; returns its new epoch token. */
  requestMeasurement(id: RowId): number {
    const next = this.epochOf(id) + 1;
    this.epochs.set(id, next);
    return next;
  }

  /**
   * Apply a measured height. Returns false (discarding the value) when the
   * token is stale or the row is gone; see public fallback rule 4.
   */
  applyMeasurement(id: RowId, height: number, token: number): boolean {
    if (!this.byId.has(id)) return false;
    if (this.epochOf(id) !== token) return false;
    this.mutate(() => {
      this.measured.set(id, height);
      const vi = this.indexOfVisible(id);
      if (vi !== -1) this.index.setHeight(vi, height);
    });
    return true;
  }

  /** Measure a row's content tree asynchronously, then apply if still fresh. */
  async measureAsync(
    id: RowId,
    measurer: (node: ContentNode, width: number, metrics: GlyphMetrics) => number | Promise<number> =
      measureNode,
  ): Promise<boolean> {
    const row = this.byId.get(id);
    if (!row) return false;
    const token = this.requestMeasurement(id);
    const height = await measurer(row.node, this.measureWidth, this.metrics);
    return this.applyMeasurement(id, height, token);
  }

  /** Changing the measure width invalidates every in-flight measurement. */
  setMeasureWidth(width: number): void {
    if (width === this.measureWidth) return;
    this.measureWidth = width;
    for (const id of this.byId.keys()) this.requestMeasurement(id);
  }

  scrollTo(y: number): void {
    this.scrollTop = this.clampScroll(y);
  }

  /** Negative deltas scroll in reverse, towards the list start. */
  scrollBy(delta: number): void {
    this.scrollTo(this.scrollTop + delta);
  }

  jumpToIndex(i: number, align: 'top' | 'center' = 'top'): void {
    if (i < 0 || i >= this.index.length) return;
    const top = this.index.offsetOf(i);
    const y =
      align === 'top' ? top : top - (this.viewportHeight - this.index.heightAt(i)) / 2;
    this.scrollTo(y);
  }

  jumpToId(id: RowId, align: 'top' | 'center' = 'top'): void {
    const i = this.indexOfVisible(id);
    if (i !== -1) this.jumpToIndex(i, align);
  }

  /**
   * Rows intersecting [scrollTop, scrollTop + viewportHeight). Zero-height
   * rows are included only when their top edge lies strictly inside the
   * window; the row at the exact bottom edge is excluded.
   */
  visibleRange(): VisibleRange {
    const start = this.index.indexAtOffset(this.scrollTop);
    if (start === -1) return { startIndex: 0, endIndex: 0, ids: [] };
    const limit = this.scrollTop + this.viewportHeight;
    let end = start;
    while (end < this.index.length && this.index.offsetOf(end) < limit) end++;
    return { startIndex: start, endIndex: end, ids: this.visibleIds.slice(start, end) };
  }
}
