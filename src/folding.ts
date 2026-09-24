/**
 * Folded text projection.
 *
 * Maintains a set of (possibly nested) fold ranges over an original text and
 * produces a *visible* projection in which every outermost collapsed fold is
 * replaced by a placeholder. All public offsets are UTF-16 code-unit offsets
 * into either the original text or the visible projection.
 */

export interface FoldSpec {
  /** UTF-16 offset of the first code unit hidden by the fold. */
  start: number;
  /** UTF-16 offset one past the last hidden code unit. */
  end: number;
  /** Placeholder shown while collapsed. Defaults to the projection default. */
  placeholder?: string;
  /** Initial collapsed state. Defaults to true. */
  collapsed?: boolean;
}

export interface FoldState {
  id: number;
  start: number;
  end: number;
  placeholder: string;
  collapsed: boolean;
  /** 0 for top-level folds, +1 per enclosing fold. */
  depth: number;
}

export interface ProjectionOptions {
  /**
   * How to handle partially intersecting fold ranges:
   *  - 'throw' (default): reject with a FoldIntersectionError.
   *  - 'merge': normalize by replacing each intersecting cluster with its union.
   */
  onIntersection?: 'throw' | 'merge';
  /** Placeholder used when a fold spec does not provide one. Default '…'. */
  defaultPlaceholder?: string;
}

export class FoldIntersectionError extends Error {
  readonly a: { start: number; end: number };
  readonly b: { start: number; end: number };
  constructor(a: { start: number; end: number }, b: { start: number; end: number }) {
    super(
      `Fold ranges partially intersect: [${a.start}, ${a.end}) and [${b.start}, ${b.end}). ` +
        `Folds must be disjoint or properly nested (or use onIntersection: 'merge').`,
    );
    this.name = 'FoldIntersectionError';
    this.a = a;
    this.b = b;
  }
}

export class SurrogatePairError extends Error {
  constructor(offset: number) {
    super(`Offset ${offset} splits a UTF-16 surrogate pair.`);
    this.name = 'SurrogatePairError';
  }
}

export class FoldRangeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'FoldRangeError';
  }
}

interface Fold {
  id: number;
  start: number;
  end: number;
  placeholder: string;
  collapsed: boolean;
  parent: Fold | null;
}

type Segment =
  | { kind: 'text'; origStart: number; origEnd: number; visStart: number }
  | { kind: 'placeholder'; fold: Fold; visStart: number; visEnd: number };

