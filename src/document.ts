import {
  buildFoldTree,
  flattenFoldTree,
  normalizeFolds,
  validateFolds,
  type FoldNode,
  type FoldRegion,
} from "./folds.ts";
import {
  offsetAt,
  positionAt,
  snapOffset,
  snapRangeOutward,
  type Position,
} from "./utf16.ts";

export interface FoldedDocumentOptions {
  /** Placeholder text substituted for a collapsed fold. Default "⋯". */
  placeholder?: string;
}

export interface EditResult {
  /** Start of the replaced range in the original text (before the edit). */
  originalStart: number;
  /** End of the replaced range in the original text (before the edit). */
  originalEnd: number;
  /** insertedLength - (originalEnd - originalStart). */
  delta: number;
}

interface Segment {
  vStart: number;
  vEnd: number;
  oStart: number;
  oEnd: number;
  kind: "text" | "placeholder";
}

/**
 * A text document with nested folding regions.
 *
 * All offsets — visible and original — are UTF-16 code-unit offsets. Fold
 * boundaries and edit boundaries are snapped outward so a surrogate pair is
 * never split. The document maintains:
 *  - a projection (`visibleText`) where collapsed folds are replaced by a
 *    placeholder, and inner folds of a collapsed fold stay hidden;
 *  - a bidirectional offset mapping between visible and original text;
 *  - structural edits applied in visible coordinates that shift placeholders
 *    and preserve the nesting tree.
 */
export class FoldedDocument {
  #text: string;
  #placeholder: string;
  #roots: FoldNode[] = [];
  #segments: Segment[] | null = null;
  #visible: string | null = null;

  constructor(text: string, options: FoldedDocumentOptions = {}) {
    this.#text = text;
    this.#placeholder = options.placeholder ?? "⋯";
  }

  /** Original (unprojected) text. */
  get text(): string {
    return this.#text;
  }

  /** Projected visible text with placeholders for collapsed folds. */
  get visibleText(): string {
    this.#ensureProjection();
    return this.#visible!;
  }

