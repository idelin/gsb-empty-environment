/**
 * Incremental image pyramid with block-granular invalidation.
 *
 * - Each parent block is generated from the 2x2 neighbourhood of child
 *   blocks; the filter kernel may sample across block boundaries.
 * - Local edits invalidate a pixel-precise range that is expanded by the
 *   kernel radius while propagating upwards, so only affected blocks are
 *   recomputed.
 * - Updates run level by level, lowest dirty level first, so a block is
 *   only recomputed after its dependencies are clean.
 * - Every invalidation carries a monotonically increasing epoch. A computed
 *   block is committed only if neither the block itself nor any child block
 *   in its kernel footprint was invalidated while it was being computed,
 *   which guarantees stale results never overwrite newer parent data.
 * - Pyramid borders use mirror extension (reflect-101: -1 -> 1, n -> n-2).
 */

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Kernel {
  /** Odd kernel size (width = height = size). */
  readonly size: number;
  /** Row-major weights, size * size entries. */
  readonly weights: ArrayLike<number>;
}

export interface PyramidOptions {
  width: number;
  height: number;
  /** Number of levels including the base. Defaults to down to 1x1. */
  levels?: number;
  /** Block edge length in pixels, identical on every level. Default 16. */
  blockSize?: number;
  /** Filter kernel used for downsampling. Default: 5x5 binomial to 1/256. */
  kernel?: Kernel;
}

export interface PyramidStats {
  /** Blocks recomputed and committed since construction. */
  computedBlocks: number;
  /** Computed blocks discarded because a newer invalidation arrived. */
  rejectedCommits: number;
}

/** Default 5x5 binomial (Gaussian-like) kernel, sums to 1. */
export function binomialKernel5(): Kernel {
  const b = [1, 4, 6, 4, 1];
  const weights = new Float64Array(25);
  for (let y = 0; y < 5; y++) {
    for (let x = 0; x < 5; x++) {
      weights[y * 5 + x] = (b[y] * b[x]) / 256;
    }
  }
  return { size: 5, weights };
}

/** Mirror (reflect-101) extension: -1 -> 1, length -> length - 2. */
export function mirrorIndex(index: number, length: number): number {
  if (length <= 1) return 0;
  let i = index;
  while (i < 0 || i >= length) {
    i = i < 0 ? -i : 2 * length - 2 - i;
  }
  return i;
}

interface BlockState {
  /** Epoch of the newest invalidation that touched this block. */
  version: number;
  /** Epoch the stored pixels were computed against (< version => dirty). */
  cleanVersion: number;
}

interface LevelState {
  width: number;
  height: number;
  blocksX: number;
  blocksY: number;
  pixels: Float64Array;
  blocks: BlockState[];
  /** Indices of blocks awaiting recomputation (levels >= 1 only). */
  dirty: Set<number>;
  /** Exact union of invalidated pixel ranges since the level was clean. */
  pendingRect: Rect | null;
}

function unionRect(a: Rect | null, b: Rect): Rect {
  if (a === null) return { ...b };
  const x0 = Math.min(a.x, b.x);
  const y0 = Math.min(a.y, b.y);
  const x1 = Math.max(a.x + a.w, b.x + b.w);
  const y1 = Math.max(a.y + a.h, b.y + b.h);
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
}

export class ImagePyramid {
  readonly width: number;
  readonly height: number;
  readonly blockSize: number;
  readonly kernel: Kernel;
  readonly kernelRadius: number;
  readonly stats: PyramidStats = { computedBlocks: 0, rejectedCommits: 0 };

  /**
   * Optional hook invoked after a block was computed but before its result
   * is committed. Useful to simulate concurrent edits landing mid-update
   * (e.g. in tests); the stale-commit guard still applies.
   */
  onBlockComputed: ((level: number, blockIndex: number) => void) | null = null;

  /** Monotonic counter bumped by every edit; invalidations are tagged with it. */
  private epoch = 0;
  private readonly levels: LevelState[] = [];
  private readonly scratch: Float64Array;

  constructor(options: PyramidOptions) {
    const { width, height } = options;
    if (!Number.isInteger(width) || width < 1 || !Number.isInteger(height) || height < 1) {
      throw new RangeError('width and height must be positive integers');
    }
    this.width = width;
    this.height = height;

    const blockSize = options.blockSize ?? 16;
    if (!Number.isInteger(blockSize) || blockSize < 1) {
      throw new RangeError('blockSize must be a positive integer');
    }
    this.blockSize = blockSize;

    this.kernel = options.kernel ?? binomialKernel5();
    const ksize = this.kernel.size;
    if (!Number.isInteger(ksize) || ksize < 1 || ksize % 2 === 0) {
      throw new RangeError('kernel size must be a positive odd integer');
    }
    if (this.kernel.weights.length < ksize * ksize) {
      throw new RangeError('kernel weights must contain size*size entries');
    }
    this.kernelRadius = (ksize - 1) / 2;

    const maxLevels = options.levels ?? Number.POSITIVE_INFINITY;
    if (maxLevels < 1) throw new RangeError('levels must be >= 1');

    let w = width;
    let h = height;
    for (;;) {
      this.levels.push(ImagePyramid.makeLevel(w, h, blockSize, this.levels.length === 0));
      if (this.levels.length >= maxLevels || (w === 1 && h === 1)) break;
      w = Math.max(1, Math.ceil(w / 2));
      h = Math.max(1, Math.ceil(h / 2));
    }

    this.scratch = new Float64Array(blockSize * blockSize);
  }

