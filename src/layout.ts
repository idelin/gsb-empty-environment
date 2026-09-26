import type { Affinity, FlatCluster, LayoutOptions, Level } from "./types.ts";
import type { DocumentModel } from "./model.ts";
import { StaleLayoutError } from "./model.ts";
import { visualOrder } from "./reorder.ts";

export interface PlacedCluster {
  logicalIndex: number;
  text: string;
  level: Level;
  rtl: boolean;
  line: number;
  visualIndexInLine: number;
  x: number;
  width: number;
  nodeId: string;
  /** Slot holding the cluster's leading edge; assigned after line slots exist. */
  leadingSlot: number;
  /** Slot holding the cluster's trailing edge. */
  trailingSlot: number;
}

export interface CaretRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * A caret position. Never exists inside a grapheme cluster: offsets index the
 * boundaries 0..N. At soft-wrap / direction-run boundaries the same offset can
 * resolve to two positions, distinguished by explicit affinity.
 */
export class Caret {
  readonly offset: number;
  readonly affinity: Affinity;
  readonly line: number;
  readonly x: number;
  readonly slotFlatIndex: number;
  readonly layoutVersion: number;
  readonly rect: CaretRect;

  constructor(init: {
    offset: number;
    affinity: Affinity;
    line: number;
    x: number;
    slotFlatIndex: number;
    layoutVersion: number;
    rect: CaretRect;
  }) {
    this.offset = init.offset;
    this.affinity = init.affinity;
    this.line = init.line;
    this.x = init.x;
    this.slotFlatIndex = init.slotFlatIndex;
    this.layoutVersion = init.layoutVersion;
    this.rect = init.rect;
  }
}

export interface HitResult {
  slotFlatIndex: number;
  line: number;
  x: number;
  /** All carets at the hit boundary — multi-valued mappings are NOT collapsed. */
  carets: Caret[];
  /** Convenience pointer into `carets` chosen by the documented tie policy. */
  primary: Caret;
}

export interface VisualSlot {
  flatIndex: number;
  line: number;
  indexInLine: number;
  x: number;
  leftItem: PlacedCluster | undefined;
  rightItem: PlacedCluster | undefined;
  carets: Caret[];
}

export interface LayoutLine {
  index: number;
  y: number;
  width: number;
  height: number;
  items: PlacedCluster[];
  slots: VisualSlot[];
  /** Logical offsets of the first / last cluster placed on this visual line. */
  startOffset: number;
  endOffset: number;
}

export class BidiLayout {
  readonly model: DocumentModel;
  readonly version: number;
  readonly options: LayoutOptions;
  readonly lines: LayoutLine[] = [];
  readonly placed: PlacedCluster[] = [];
  private readonly slots: VisualSlot[] = [];
  private readonly caretByKey = new Map<string, Caret>();
  private readonly caretsByOffset = new Map<number, Caret[]>();

  constructor(model: DocumentModel, options: LayoutOptions) {
    this.model = model;
    this.version = model.version;
    this.options = options;
    this.build();
  }

  private build(): void {
    const clusters = this.model.getClusters();
    const levels = clusters.map((c) => c.level);
    const order = visualOrder(levels);
    const { maxWidth, metrics, lineHeight } = {
      maxWidth: this.options.maxWidth,
      metrics: this.options.metrics,
      lineHeight: this.options.metrics.lineHeight,
    };

    // Greedy soft wrap in VISUAL order, packed from physical left to right.
    let lineIndex = 0;
    let x = 0;
    let visualIndexInLine = 0;
    let lineItems: PlacedCluster[] = [];
    const commitLine = (): void => {
      this.finishLine(lineIndex, lineItems, lineHeight);
      lineIndex += 1;
      x = 0;
      visualIndexInLine = 0;
      lineItems = [];
    };

    for (const logicalIndex of order) {
      const cluster = clusters[logicalIndex]!;
      const width = metrics.clusterWidth(cluster);
      if (x > 0 && x + width > maxWidth) commitLine();
      const placed: PlacedCluster = {
        logicalIndex,
        text: cluster.text,
        level: cluster.level,
        rtl: cluster.level % 2 === 1,
        line: lineIndex,
        visualIndexInLine,
        x,
        width,
        nodeId: cluster.nodeId,
        leadingSlot: -1,
        trailingSlot: -1,
      };
      lineItems.push(placed);
      this.placed.push(placed);
      x += width;
      visualIndexInLine += 1;
    }
    if (lineItems.length > 0 || this.lines.length === 0) commitLine();
    if (clusters.length === 0) {
      const slot = this.slots[0]!;
      const only = new Caret({
        offset: 0,
        affinity: "forward",
        line: 0,
        x: 0,
        slotFlatIndex: slot.flatIndex,
        layoutVersion: this.version,
        rect: { x: 0, y: 0, width: 1, height: this.options.metrics.lineHeight },
      });
      slot.carets.push(only);
      this.caretByKey.set("0:forward", only);
      this.caretsByOffset.set(0, [only]);
    }
  }