  /** All fold regions, flattened and sorted (snapshot). */
  get folds(): FoldRegion[] {
    return flattenFoldTree(this.#roots).map((n) => ({
      start: n.start,
      end: n.end,
    }));
  }

  /** Nesting tree snapshot (deep copy, parents before children). */
  get foldTree(): FoldNode[] {
    const copy = (nodes: FoldNode[]): FoldNode[] =>
      nodes.map((n) => ({
        start: n.start,
        end: n.end,
        collapsed: n.collapsed,
        children: copy(n.children),
      }));
    return copy(this.#roots);
  }

  // ------------------------------------------------------------- folds ----

  /**
   * Add a fold region. Boundaries are snapped outward to surrogate-safe
   * offsets. Throws if the region partially intersects an existing fold —
   * use `setFolds(..., { normalize: true })` or `normalizeFolds` to merge
   * instead of reject.
   */
  addFold(start: number, end: number, collapsed = true): FoldRegion {
    const [s, e] = snapRangeOutward(this.#text, start, end);
    if (e <= s) throw new Error(`Empty fold region [${s}, ${e})`);
    if (this.folds.some((r) => r.start === s && r.end === e)) {
      throw new Error(`Duplicate fold region [${s}, ${e})`);
    }
    const regions = [...this.folds, { start: s, end: e }];
    const flags = [
      ...flattenFoldTree(this.#roots).map((n) => n.collapsed),
      collapsed,
    ];
    const sorted = validateFolds(regions); // throws on partial intersection
    const sortedFlags = sorted.map(
      (r) => flags[regions.findIndex((x) => x.start === r.start && x.end === r.end)],
    );
    this.#roots = buildFoldTree(sorted, sortedFlags);
    this.#invalidate();
    return { start: s, end: e };
  }

  /**
   * Replace all folds. With `normalize: true`, partially intersecting regions
   * are merged into their union; otherwise they are rejected.
   */
  setFolds(regions: FoldRegion[], opts: { normalize?: boolean } = {}): void {
    const prepared = regions.map((r) => {
      const [s, e] = snapRangeOutward(this.#text, r.start, r.end);
      return { start: s, end: e };
    });
    const wellFormed = opts.normalize
      ? normalizeFolds(prepared)
      : validateFolds(prepared);
    this.#roots = buildFoldTree(wellFormed);
    this.#invalidate();
  }

  /** Deepest fold node containing `offset` (original coordinates), if any. */
  #findNode(offset: number): FoldNode | null {
    let best: FoldNode | null = null;
    const walk = (nodes: FoldNode[]) => {
      for (const node of nodes) {
        if (node.start <= offset && offset <= node.end) {
          best = node;
          walk(node.children);
        }
      }
    };
    walk(this.#roots);
    return best;
  }

  /** Collapse the deepest fold containing the original offset. */
  collapseAt(offset: number): boolean {
    const node = this.#findNode(offset);
    if (!node || node.collapsed) return false;
    node.collapsed = true;
    this.#invalidate();
    return true;
  }

  /**
   * Expand the deepest collapsed fold containing the original offset.
   * Inner folds keep their own collapsed state.
   */
  expandAt(offset: number): boolean {
    const node = this.#findNode(offset);
    if (!node || !node.collapsed) return false;
    node.collapsed = false;
    this.#invalidate();
    return true;
  }

  collapseAll(): void {
    for (const n of flattenFoldTree(this.#roots)) n.collapsed = true;
    this.#invalidate();
  }

  expandAll(): void {
    for (const n of flattenFoldTree(this.#roots)) n.collapsed = false;
    this.#invalidate();
  }

  // -------------------------------------------------------- projection ----

  #invalidate(): void {
    this.#segments = null;
    this.#visible = null;
  }

  #ensureProjection(): void {
    if (this.#segments !== null) return;
    const segments: Segment[] = [];
    const parts: string[] = [];
    let v = 0;
    const emitText = (oStart: number, oEnd: number) => {
      if (oEnd <= oStart) return;
      const s = this.#text.slice(oStart, oEnd);
      parts.push(s);
      segments.push({ vStart: v, vEnd: v + s.length, oStart, oEnd, kind: "text" });
      v += s.length;
    };
    const emitPlaceholder = (oStart: number, oEnd: number) => {
      parts.push(this.#placeholder);
      segments.push({
        vStart: v,
        vEnd: v + this.#placeholder.length,
        oStart,
        oEnd,
        kind: "placeholder",
      });
      v += this.#placeholder.length;
    };
    const walk = (from: number, to: number, nodes: FoldNode[]) => {
      let pos = from;
      for (const node of nodes) {
        emitText(pos, node.start);
        if (node.collapsed) {
          // Inner folds of a collapsed fold are hidden: no placeholder.
          emitPlaceholder(node.start, node.end);
        } else {
          walk(node.start, node.end, node.children);
        }
        pos = node.end;
      }
      emitText(pos, to);
    };
    walk(0, this.#text.length, this.#roots);
    this.#segments = segments;
    this.#visible = parts.join("");
  }

  /** Segment containing visible offset `v` (end-boundaries belong to the next segment). */
  #segmentAtVisible(v: number): Segment {
    const segs = this.#segments!;
    for (const seg of segs) {
      if (v >= seg.vStart && v < seg.vEnd) return seg;
    }
    return segs[segs.length - 1];
  }

  /** Segment containing original offset `o`. */
  #segmentAtOriginal(o: number): Segment {
    const segs = this.#segments!;
    for (const seg of segs) {
      if (o >= seg.oStart && o < seg.oEnd) return seg;
    }
    return segs[segs.length - 1];
  }

  // ---------------------------------------------------------- mapping ----

  /** Map a visible offset to an original offset. */
  toOriginalOffset(visibleOffset: number): number {
    this.#ensureProjection();
    const v = Math.max(0, Math.min(this.#visible!.length, visibleOffset));
    const seg = this.#segmentAtVisible(v);
    if (seg.kind === "text") return seg.oStart + (v - seg.vStart);
    // Placeholder: snap to the nearer edge of the hidden range.
    return v - seg.vStart <= seg.vEnd - v ? seg.oStart : seg.oEnd;
  }

  /** Map an original offset to a visible offset. */
  toVisibleOffset(originalOffset: number): number {
    this.#ensureProjection();
    const o = Math.max(0, Math.min(this.#text.length, originalOffset));
    const seg = this.#segmentAtOriginal(o);
    if (seg.kind === "text") return seg.vStart + (o - seg.oStart);
    if (o <= seg.oStart) return seg.vStart;
    if (o >= seg.oEnd) return seg.vEnd;
    return o - seg.oStart <= seg.oEnd - o ? seg.vStart : seg.vEnd;
  }

  /**
   * Map a visible selection to the original range it covers. A selection
   * touching a placeholder expands to the fold's full original range.
   */
  toOriginalRange(visibleStart: number, visibleEnd: number): [number, number] {
    this.#ensureProjection();
    let [vs, ve] = visibleStart <= visibleEnd
      ? [visibleStart, visibleEnd]
      : [visibleEnd, visibleStart];
    vs = Math.max(0, Math.min(this.#visible!.length, vs));
    ve = Math.max(0, Math.min(this.#visible!.length, ve));

    const startSeg = this.#segmentAtVisible(vs);
    const oStart =
      startSeg.kind === "placeholder"
        ? startSeg.oStart
        : startSeg.oStart + (vs - startSeg.vStart);

    let oEnd: number;
    if (ve === vs) {
      oEnd = oStart;
    } else {
      const endSeg = this.#segmentAtVisible(ve - 1);
      oEnd =
        endSeg.kind === "placeholder"
          ? endSeg.oEnd
          : endSeg.oStart + (ve - endSeg.vStart);
    }
    return snapRangeOutward(this.#text, oStart, oEnd);
  }

  /** Map an original range to the visible range covering it. */
  toVisibleRange(originalStart: number, originalEnd: number): [number, number] {
    const [os, oe] =
      originalStart <= originalEnd
        ? [originalStart, originalEnd]
        : [originalEnd, originalStart];
    return [this.toVisibleOffset(os), this.toVisibleOffset(oe)];
  }

  // ------------------------------------------------------------ lines ----

  /** Line/character position of a visible offset (LF and CRLF aware). */
  visiblePositionAt(visibleOffset: number): Position {
    return positionAt(this.visibleText, visibleOffset);
  }

  /** Visible offset of a line/character position (LF and CRLF aware). */
  visibleOffsetAt(position: Position): number {
    return offsetAt(this.visibleText, position);
  }

  /** Line/character position of an original offset. */
  originalPositionAt(originalOffset: number): Position {
    return positionAt(this.#text, originalOffset);
  }

  /** Original offset of a line/character position. */
  originalOffsetAt(position: Position): number {
    return offsetAt(this.#text, position);
  }

  // ------------------------------------------------------------ edits ----

  /**
   * Replace `visibleText[visibleStart..visibleEnd)` with `inserted`.
   *
   * The range is mapped to original coordinates first, so deleting a
   * placeholder deletes the whole folded range (and its folds). Surviving
   * folds are shifted by the edit delta; folds containing the edit grow or
   * shrink; folds only partially overlapped by a deletion are removed while
   * their surviving children keep their nesting state.
   */
  applyVisibleEdit(
    visibleStart: number,
    visibleEnd: number,
    inserted: string,
  ): EditResult {
    const vs = snapOffset(this.visibleText, visibleStart, "start");
    const ve = snapOffset(this.visibleText, visibleEnd, "end");
    const [os, oe] = this.toOriginalRange(Math.min(vs, ve), Math.max(vs, ve));

    const delta = inserted.length - (oe - os);
    this.#text = this.#text.slice(0, os) + inserted + this.#text.slice(oe);

    const survivors: FoldRegion[] = [];
    const flags: boolean[] = [];
    for (const node of flattenFoldTree(this.#roots)) {
      let { start, end } = node;
      if (end <= os) {
        // Entirely before the edit (insertion at `end` stays outside).
      } else if (start >= oe) {
        // Entirely after the edit (insertion at `start` stays outside).
        start += delta;
        end += delta;
      } else if (os <= start && oe >= end) {
        continue; // Fully deleted: drop the fold (children are dropped too).
      } else if (os >= start && oe <= end) {
        end += delta; // Edit strictly inside the fold: grow/shrink.
      } else {
        continue; // Partially overlapped by a deletion: drop, keep children.
      }
      survivors.push({ start, end });
      flags.push(node.collapsed);
    }

    const sorted = validateFolds(survivors);
    const sortedFlags = sorted.map(
      (r) =>
        flags[
          survivors.findIndex((x) => x.start === r.start && x.end === r.end)
        ],
    );
    this.#roots = buildFoldTree(sorted, sortedFlags);
    this.#invalidate();
    return { originalStart: os, originalEnd: oe, delta };
  }
}