function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** True when `offset` falls between a high and a low surrogate. */
export function splitsSurrogatePair(text: string, offset: number): boolean {
  return (
    offset > 0 &&
    offset < text.length &&
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

function rangesPartiallyIntersect(
  a: { start: number; end: number },
  b: { start: number; end: number },
): boolean {
  const overlap = a.start < b.end && b.start < a.end;
  const nested = (a.start <= b.start && b.end <= a.end) || (b.start <= a.start && a.end <= b.end);
  return overlap && !nested;
}

export class FoldedTextProjection {
  private text: string;
  private folds: Fold[] = [];
  private nextId = 1;
  private segments: Segment[] = [];
  private visible = '';
  private readonly onIntersection: 'throw' | 'merge';
  private readonly defaultPlaceholder: string;

  constructor(text: string, specs: FoldSpec[] = [], options: ProjectionOptions = {}) {
    this.text = text;
    this.onIntersection = options.onIntersection ?? 'throw';
    this.defaultPlaceholder = options.defaultPlaceholder ?? '…';

    const folds: Fold[] = specs.map((spec) => {
      this.validateRange(spec.start, spec.end);
      return {
        id: this.nextId++,
        start: spec.start,
        end: spec.end,
        placeholder: spec.placeholder ?? this.defaultPlaceholder,
        collapsed: spec.collapsed ?? true,
        parent: null,
      };
    });

    this.folds = this.normalize(folds);
    this.rebuildHierarchy();
    this.rebuild();
  }

  // ------------------------------------------------------------- construction

  private validateRange(start: number, end: number): void {
    if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end > this.text.length || start >= end) {
      throw new FoldRangeError(
        `Invalid fold range [${start}, ${end}) for text of length ${this.text.length}.`,
      );
    }
    if (splitsSurrogatePair(this.text, start)) throw new SurrogatePairError(start);
    if (splitsSurrogatePair(this.text, end)) throw new SurrogatePairError(end);
  }

  /** Reject or merge partially intersecting ranges until the set is laminar. */
  private normalize(folds: Fold[]): Fold[] {
    for (;;) {
      let hit: [number, number] | null = null;
      for (let i = 0; i < folds.length && hit === null; i++) {
        for (let j = i + 1; j < folds.length; j++) {
          if (rangesPartiallyIntersect(folds[i], folds[j])) {
            hit = [i, j];
            break;
          }
        }
      }
      if (hit === null) return folds;
      const [i, j] = hit;
      if (this.onIntersection === 'throw') {
        throw new FoldIntersectionError(folds[i], folds[j]);
      }
      folds[i].start = Math.min(folds[i].start, folds[j].start);
      folds[i].end = Math.max(folds[i].end, folds[j].end);
      folds.splice(j, 1);
    }
  }

  /** (Re)compute parent links. Requires a laminar fold set. */
  private rebuildHierarchy(): void {
    const sorted = [...this.folds].sort((a, b) => a.start - b.start || b.end - a.end);
    const stack: Fold[] = [];
    for (const fold of sorted) {
      while (stack.length > 0 && stack[stack.length - 1].end <= fold.start) stack.pop();
      fold.parent = stack.length > 0 ? stack[stack.length - 1] : null;
      stack.push(fold);
    }
  }

  /** Rebuild the visible projection and its offset-mapping segments. */
  private rebuild(): void {
    const rendered = this.folds
      .filter((fold) => fold.collapsed && !this.hasCollapsedAncestor(fold))
      .sort((a, b) => a.start - b.start);

    const segments: Segment[] = [];
    let visible = '';
    let cursor = 0;
    for (const fold of rendered) {
      if (fold.start > cursor) {
        segments.push({ kind: 'text', origStart: cursor, origEnd: fold.start, visStart: visible.length });
        visible += this.text.slice(cursor, fold.start);
      }
      const visStart = visible.length;
      visible += fold.placeholder;
      segments.push({ kind: 'placeholder', fold, visStart, visEnd: visible.length });
      cursor = fold.end;
    }
    if (cursor < this.text.length) {
      segments.push({ kind: 'text', origStart: cursor, origEnd: this.text.length, visStart: visible.length });
      visible += this.text.slice(cursor);
    }
    this.segments = segments;
    this.visible = visible;
  }

  private hasCollapsedAncestor(fold: Fold): boolean {
    let current = fold.parent;
    while (current !== null) {
      if (current.collapsed) return true;
      current = current.parent;
    }
    return false;
  }

  // ----------------------------------------------------------------- queries

  /** The projected text with placeholders substituted for collapsed folds. */
  getVisibleText(): string {
    return this.visible;
  }

  /** The current original text (edits are applied to it). */
  getOriginalText(): string {
    return this.text;
  }

  /** Snapshot of all folds, sorted by start offset. */
  getFolds(): FoldState[] {
    const depthOf = (fold: Fold): number => {
      let depth = 0;
      let current = fold.parent;
      while (current !== null) {
        depth++;
        current = current.parent;
      }
      return depth;
    };
    return [...this.folds]
      .sort((a, b) => a.start - b.start || b.end - a.end)
      .map((fold) => ({
        id: fold.id,
        start: fold.start,
        end: fold.end,
        placeholder: fold.placeholder,
        collapsed: fold.collapsed,
        depth: depthOf(fold),
      }));
  }

  setCollapsed(id: number, collapsed: boolean): void {
    const fold = this.folds.find((candidate) => candidate.id === id);
    if (!fold) throw new FoldRangeError(`Unknown fold id ${id}.`);
    if (fold.collapsed !== collapsed) {
      fold.collapsed = collapsed;
      this.rebuild();
    }
  }

  // ---------------------------------------------------------------- mapping

  /** Map an original-text UTF-16 offset to a visible-text UTF-16 offset. */
  toVisibleOffset(originalOffset: number): number {
    const offset = clamp(Math.round(originalOffset), 0, this.text.length);
    for (const segment of this.segments) {
      if (segment.kind === 'text') {
        if (offset <= segment.origEnd) return segment.visStart + (offset - segment.origStart);
      } else {
        if (offset < segment.fold.end) {
          if (offset <= segment.fold.start) return segment.visStart;
          const mid = (segment.fold.start + segment.fold.end) / 2;
          return offset <= mid ? segment.visStart : segment.visEnd;
        }
      }
    }
    return this.visible.length;
  }

  /** Map a visible-text UTF-16 offset to an original-text UTF-16 offset. */
  toOriginalOffset(visibleOffset: number): number {
    const offset = clamp(Math.round(visibleOffset), 0, this.visible.length);
    for (const segment of this.segments) {
      if (segment.kind === 'text') {
        const segVisEnd = segment.visStart + (segment.origEnd - segment.origStart);
        if (offset <= segVisEnd) return segment.origStart + (offset - segment.visStart);
      } else {
        if (offset < segment.visEnd) {
          if (offset <= segment.visStart) return segment.fold.start;
          const mid = (segment.visStart + segment.visEnd) / 2;
          return offset <= mid ? segment.fold.start : segment.fold.end;
        }
      }
    }
    return this.text.length;
  }

  /**
   * Map a visible selection to an original-text range. A selection edge that
   * falls strictly inside a placeholder expands to cover the whole fold, so a
   * selection spanning a placeholder maps back to the complete original range.
   */
  mapVisibleRangeToOriginal(visibleStart: number, visibleEnd: number): { start: number; end: number } {
    const start = this.mapSelectionBoundary(visibleStart, 'start');
    const end = this.mapSelectionBoundary(visibleEnd, 'end');
    return { start: Math.min(start, end), end: Math.max(start, end) };
  }

  private mapSelectionBoundary(visibleOffset: number, side: 'start' | 'end'): number {
    const offset = clamp(Math.round(visibleOffset), 0, this.visible.length);
    for (const segment of this.segments) {
      if (segment.kind === 'placeholder' && offset > segment.visStart && offset < segment.visEnd) {
        return side === 'start' ? segment.fold.start : segment.fold.end;
      }
    }
    return this.toOriginalOffset(offset);
  }

  // ----------------------------------------------------------------- editing

  /**
   * Replace the visible range [visibleStart, visibleEnd) with `insertText`.
   *
   * The range is mapped to the original text (placeholder-spanning selections
   * expand to the full folded range), the edit is applied, and every fold is
   * shifted, stretched or removed accordingly. Nesting and collapsed state of
   * surviving folds are preserved.
   */
  applyEdit(visibleStart: number, visibleEnd: number, insertText: string): void {
    if (
      !Number.isInteger(visibleStart) ||
      !Number.isInteger(visibleEnd) ||
      visibleStart < 0 ||
      visibleEnd > this.visible.length ||
      visibleStart > visibleEnd
    ) {
      throw new FoldRangeError(
        `Invalid edit range [${visibleStart}, ${visibleEnd}) for visible text of length ${this.visible.length}.`,
      );
    }
    if (splitsSurrogatePair(this.visible, visibleStart)) throw new SurrogatePairError(visibleStart);
    if (splitsSurrogatePair(this.visible, visibleEnd)) throw new SurrogatePairError(visibleEnd);

    const { start: origStart, end: origEnd } = this.mapVisibleRangeToOriginal(visibleStart, visibleEnd);
    const delta = insertText.length - (origEnd - origStart);
    this.text = this.text.slice(0, origStart) + insertText + this.text.slice(origEnd);

    const survivors: Fold[] = [];
    for (const fold of this.folds) {
      if (fold.end <= origStart) {
        survivors.push(fold); // entirely before the edit
      } else if (fold.start >= origEnd) {
        fold.start += delta; // entirely after the edit
        fold.end += delta;
        survivors.push(fold);
      } else if (fold.start <= origStart && fold.end >= origEnd) {
        fold.end += delta; // edit strictly inside (or exactly replacing) the fold
        survivors.push(fold);
      }
      // otherwise the edit destroys part of the fold boundary: drop the fold
    }
    this.folds = survivors;
    this.rebuildHierarchy();
    this.rebuild();
  }
}
