import { RgbaImage, type Color } from './rgba-image.ts';
import { Delta, PixelDelta, CropDelta, CompositeDelta, makeStrip } from './delta.ts';

interface HistoryNode {
  id: number;
  label: string;
  parent: HistoryNode | null;
  delta: Delta | null; // null only on the root
  children: HistoryNode[];
}

export interface MemoryStats {
  nodes: number;
  deltaBytes: number;
  snapshotBytes: number;
}

/**
 * Editor holds the live image plus a history tree. Undo/redo walk the tree;
 * committing after an undo creates a new branch. Old deltas are RLE
 * compressed in memory, and history deeper than `maxDepth` is evicted by
 * folding it into the root snapshot — the current image is never touched.
 */
export class Editor {
  readonly image: RgbaImage;
  private root: HistoryNode;
  private rootSnapshot: RgbaImage; // full state at the root (grows via eviction)
  private current: HistoryNode;
  private activeChild = new Map<number, HistoryNode>();
  private nextId = 1;
  private openTxn: Delta[] | null = null;

  /** Retain at most this many deltas between root and current (eviction). */
  maxDepth: number;
  /** Compress deltas farther than this many steps behind current. */
  compressAfter: number;

  constructor(image: RgbaImage, opts: { maxDepth?: number; compressAfter?: number } = {}) {
    this.image = image;
    this.rootSnapshot = image.clone();
    this.root = { id: 0, label: '(root)', parent: null, delta: null, children: [] };
    this.current = this.root;
    this.maxDepth = opts.maxDepth ?? Infinity;
    this.compressAfter = opts.compressAfter ?? Infinity;
  }

  // ---- editing primitives -------------------------------------------------

  /** Paint a solid rect; records only pixels whose value actually changes. */
  strokeRect(x: number, y: number, w: number, h: number, color: Color, label = 'stroke'): boolean {
    this.checkRect(x, y, w, h);
    const indices: number[] = [];
    const before: number[] = [];
    const after: number[] = [];
    for (let row = y; row < y + h; row++) {
      for (let col = x; col < x + w; col++) {
        const o = (row * this.image.width + col) * 4;
        const b0 = this.image.data[o], b1 = this.image.data[o + 1],
              b2 = this.image.data[o + 2], b3 = this.image.data[o + 3];
        if (b0 === color[0] && b1 === color[1] && b2 === color[2] && b3 === color[3]) continue;
        indices.push(row * this.image.width + col);
        before.push(b0, b1, b2, b3);
        after.push(color[0], color[1], color[2], color[3]);
        this.image.data[o] = color[0];
        this.image.data[o + 1] = color[1];
        this.image.data[o + 2] = color[2];
        this.image.data[o + 3] = color[3];
      }
    }
    if (indices.length === 0) return false;
    this.record(new PixelDelta(Uint32Array.from(indices), Uint8Array.from(before), Uint8Array.from(after)), label);
    return true;
  }

  /** Crop to a rect; removed pixels are stored in the delta (a metadata transaction). */
  crop(x: number, y: number, w: number, h: number, label = 'crop'): boolean {
    this.checkRect(x, y, w, h);
    const W = this.image.width, H = this.image.height;
    if (x === 0 && y === 0 && w === W && h === H) return false;

    const grab = (sx: number, sy: number, sw: number, sh: number) => {
      const bytes = new Uint8Array(sw * sh * 4);
      for (let row = 0; row < sh; row++) {
        const src = ((sy + row) * W + sx) * 4;
        bytes.set(this.image.data.subarray(src, src + sw * 4), row * sw * 4);
      }
      return bytes;
    };
    const strips = [];
    if (y > 0) strips.push(makeStrip(0, 0, W, y, grab(0, 0, W, y)));                 // top
    if (y + h < H) strips.push(makeStrip(0, y + h, W, H - y - h, grab(0, y + h, W, H - y - h))); // bottom
    if (x > 0) strips.push(makeStrip(0, y, x, h, grab(0, y, x, h)));                 // left
    if (x + w < W) strips.push(makeStrip(x + w, y, W - x - w, h, grab(x + w, y, W - x - w, h))); // right

    const delta = new CropDelta(W, H, x, y, w, h, strips);
    delta.apply(this.image);
    this.record(delta, label);
    return true;
  }

