/**
 * Public model: owns the node tree + metrics, rebuilds layout snapshots and
 * implements caret motion, hit testing and selections on top of a snapshot.
 */

import type { Caret, Metrics, TreeNode, VisualLine } from './types.ts';
import type { LayoutSnapshot } from './types.ts';
import { StaleLayoutError } from './types.ts';
import { isTextNode } from './types.ts';
import { buildClusters, flattenTree } from './clusters.ts';
import { layoutClusters } from './layout.ts';

export interface VisualRange {
  line: number;
  x1: number;
  x2: number;
}

export class Selection {
  readonly anchor: Caret;
  head: Caret;

  constructor(
    anchor: Caret,
    head: Caret,
  ) {
    if (anchor.version !== head.version) {
      throw new StaleLayoutError(anchor.version, head.version);
    }
    this.anchor = anchor;
    this.head = head;
  }

  get version(): number {
    return this.anchor.version;
  }

  /** Logical selection is always the ordered span between the offsets. */
  get startOffset(): number {
    return Math.min(this.anchor.offset, this.head.offset);
  }

  get endOffset(): number {
    return Math.max(this.anchor.offset, this.head.offset);
  }

  /** Logical (document) direction of the drag; independent of visual order. */
  get logicalDirection(): 'forward' | 'backward' | 'none' {
    if (this.head.offset > this.anchor.offset) return 'forward';
    if (this.head.offset < this.anchor.offset) return 'backward';
    if (this.head.line === this.anchor.line && this.head.x === this.anchor.x) return 'none';
    // same offset at a soft wrap: the lower-line home is the forward stop
    return this.head.line > this.anchor.line ? 'forward' : 'backward';
  }

  get isCollapsed(): boolean {
    return (
      this.anchor.offset === this.head.offset &&
      this.anchor.line === this.head.line &&
      this.anchor.x === this.head.x
    );
  }

  /** Highlight rectangles in visual coordinates, per visual line. */
  visualRanges(): VisualRange[] {
    const ranges: VisualRange[] = [];
    const topLine = Math.min(this.anchor.line, this.head.line);
    const bottomLine = Math.max(this.anchor.line, this.head.line);
    for (let line = topLine; line <= bottomLine; line++) {
      let x1: number;
      let x2: number;
      if (line === this.anchor.line && line === this.head.line) {
        x1 = Math.min(this.anchor.x, this.head.x);
        x2 = Math.max(this.anchor.x, this.head.x);
      } else if (line === this.anchor.line) {
        x1 = this.anchor.x;
        x2 = Number.POSITIVE_INFINITY;
      } else if (line === this.head.line) {
        x1 = Number.NEGATIVE_INFINITY;
        x2 = this.head.x;
      } else {
        x1 = Number.NEGATIVE_INFINITY;
        x2 = Number.POSITIVE_INFINITY;
      }
      // A whole interior line (or a line whose endpoint sits at an edge) is
      // still a selected line: the infinities mark "extend to the margin".
      if (!(x1 === x2 && Number.isFinite(x1))) ranges.push({ line, x1, x2 });
    }
    return ranges;
  }
}

export class BidiTextModel {
  private root: TreeNode;
  private readonly metrics: Metrics;
  private lineWidth: number;
  private versionCounter = 0;
  private snapshot!: LayoutSnapshot;

  constructor(root: TreeNode, metrics: Metrics, lineWidth: number) {
    this.root = root;
    this.metrics = metrics;
    this.lineWidth = lineWidth;
    this.snapshot = this.rebuild();
  }

  private rebuild() {
    this.versionCounter += 1;
    const runs = flattenTree(this.root);
    const clusters = buildClusters(runs, this.metrics);
    return layoutClusters(clusters, this.lineWidth, this.versionCounter);
  }

  get version(): number {
    return this.versionCounter;
  }

  get layout() {
    return this.snapshot;
  }

  get lines(): VisualLine[] {
    return this.snapshot.lines;
  }

  setLineWidth(width: number): void {
    if (width === this.lineWidth) return;
    this.lineWidth = width;
    this.snapshot = this.rebuild();
  }

  /**
   * Local level update: change the level carried by one node (a text node,
   * or a span -> every descendant without its own level) and rebuild. All
   * carets/selections from the previous snapshot become unusable.
   */
  setNodeLevel(nodeId: string, level: number): void {
    const node = this.findNode(nodeId);
    if (!node) throw new Error(`unknown node id: ${nodeId}`);
    if (level < 0 || !Number.isInteger(level)) throw new Error('levels must be non-negative integers');
    node.level = level;
    this.snapshot = this.rebuild();
  }

  private findNode(id: string): TreeNode | undefined {
    let found: TreeNode | undefined;
    const walk = (node: TreeNode): void => {
      if (found) return;
      if (node.id === id) {
        found = node;
        return;
      }
      if (!isTextNode(node)) node.children.forEach(walk);
    };
    walk(this.root);
    return found;
  }

  private check(caret: Caret): void {
    if (caret.version !== this.versionCounter) {
      throw new StaleLayoutError(caret.version, this.versionCounter);
    }
  }

  clusterText(offset: number): string {
    return this.snapshot.clusters[offset]?.text ?? '';
  }