  private static makeLevel(width: number, height: number, blockSize: number, isBase: boolean): LevelState {
    const blocksX = Math.ceil(width / blockSize);
    const blocksY = Math.ceil(height / blockSize);
    const blocks: BlockState[] = [];
    const dirty = new Set<number>();
    for (let i = 0; i < blocksX * blocksY; i++) {
      blocks.push({ version: 0, cleanVersion: isBase ? 0 : -1 });
      if (!isBase) dirty.add(i);
    }
    return {
      width,
      height,
      blocksX,
      blocksY,
      pixels: new Float64Array(width * height),
      blocks,
      dirty,
      pendingRect: isBase ? null : { x: 0, y: 0, w: width, h: height },
    };
  }

  get levelCount(): number {
    return this.levels.length;
  }

  private levelAt(level: number): LevelState {
    if (!Number.isInteger(level) || level < 0 || level >= this.levels.length) {
      throw new RangeError(`level must be in [0, ${this.levels.length})`);
    }
    return this.levels[level];
  }

  levelWidth(level: number): number {
    return this.levelAt(level).width;
  }

  levelHeight(level: number): number {
    return this.levelAt(level).height;
  }

  /** Copy of the pixel data of a level (row-major, single channel). */
  getLevelPixels(level: number): Float64Array {
    return this.levelAt(level).pixels.slice();
  }

  /** True when no block is waiting for recomputation. */
  get isClean(): boolean {
    return this.lowestDirtyLevel() < 0;
  }

  /** Number of blocks currently awaiting recomputation on a level. */
  dirtyBlockCount(level: number): number {
    return this.levelAt(level).dirty.size;
  }

  /**
   * Pixel-precise union of the ranges invalidated since the level was last
   * fully clean, or null when the level has nothing pending.
   */
  pendingDirtyRect(level: number): Rect | null {
    const rect = this.levelAt(level).pendingRect;
    return rect === null ? null : { ...rect };
  }

  /**
   * Replace a rectangle of base-level pixels and invalidate exactly the
   * parent blocks whose kernel footprint intersects the edited range.
   */
  edit(x: number, y: number, w: number, h: number, data: ArrayLike<number>): void {
    const base = this.levels[0];
    if (!Number.isInteger(x) || !Number.isInteger(y) || !Number.isInteger(w) || !Number.isInteger(h)) {
      throw new RangeError('edit coordinates must be integers');
    }
    if (x < 0 || y < 0 || w < 1 || h < 1 || x + w > base.width || y + h > base.height) {
      throw new RangeError(`edit rect [${x},${y} ${w}x${h}] outside base level ${base.width}x${base.height}`);
    }
    if (data.length < w * h) {
      throw new RangeError(`edit data needs at least ${w * h} samples, got ${data.length}`);
    }

    this.epoch += 1;
    const e = this.epoch;

    for (let row = 0; row < h; row++) {
      const dst = (y + row) * base.width + x;
      for (let col = 0; col < w; col++) {
        base.pixels[dst + col] = data[row * w + col];
      }
    }

    // Base blocks are written directly: bump their version so in-flight
    // parent computations see the dependency change.
    const bs = this.blockSize;
    for (let by = Math.floor(y / bs); by <= Math.floor((y + h - 1) / bs); by++) {
      for (let bx = Math.floor(x / bs); bx <= Math.floor((x + w - 1) / bs); bx++) {
        const blk = base.blocks[by * base.blocksX + bx];
        blk.version = e;
        blk.cleanVersion = e;
      }
    }

    // Propagate the dirty range upwards. A parent pixel px depends on child
    // pixels [2*px - r, 2*px + r], so a dirty child range [c0, c1) maps to
    // parent pixels ceil((c0 - r) / 2) .. floor((c1 - 1 + r) / 2).
    let rx0 = x;
    let ry0 = y;
    let rx1 = x + w;
    let ry1 = y + h;
    const r = this.kernelRadius;
    for (let li = 1; li < this.levels.length; li++) {
      const level = this.levels[li];
      const px0 = Math.max(0, Math.ceil((rx0 - r) / 2));
      const py0 = Math.max(0, Math.ceil((ry0 - r) / 2));
      const px1 = Math.min(level.width, Math.floor((rx1 - 1 + r) / 2) + 1);
      const py1 = Math.min(level.height, Math.floor((ry1 - 1 + r) / 2) + 1);
      if (px1 <= px0 || py1 <= py0) break;
      this.markDirty(li, px0, py0, px1, py1, e);
      rx0 = px0;
      ry0 = py0;
      rx1 = px1;
      ry1 = py1;
    }
  }