  private finishLine(index: number, itemsIn: PlacedCluster[], height: number): void {
    const items = itemsIn;
    const width = items.reduce((sum, item) => sum + item.width, 0);
    const slotsCount = items.length + 1;
    const lineStartOffset = items[0]?.logicalIndex ?? 0;
    const endItem = items[items.length - 1];
    const endOffset = endItem === undefined ? 0 : endItem.logicalIndex + 1;
    const slots: VisualSlot[] = [];
    const flatBase = this.slots.length;
    for (let i = 0; i < slotsCount; i++) {
      const slot: VisualSlot = {
        flatIndex: flatBase + i,
        line: index,
        indexInLine: i,
        x: i === 0 ? 0 : items[i - 1]!.x + items[i - 1]!.width,
        leftItem: i === 0 ? undefined : items[i - 1],
        rightItem: i === items.length ? undefined : items[i]!,
        carets: [],
      };
      slots.push(slot);
      this.slots.push(slot);
    }
    const line: LayoutLine = {
      index,
      y: index * height,
      width,
      height,
      items,
      slots,
      startOffset: lineStartOffset,
      endOffset,
    };
    this.lines.push(line);

    // Every cluster contributes two edge carets on its exact edge slots;
    // at a soft-wrap edge only the edge physically present on this line is
    // generated, so the same logical offset appears once per line, each with
    // its explicit affinity.
    for (const item of items) {
      // forward caret = cluster's logical start = its leading edge:
      // physical LEFT slot for LTR, physical RIGHT slot for RTL.
      // backward caret = trailing edge: the opposite side.
      const leftSlotIndex = item.visualIndexInLine;
      const rightSlotIndex = item.visualIndexInLine + 1;
      item.leadingSlot = line.slots[item.rtl ? rightSlotIndex : leftSlotIndex]!.flatIndex;
      item.trailingSlot = line.slots[item.rtl ? leftSlotIndex : rightSlotIndex]!.flatIndex;
      this.addCaret(line, item.leadingSlot, item.logicalIndex, "forward");
      this.addCaret(line, item.trailingSlot, item.logicalIndex + 1, "backward");
    }
  }

  private addCaret(
    line: LayoutLine,
    slotFlatIndex: number,
    offset: number,
    affinity: Affinity,
  ): void {
    const slot = this.slots[slotFlatIndex]!;
    const key = `${offset}:${affinity}`;
    if (this.caretByKey.has(key)) {
      throw new Error(`Internal: caret ${key} generated twice`);
    }
    const caret = new Caret({
      offset,
      affinity,
      line: line.index,
      x: slot.x,
      slotFlatIndex: slot.flatIndex,
      layoutVersion: this.version,
      rect: { x: slot.x, y: line.y, width: 1, height: line.height },
    });
    slot.carets.push(caret);
    this.caretByKey.set(key, caret);
    const list = this.caretsByOffset.get(offset) ?? [];
    list.push(caret);
    this.caretsByOffset.set(offset, list);
  }

  assertCurrent(caret: Caret, kind = "caret"): void {
    if (caret.layoutVersion !== this.version) {
      throw new StaleLayoutError(kind, caret.layoutVersion, this.version);
    }
  }

  get length(): number {
    return this.placed.length;
  }

  /** All visual positions of a logical offset: 0, 1 or 2 carets. */
  caretsAt(offset: number): Caret[] {
    if (offset < 0 || offset > this.length) {
      throw new Error(`Offset ${offset} out of bounds [0, ${this.length}]`);
    }
    return this.caretsByOffset.get(offset) ?? [];
  }

  caretAt(offset: number, affinity: Affinity): Caret {
    const caret = this.caretByKey.get(`${offset}:${affinity}`);
    if (caret === undefined) throw new Error(`No caret at ${offset}/${affinity}`);
    return caret;
  }

  /**
   * Hit test a physical point. Returns EVERY caret at the nearest boundary;
   * primary prefers the edge attached to the deeper (higher) level, ties to
   * the backward affinity. Multi-valued mappings are never compressed.
   */
  hitTest(line: number, x: number): HitResult {
    if (line < 0 || line >= this.lines.length) throw new Error(`Bad line ${line}`);
    const target = this.lines[line]!;
    let best = target.slots[0]!;
    for (const slot of target.slots) {
      if (Math.abs(slot.x - x) < Math.abs(best.x - x)) best = slot;
    }
    // Coincident same-offset edges (e.g. inside a uniform run) are one
    // physical position; distinct offsets at a bidi split are kept both.
    const carets: Caret[] = [];
    for (const caret of best.carets) {
      if (!carets.some((other) => other.offset === caret.offset)) carets.push(caret);
    }
    if (carets.length === 0) throw new Error("Internal: boundary without caret");
    const attachmentLevel = (caret: Caret): number => {
      const itemIndex = caret.affinity === "backward" ? caret.offset - 1 : caret.offset;
      return this.model.getClusters()[itemIndex]?.level ?? -1;
    };
    let primary = carets[0]!;
    for (const caret of carets) {
      const levelCmp = attachmentLevel(caret) - attachmentLevel(primary);
      if (levelCmp > 0 || (levelCmp === 0 && primary.affinity === "forward")) primary = caret;
    }
    return { slotFlatIndex: best.flatIndex, line, x: best.x, carets: [...carets], primary };
  }