  // ---- transactions ---------------------------------------------------------

  beginTransaction(): void {
    if (this.openTxn) throw new Error('transaction already open');
    this.openTxn = [];
  }

  commitTransaction(label = 'transaction'): boolean {
    if (!this.openTxn) throw new Error('no open transaction');
    const deltas = this.openTxn;
    this.openTxn = null;
    if (deltas.length === 0) return false;
    this.commit(deltas.length === 1 ? deltas[0] : new CompositeDelta(deltas), label);
    return true;
  }

  get transactionOpen(): boolean {
    return this.openTxn !== null;
  }

  // ---- history navigation ---------------------------------------------------

  undo(): boolean {
    if (this.current === this.root || !this.current.delta) return false;
    this.current.delta.revert(this.image);
    this.activeChild.set(this.current.parent!.id, this.current);
    this.current = this.current.parent!;
    this.compressOld();
    return true;
  }

  /** Redo along the last-used branch, or pick a branch by index. */
  redo(branchIndex?: number): boolean {
    const kids = this.current.children;
    if (kids.length === 0) return false;
    const child = branchIndex !== undefined
      ? kids[branchIndex]
      : this.activeChild.get(this.current.id) ?? kids[kids.length - 1];
    if (!child || !child.delta) return false;
    child.delta.apply(this.image);
    this.current = child;
    this.compressOld();
    return true;
  }

  /** Labels of the branches available below the current node. */
  branches(): string[] {
    return this.current.children.map((c) => c.label);
  }

  get depth(): number {
    let d = 0;
    for (let n = this.current; n.parent; n = n.parent) d++;
    return d;
  }

  get label(): string {
    return this.current.label;
  }

  // ---- memory management ----------------------------------------------------

  memoryStats(): MemoryStats {
    let nodes = 0, deltaBytes = 0;
    const walk = (n: HistoryNode) => {
      nodes++;
      if (n.delta) deltaBytes += n.delta.byteSize();
      for (const c of n.children) walk(c);
    };
    walk(this.root);
    return { nodes, deltaBytes, snapshotBytes: this.rootSnapshot.data.length };
  }

  private record(delta: Delta, label: string): void {
    if (this.openTxn) {
      this.openTxn.push(delta);
      return;
    }
    this.commit(delta, label);
  }

  private commit(delta: Delta, label: string): void {
    const node: HistoryNode = {
      id: this.nextId++, label, parent: this.current, delta, children: [],
    };
    this.current.children.push(node);
    this.activeChild.set(this.current.id, node);
    this.current = node;
    this.compressOld();
    this.evict();
  }

  /** RLE-compress every stored delta except the newest `compressAfter` ones. */
  private compressOld(): void {
    if (!Number.isFinite(this.compressAfter)) return;
    const protect = new Set<HistoryNode>();
    let n: HistoryNode | null = this.current;
    for (let i = 0; i <= this.compressAfter && n; i++, n = n.parent) protect.add(n);
    const walk = (node: HistoryNode) => {
      if (node.delta && !protect.has(node)) node.delta.compress();
      for (const c of node.children) walk(c);
    };
    walk(this.root);
  }

  /**
   * Fold the oldest retained delta into the root snapshot. Only possible
   * while the root has a single child (a linear trunk); the live image and
   * all retained deltas are untouched, so the current picture cannot change.
   */
  private evict(): void {
    while (this.depth > this.maxDepth && this.root.children.length === 1) {
      const oldest = this.root.children[0];
      oldest.delta!.apply(this.rootSnapshot);
      for (const c of oldest.children) c.parent = this.root;
      this.root.children = oldest.children;
    }
  }

  private checkRect(x: number, y: number, w: number, h: number): void {
    if (![x, y, w, h].every(Number.isInteger) || w <= 0 || h <= 0 ||
        x < 0 || y < 0 || x + w > this.image.width || y + h > this.image.height) {
      throw new Error(`rect (${x},${y},${w},${h}) outside ${this.image.width}x${this.image.height}`);
    }
  }
}