  private markDirty(li: number, rx0: number, ry0: number, rx1: number, ry1: number, e: number): void {
    const level = this.levels[li];
    const bs = this.blockSize;
    for (let by = Math.floor(ry0 / bs); by <= Math.floor((ry1 - 1) / bs); by++) {
      for (let bx = Math.floor(rx0 / bs); bx <= Math.floor((rx1 - 1) / bs); bx++) {
        const bi = by * level.blocksX + bx;
        const blk = level.blocks[bi];
        if (blk.version < e) blk.version = e;
        level.dirty.add(bi);
      }
    }
    level.pendingRect = unionRect(level.pendingRect, { x: rx0, y: ry0, w: rx1 - rx0, h: ry1 - ry0 });
  }

  /**
   * Recompute dirty blocks, lowest level first so dependencies are always
   * clean before their parents. Processes at most maxBlocks blocks;
   * returns true when the pyramid is fully up to date.
   */
  update(maxBlocks: number = Number.POSITIVE_INFINITY): boolean {
    let remaining = maxBlocks;
    while (remaining > 0) {
      const li = this.lowestDirtyLevel();
      if (li < 0) return true;
      const level = this.levels[li];
      const bi = level.dirty.values().next().value!;
      this.computeBlock(li, bi);
      remaining -= 1;
    }
    return this.isClean;
  }

  private lowestDirtyLevel(): number {
    for (let li = 1; li < this.levels.length; li++) {
      if (this.levels[li].dirty.size > 0) return li;
    }
    return -1;
  }

  private computeBlock(li: number, bi: number): void {
    const level = this.levels[li];
    const child = this.levels[li - 1];
    const blk = level.blocks[bi];
    const snapshot = this.epoch;

    const bs = this.blockSize;
    const bx = bi % level.blocksX;
    const by = Math.floor(bi / level.blocksX);
    const x0 = bx * bs;
    const y0 = by * bs;
    const x1 = Math.min(x0 + bs, level.width);
    const y1 = Math.min(y0 + bs, level.height);

    const r = this.kernelRadius;
    const ks = this.kernel.size;
    const kw = this.kernel.weights;
    const cw = child.width;
    const ch = child.height;
    const cp = child.pixels;
    const out = this.scratch;

    for (let py = y0; py < y1; py++) {
      const cy = 2 * py;
      for (let px = x0; px < x1; px++) {
        const cx = 2 * px;
        let sum = 0;
        for (let ky = 0; ky < ks; ky++) {
          const sy = mirrorIndex(cy + ky - r, ch);
          const row = sy * cw;
          const krow = ky * ks;
          for (let kx = 0; kx < ks; kx++) {
            sum += kw[krow + kx] * cp[row + mirrorIndex(cx + kx - r, cw)];
          }
        }
        out[(py - y0) * bs + (px - x0)] = sum;
      }
    }

    if (this.onBlockComputed !== null) {
      this.onBlockComputed(li, bi);
    }

    // Stale-write guard: discard the result if this block, or any child
    // block inside its kernel footprint, was invalidated after the snapshot.
    if (blk.version > snapshot || !this.footprintClean(child, x0, y0, x1, y1, snapshot)) {
      this.stats.rejectedCommits += 1;
      return;
    }

    for (let py = y0; py < y1; py++) {
      const src = (py - y0) * bs;
      level.pixels.set(out.subarray(src, src + (x1 - x0)), py * level.width + x0);
    }
    blk.cleanVersion = blk.version;
    level.dirty.delete(bi);
    this.stats.computedBlocks += 1;
    if (level.dirty.size === 0) {
      level.pendingRect = null;
    }
  }

  /** True when no child block intersecting the kernel footprint of the
   * parent pixel range [x0,x1) x [y0,y1) changed after `snapshot`. */
  private footprintClean(child: LevelState, x0: number, y0: number, x1: number, y1: number, snapshot: number): boolean {
    const r = this.kernelRadius;
    const fx0 = Math.max(0, 2 * x0 - r);
    const fy0 = Math.max(0, 2 * y0 - r);
    const fx1 = Math.min(child.width, 2 * (x1 - 1) + r + 1);
    const fy1 = Math.min(child.height, 2 * (y1 - 1) + r + 1);
    const bs = this.blockSize;
    for (let by = Math.floor(fy0 / bs); by <= Math.floor((fy1 - 1) / bs); by++) {
      for (let bx = Math.floor(fx0 / bs); bx <= Math.floor((fx1 - 1) / bs); bx++) {
        if (child.blocks[by * child.blocksX + bx].version > snapshot) return false;
      }
    }
    return true;
  }
}