  private slot(flatIndex: number): VisualSlot {
    const slot = this.slots[flatIndex];
    if (slot === undefined) throw new Error(`No visual slot ${flatIndex}`);
    return slot;
  }

  /**
   * Visual arrow movement. Right/Left mean PHYSICAL screen directions, which
   * differ from logical directions inside RTL runs. Stepping right enters the
   * cluster on the right via its reading-side edge (LTR leading / RTL
   * trailing); stepping left mirrors it. The two logical carets sharing a
   * bidi boundary are therefore both reachable from opposite directions.
   */
  moveVisual(caret: Caret, direction: "right" | "left" | "up" | "down" | "home" | "end"): Caret {
    this.assertCurrent(caret);
    const current = this.slot(caret.slotFlatIndex);
    switch (direction) {
      case "right": {
        if (current.indexInLine < this.lines[current.line]!.slots.length - 1) {
          // Entering the cluster on the right: LTR clusters are entered via
          // their leading (forward) edge; RTL clusters via their trailing
          // (backward) edge — because reading them physically rightward means
          // traversing logical order backwards.
          const target = this.slot(current.flatIndex + 1);
          const right = target.rightItem;
          if (right !== undefined) {
            return right.rtl
              ? this.caretInSlot(target, right.logicalIndex + 1, "backward")
              : this.caretInSlot(target, right.logicalIndex, "forward");
          }
          return this.slotCaret(target, "backward");
        }
        if (current.line + 1 < this.lines.length) {
          const next = this.lines[current.line + 1]!.slots[0]!;
          return this.slotCaret(next, "forward");
        }
        return caret;
      }
      case "left": {
        if (current.indexInLine > 0) {
          // Entering the cluster on the left: LTR via trailing edge, RTL via
          // leading edge.
          const target = this.slot(current.flatIndex - 1);
          const left = target.leftItem;
          if (left !== undefined) {
            return left.rtl
              ? this.caretInSlot(target, left.logicalIndex, "forward")
              : this.caretInSlot(target, left.logicalIndex + 1, "backward");
          }
          return this.slotCaret(target, "forward");
        }
        if (current.line > 0) {
          const prev = this.lines[current.line - 1]!;
          return this.slotCaret(prev.slots[prev.slots.length - 1]!, "backward");
        }
        return caret;
      }
      case "home":
        return this.slotCaret(this.lines[current.line]!.slots[0]!, "forward");
      case "end": {
        const lineSlots = this.lines[current.line]!.slots;
        return this.slotCaret(lineSlots[lineSlots.length - 1]!, "backward");
      }
      case "up":
        if (current.line === 0) return caret;
        return this.hitTest(current.line - 1, caret.x).primary;
      case "down":
        if (current.line === this.lines.length - 1) return caret;
        return this.hitTest(current.line + 1, caret.x).primary;
    }
  }

  private caretInSlot(slot: VisualSlot, offset: number, affinity: Affinity): Caret {
    const found = slot.carets.find(
      (candidate) => candidate.offset === offset && candidate.affinity === affinity,
    );
    if (found === undefined) throw new Error(`Internal: caret ${offset}/${affinity} absent from slot ${slot.flatIndex}`);
    return found;
  }

  private slotCaret(slot: VisualSlot, affinity: Affinity): Caret {
    // The caller has already chosen the PHYSICAL slot being entered. Choose
    // the edge of the cluster on the entered side; only if that edge is not
    // on this slot (it sits at the dual boundary one slot away) fall back to
    // whatever edge the slot carries.
    const wantedOffset =
      affinity === "forward"
        ? slot.rightItem?.logicalIndex
        : slot.leftItem !== undefined
          ? slot.leftItem.logicalIndex + 1
          : undefined;
    const exact =
      wantedOffset !== undefined
        ? slot.carets.find((candidate) => candidate.offset === wantedOffset && candidate.affinity === affinity)
        : undefined;
    if (exact !== undefined) return exact;
    const anyAffinity = slot.carets.find((candidate) => candidate.affinity === affinity);
    if (anyAffinity !== undefined) return anyAffinity;
    // physical start of a pure RTL line carries backward affinity only
    return slot.carets[0]!;
  }

  /** Logical movement: offset +/- 1 regardless of visual position. */
  moveLogical(caret: Caret, direction: "forward" | "backward"): Caret {
    this.assertCurrent(caret);
    let offset = caret.offset;
    if (direction === "forward") {
      if (offset >= this.length) return caret;
      offset += 1;
      return this.caretAt(offset, "backward");
    }
    if (offset <= 0) return caret;
    offset -= 1;
    return this.caretAt(offset, "forward");
  }

  firstCaret(): Caret {
    return this.caretAt(0, "forward");
  }

  lastCaret(): Caret {
    return this.caretAt(this.length, "backward");
  }

  /** Visual text (left -> right) of one line, useful for rendering/fixtures. */
  visualText(line: number): string {
    return this.lines[line]!.items.map((item) => item.text).join("");
  }
}
