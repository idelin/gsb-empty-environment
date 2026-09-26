import type { BidiLayout, Caret, PlacedCluster } from "./layout.ts";
import { StaleLayoutError } from "./model.ts";

export interface LogicalSpan {
  /** Inclusive; negative when empty. */
  from: number;
  /** Exclusive; negative when empty. */
  to: number;
}

export interface SelectionRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * A visual, direction-preserving selection across one or more visual lines.
 * Anchor is the press point, active the drag point; the orientation is never
 * normalized away, so mapping back keeps bidi direction information.
 */
export class VisualSelection {
  readonly anchor: Caret;
  readonly active: Caret;
  readonly layoutVersion: number;
  readonly flatFrom: number;
  readonly flatTo: number;
  private readonly layout: BidiLayout;

  constructor(layout: BidiLayout, anchor: Caret, active: Caret) {
    layout.assertCurrent(anchor, "selection anchor");
    layout.assertCurrent(active, "selection active");
    this.layout = layout;
    this.anchor = anchor;
    this.active = active;
    this.layoutVersion = layout.version;
    this.flatFrom = Math.min(anchor.slotFlatIndex, active.slotFlatIndex);
    this.flatTo = Math.max(anchor.slotFlatIndex, active.slotFlatIndex);
  }

  private assertCurrent(): void {
    if (this.layoutVersion !== this.layout.version) {
      throw new StaleLayoutError("selection", this.layoutVersion, this.layout.version);
    }
  }

  /** Visual direction of the drag, independent of logical text direction. */
  get visualDirection(): "rightward" | "leftward" | "none" {
    if (this.active.slotFlatIndex === this.anchor.slotFlatIndex) return "none";
    return this.active.slotFlatIndex > this.anchor.slotFlatIndex ? "rightward" : "leftward";
  }

  get isEmpty(): boolean {
    return this.anchor.slotFlatIndex === this.active.slotFlatIndex;
  }

  /**
   * The cluster boxes covered when moving visually from anchor to active.
   * Over a soft wrap these naturally span multiple visual lines.
   */
  selectedItems(): PlacedCluster[] {
    this.assertCurrent();
    const out: PlacedCluster[] = [];
    for (const line of this.layout.lines) {
      for (const item of line.items) {
        const before = item.visualIndexInLine; // slot index before the item
        const after = item.visualIndexInLine + 1;
        const flatBefore = line.slots[before]!.flatIndex;
        const flatAfter = line.slots[after]!.flatIndex;
        if (Math.max(this.flatFrom, flatBefore) < Math.min(this.flatTo, flatAfter)) {
          out.push(item);
        }
      }
    }
    return out;
  }

  /**
   * Copy content back in LOGICAL order regardless of visual drag direction —
   * a visual bidi selection may select a logically non-contiguous set.
   */
  copiedText(): string {
    return [...this.selectedItems()]
      .sort((a, b) => a.logicalIndex - b.logicalIndex)
      .map((item) => item.text)
      .join("");
  }

  /**
   * Logical spans covered by the visual selection. A single visual run can
   * produce several spans when direction fragments interleave.
   */
  logicalSpans(): LogicalSpan[] {
    const indices = this.selectedItems()
      .map((item) => item.logicalIndex)
      .sort((a, b) => a - b);
    const spans: LogicalSpan[] = [];
    for (const index of indices) {
      const last = spans[spans.length - 1];
      if (last !== undefined && last.to === index) last.to = index + 1;
      else spans.push({ from: index, to: index + 1 });
    }
    return spans;
  }

  /**
   * Render rectangles per visual line (selection may be non-rectangular in
   * bidi even on a single line; we union per contiguous visual run).
   */
  rects(): SelectionRect[] {
    this.assertCurrent();
    const byLine = new Map<number, PlacedCluster[]>();
    for (const item of this.selectedItems()) {
      const list = byLine.get(item.line) ?? [];
      list.push(item);
      byLine.set(item.line, list);
    }
    const rects: SelectionRect[] = [];
    for (const [lineIndex, items] of byLine) {
      const ordered = [...items].sort((a, b) => a.x - b.x);
      const line = this.layout.lines[lineIndex]!;
      let runStart = -1;
      let runEnd = -1;
      const flush = (): void => {
        if (runStart !== -1) {
          rects.push({
            x: runStart,
            y: line.y,
            width: runEnd - runStart,
            height: line.height,
          });
        }
      };
      for (const item of ordered) {
        const left = item.x;
        const right = item.x + item.width;
        if (runStart === -1 || left > runEnd + 1e-9) {
          flush();
          runStart = left;
        }
        runEnd = Math.max(runEnd, right);
      }
      flush();
    }
    return rects.sort((a, b) => a.y - b.y || a.x - b.x);
  }
}