  /** All visual homes of one logical offset - up to two, never collapsed. */
  caretsAtOffset(offset: number): Caret[] {
    return this.snapshot.carets.filter((caret) => caret.offset === offset);
  }

  /** Canonical home: leading edge wins (matches logical "right" semantics). */
  primaryCaret(offset: number): Caret {
    const homes = this.caretsAtOffset(offset);
    if (homes.length === 0) throw new Error(`offset ${offset} has no visual caret (e.g. an empty line)`);
    return (
      homes.find((caret) => caret.affinity.edge === 'leading' && caret.lineBias === 'lower') ??
      homes.find((caret) => caret.affinity.edge === 'leading') ??
      homes[0]!
    );
  }

  private siblingHome(caret: Caret): Caret | undefined {
    const wantedBias = caret.lineBias === 'upper' ? 'lower' : 'upper';
    return this.snapshot.carets.find(
      (other) =>
        other.offset === caret.offset &&
        other.lineBias === wantedBias,
    );
  }

  /**
   * One physical visual step left/right. Within a line carets advance in x;
   * stepping off a soft-wrap edge jumps to the SAME offset's dual home on
   * the adjacent line (upper <-> lower), so ltr and rtl wraps both work
   * without collapsing the two positions. Returns null at document ends.
   */
  moveVisual(caret: Caret, direction: 'left' | 'right'): Caret | null {
    this.check(caret);
    const inLine = this.snapshot.carets.filter((candidate) => candidate.line === caret.line);
    const index = inLine.findIndex(
      (candidate) =>
        candidate.offset === caret.offset &&
        candidate.x === caret.x &&
        candidate.affinity.edge === caret.affinity.edge,
    );
    if (index < 0) throw new Error('caret is not part of the current layout');
    const target = direction === 'right' ? inLine[index + 1] : inLine[index - 1];
    if (target) return target;

    const atMinEdge = index === 0;
    const atMaxEdge = index === inLine.length - 1;
    if ((direction === 'left' && atMinEdge) || (direction === 'right' && atMaxEdge)) {
      return this.siblingHome(caret) ?? null;
    }
    return null;
  }

  /**
   * One logical step: offsets move monotonically by one cluster; at a soft
   * wrap or direction boundary the caret switches between its two visual
   * homes instead of jumping in x.
   */
  moveLogical(caret: Caret, direction: 'forward' | 'backward'): Caret | null {
    this.check(caret);
    const step = direction === 'forward' ? 1 : -1;
    let offset = caret.offset + step;
    const max = this.snapshot.clusters.length;
    while (offset >= 0 && offset <= max) {
      const homes = this.caretsAtOffset(offset);
      if (homes.length > 0) {
        return (
          homes.find((home) => home.affinity.edge === (step > 0 ? 'leading' : 'trailing')) ??
          homes[0]!
        );
      }
      offset += step;
    }
    return null;
  }

  /** Move vertically, keeping x; ties prefer matching edge affinity. */
  moveVertical(caret: Caret, direction: 'up' | 'down'): Caret | null {
    this.check(caret);
    const targetLine = caret.line + (direction === 'down' ? 1 : -1);
    if (targetLine < 0 || targetLine >= this.snapshot.lines.length) return null;
    const candidates = this.snapshot.carets.filter((candidate) => candidate.line === targetLine);
    if (candidates.length === 0) return null;
    candidates.sort((a, b) => {
      const da = Math.abs(a.x - caret.x);
      const db = Math.abs(b.x - caret.x);
      if (da !== db) return da - db;
      return a.affinity.edge === caret.affinity.edge ? -1 : 1;
    });
    return candidates[0]!;
  }

  /**
   * Hit test. Ties (caret exactly between two clusters, or equidistant
   * candidates) return BOTH carets - the multi-valued mapping is exposed,
   * not forced to a single offset.
   */
  hitTest(line: number, x: number): Caret[] {
    if (line < 0 || line >= this.snapshot.lines.length) {
      throw new Error(`line ${line} out of range`);
    }
    const candidates = this.snapshot.carets.filter((caret) => caret.line === line);
    if (candidates.length === 0) return [];
    // Click exactly on a shared position: expose the multi-valued mapping.
    const exact = candidates.filter((caret) => caret.x === x);
    if (exact.length > 0) return exact;
    // Otherwise snap to the single nearest stop; a midpoint tie resolves to
    // the visually-left caret (deterministic, not offset-collapsing).
    let best = Infinity;
    for (const caret of candidates) best = Math.min(best, Math.abs(caret.x - x));
    const nearest = candidates.filter((caret) => Math.abs(caret.x - x) === best);
    if (nearest.length > 1) return [nearest.reduce((a, b) => (a.x <= b.x ? a : b))];
    return nearest;
  }

  select(anchor: Caret, head: Caret): Selection {
    this.check(anchor);
    this.check(head);
    return new Selection(anchor, head);
  }

  /** Copy a logical offset range back out in LOGICAL order. */
  copyText(startOffset: number, endOffset: number): string {
    return this.snapshot.clusters
      .slice(startOffset, endOffset)
      .filter((cluster) => !cluster.hardBreak)
      .map((cluster) => cluster.text)
      .join('');
  }

  copySelection(selection: Selection): string {
    this.check(selection.anchor);
    this.check(selection.head);
    return this.copyText(selection.startOffset, selection.endOffset);
  }
}
