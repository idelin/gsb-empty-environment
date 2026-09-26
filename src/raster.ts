export type Label = number;

export interface Run {
  readonly label: Label;
  readonly len: number;
}

export interface RowData {
  readonly runs: readonly Run[];
}

interface Block {
  readonly rows: readonly RowData[];
  readonly pixelTotal: number;
  readonly labelCounts: ReadonlyMap<Label, number>;
}

export interface PatchBaseline {
  readonly width: number;
  readonly height: number;
  readonly baseSeq: number;
  readonly resultSeq: number;
  readonly affectedRows: number;
  readonly beforeHash: number;
  readonly afterHash: number;
}

export interface RowChange {
  readonly y: number;
  readonly before: RowData;
  readonly after: RowData;
}

export interface Patch {
  readonly baseline: PatchBaseline;
  readonly changes: readonly RowChange[];
}

export interface BlockInfo {
  readonly index: number;
  readonly rowStart: number;
  readonly rowCount: number;
  readonly pixelTotal: number;
  readonly labelCounts: ReadonlyMap<Label, number>;
}

export interface OpStats {
  rowsDecoded: number;
}

export class RasterError extends Error {}
export class InvalidLabelError extends RasterError {}
export class DecodeLengthError extends RasterError {}
export class CorruptBlockError extends RasterError {}
export class BaselineMismatchError extends RasterError {}
export class BoundsError extends RasterError {}

const HASH_SEED = 0x811c9dc5;

function hashRows(rows: readonly RowData[]): number {
  let h = HASH_SEED >>> 0;
  for (const row of rows) {
    for (const r of row.runs) {
      h ^= (r.label + 1) >>> 0;
      h = Math.imul(h, 0x01000193) >>> 0;
      h ^= r.len >>> 0;
      h = Math.imul(h, 0x01000193) >>> 0;
    }
  }
  return h >>> 0;
}

export function normalizeRuns(runs: readonly Run[]): Run[] {
  const out: Run[] = [];
  for (const r of runs) {
    const last = out[out.length - 1];
    if (last && last.label === r.label) {
      out[out.length - 1] = { label: last.label, len: last.len + r.len };
    } else {
      out.push({ label: r.label, len: r.len });
    }
  }
  return out;
}

export function decodeRow(
  runs: readonly Run[],
  width: number,
  isValidLabel: (label: Label) => boolean,
): Label[] {
  let total = 0;
  for (const r of runs) {
    if (!Number.isInteger(r.len) || r.len <= 0) {
      throw new DecodeLengthError(`run length must be a positive integer, got ${r.len}`);
    }
    if (!isValidLabel(r.label)) {
      throw new InvalidLabelError(`invalid label in run: ${r.label}`);
    }
    total += r.len;
  }
  if (total !== width) {
    throw new DecodeLengthError(`runs decode to ${total} pixels, expected row width ${width}`);
  }
  const out: Label[] = new Array(width);
  let offset = 0;
  for (const r of runs) {
    out.fill(r.label, offset, offset + r.len);
    offset += r.len;
  }
  return out;
}

export function encodeRow(pixels: ArrayLike<Label>): RowData {
  const runs: Run[] = [];
  let i = 0;
  const n = pixels.length;
  while (i < n) {
    const label = pixels[i];
    let j = i + 1;
    while (j < n && pixels[j] === label) j++;
    runs.push({ label, len: j - i });
    i = j;
  }
  return { runs };
}

function buildBlock(rows: readonly RowData[]): Block {
  let pixelTotal = 0;
  const labelCounts = new Map<Label, number>();
  for (const row of rows) {
    for (const r of row.runs) {
      pixelTotal += r.len;
      labelCounts.set(r.label, (labelCounts.get(r.label) ?? 0) + r.len);
    }
  }
  return { rows, pixelTotal, labelCounts };
}

interface Snapshot {
  readonly seq: number;
  readonly blocks: readonly Block[];
}

export interface RasterOptions {
  readonly fill?: Label;
  readonly labels?: readonly Label[];
  readonly blockRows?: number;
}

export class RasterImage {
  readonly width: number;
  readonly height: number;
  readonly blockRows: number;
  readonly stats: OpStats = { rowsDecoded: 0 };

  private blocks: Block[];
  private seq = 0;
  private readonly snapshots: Snapshot[] = [];
  private readonly labelSet: ReadonlySet<Label> | null;

  constructor(width: number, height: number, options: RasterOptions = {}) {
    if (!Number.isInteger(width) || width <= 0) throw new BoundsError(`bad width ${width}`);
    if (!Number.isInteger(height) || height <= 0) throw new BoundsError(`bad height ${height}`);
    this.width = width;
    this.height = height;
    this.blockRows = Math.max(1, options.blockRows ?? 8);
    this.labelSet = options.labels ? new Set(options.labels) : null;
    const fill = options.fill ?? 0;
    if (!this.isValidLabel(fill)) throw new InvalidLabelError(`invalid fill label ${fill}`);
    const row = encodeRow(new Array(width).fill(fill));
    const rows: RowData[] = [];
    for (let y = 0; y < height; y++) rows.push(row);
    this.blocks = [];
    for (let start = 0; start < height; start += this.blockRows) {
      this.blocks.push(buildBlock(rows.slice(start, start + this.blockRows)));
    }
  }

  get version(): number {
    return this.seq;
  }

  get blockCount(): number {
    return this.blocks.length;
  }

  isValidLabel(label: Label): boolean {
    if (!Number.isInteger(label) || label < 0 || label > 0xffffffff) return false;
    return this.labelSet ? this.labelSet.has(label) : true;
  }

  private readonly checkLabel = (label: Label): boolean => this.isValidLabel(label);

  resetStats(): void {
    this.stats.rowsDecoded = 0;
  }

  private checkRect(x: number, y: number, w: number, h: number): void {
    const ok =
      Number.isInteger(x) && Number.isInteger(y) &&
      Number.isInteger(w) && Number.isInteger(h) &&
      x >= 0 && y >= 0 && w >= 0 && h >= 0 &&
      x + w <= this.width && y + h <= this.height;
    if (!ok) throw new BoundsError(`rect (${x},${y},${w},${h}) outside ${this.width}x${this.height}`);
  }

  private rowAt(y: number): RowData {
    const bi = Math.floor(y / this.blockRows);
    return this.blocks[bi].rows[y - bi * this.blockRows];
  }

  rowRuns(y: number): readonly Run[] {
    return this.rowAt(y).runs;
  }

  blockInfo(index: number): BlockInfo {
    const b = this.blocks[index];
    if (!b) throw new BoundsError(`no block ${index}`);
    return {
      index,
      rowStart: index * this.blockRows,
      rowCount: b.rows.length,
      pixelTotal: b.pixelTotal,
      labelCounts: b.labelCounts,
    };
  }

  validate(): void {
    for (let bi = 0; bi < this.blocks.length; bi++) {
      const b = this.blocks[bi];
      let sum = 0;
      for (const row of b.rows) {
        for (const r of row.runs) sum += r.len;
      }
      const expected = this.width * b.rows.length;
      if (sum !== expected || b.pixelTotal !== expected) {
        throw new CorruptBlockError(
          `block ${bi}: runs sum to ${sum}, index says ${b.pixelTotal}, expected ${expected}`,
        );
      }
    }
  }

  readRect(x: number, y: number, w: number, h: number): Label[] {
    this.checkRect(x, y, w, h);
    const out: Label[] = new Array(w * h);
    for (let ry = 0; ry < h; ry++) {
      const px = decodeRow(this.rowAt(y + ry).runs, this.width, this.checkLabel);
      this.stats.rowsDecoded++;
      for (let rx = 0; rx < w; rx++) out[ry * w + rx] = px[x + rx];
    }
    return out;
  }

  writeRect(x: number, y: number, w: number, h: number, src: Label | ArrayLike<Label>): Patch {
    this.checkRect(x, y, w, h);
    const isArray = typeof src !== "number";
    if (isArray && src.length !== w * h) {
      throw new DecodeLengthError(`source has ${src.length} labels, rect needs ${w * h}`);
    }
    if (w === 0 || h === 0) return this.emptyPatch();
    const fns = new Map<number, (px: Label[]) => void>();
    for (let ry = 0; ry < h; ry++) {
      fns.set(y + ry, (px) => {
        for (let rx = 0; rx < w; rx++) {
          const label = isArray ? (src as ArrayLike<Label>)[ry * w + rx] : (src as Label);
          if (!this.isValidLabel(label)) {
            throw new InvalidLabelError(`invalid label ${label} at rect offset (${rx},${ry})`);
          }
          px[x + rx] = label;
        }
      });
    }
    return this.updateRows(fns);
  }

  applyMask(
    x: number,
    y: number,
    w: number,
    h: number,
    mask: ArrayLike<boolean | number>,
    label: Label,
  ): Patch {
    this.checkRect(x, y, w, h);
    if (!this.isValidLabel(label)) throw new InvalidLabelError(`invalid mask label ${label}`);
    if (mask.length !== w * h) {
      throw new DecodeLengthError(`mask has ${mask.length} entries, rect needs ${w * h}`);
    }
    if (w === 0 || h === 0) return this.emptyPatch();
    const fns = new Map<number, (px: Label[]) => void>();
    for (let ry = 0; ry < h; ry++) {
      fns.set(y + ry, (px) => {
        for (let rx = 0; rx < w; rx++) {
          if (mask[ry * w + rx]) px[x + rx] = label;
        }
      });
    }
    return this.updateRows(fns);
  }

  translateRegion(x: number, y: number, w: number, h: number, dx: number, dy: number): Patch {
    this.checkRect(x, y, w, h);
    if (w === 0 || h === 0 || (dx === 0 && dy === 0)) return this.emptyPatch();
    const src = this.readRect(x, y, w, h);
    const nx = x + dx;
    const ny = y + dy;
    const cx0 = Math.max(nx, 0);
    const cy0 = Math.max(ny, 0);
    const cx1 = Math.min(nx + w, this.width);
    const cy1 = Math.min(ny + h, this.height);
    if (cx1 <= cx0 || cy1 <= cy0) return this.emptyPatch();
    const cw = cx1 - cx0;
    const fns = new Map<number, (px: Label[]) => void>();
    for (let ry = cy0; ry < cy1; ry++) {
      const srcRow = ry - ny;
      const srcCol = cx0 - nx;
      fns.set(ry, (px) => {
        for (let i = 0; i < cw; i++) px[cx0 + i] = src[srcRow * w + srcCol + i];
      });
    }
    return this.updateRows(fns);
  }

  applyPatch(patch: Patch, inverse = false): void {
    const b = patch.baseline;
    if (b.width !== this.width || b.height !== this.height) {
      throw new BaselineMismatchError(
        `patch baseline ${b.width}x${b.height} != image ${this.width}x${this.height}`,
      );
    }
    const current = patch.changes.map((c) => this.rowAt(c.y));
    const currentHash = hashRows(current);
    const wantHash = inverse ? b.afterHash : b.beforeHash;
    if (currentHash !== wantHash) {
      throw new BaselineMismatchError(
        `affected rows do not match patch ${inverse ? "after" : "before"} summary`,
      );
    }
    const changes: RowChange[] = patch.changes.map((c) => ({
      y: c.y,
      before: inverse ? c.after : c.before,
      after: inverse ? c.before : c.after,
    }));
    this.commitChanges(changes);
  }

  snapshot(): number {
    this.snapshots.push({ seq: this.seq, blocks: this.blocks });
    return this.snapshots.length - 1;
  }

  restoreSnapshot(id: number): void {
    const s = this.snapshots[id];
    if (!s) throw new BoundsError(`no snapshot ${id}`);
    this.blocks = s.blocks.slice();
    this.seq = s.seq;
  }

  compareWithSnapshot(id: number): { shared: number; total: number } {
    const s = this.snapshots[id];
    if (!s) throw new BoundsError(`no snapshot ${id}`);
    let shared = 0;
    for (let i = 0; i < this.blocks.length; i++) {
      if (this.blocks[i] === s.blocks[i]) shared++;
    }
    return { shared, total: this.blocks.length };
  }

  debugTamperRunLength(y: number, runIndex: number, newLen: number): void {
    const row = this.rowAt(y);
    const runs = row.runs.slice();
    const r = runs[runIndex];
    if (!r) throw new BoundsError(`no run ${runIndex} in row ${y}`);
    runs[runIndex] = { label: r.label, len: newLen };
    const bi = Math.floor(y / this.blockRows);
    const rows = this.blocks[bi].rows.slice();
    rows[y - bi * this.blockRows] = { runs };
    const next = this.blocks.slice();
    next[bi] = buildBlock(rows);
    this.blocks = next;
  }

  private emptyPatch(): Patch {
    return {
      baseline: {
        width: this.width,
        height: this.height,
        baseSeq: this.seq,
        resultSeq: this.seq,
        affectedRows: 0,
        beforeHash: hashRows([]),
        afterHash: hashRows([]),
      },
      changes: [],
    };
  }

  private updateRows(fns: Map<number, (px: Label[]) => void>): Patch {
    if (fns.size === 0) return this.emptyPatch();
    const changes: RowChange[] = [];
    for (const [y, fn] of fns) {
      const before = this.rowAt(y);
      const px = decodeRow(before.runs, this.width, this.checkLabel);
      this.stats.rowsDecoded++;
      fn(px);
      changes.push({ y, before, after: encodeRow(px) });
    }
    return this.commitChanges(changes);
  }

  private commitChanges(changes: RowChange[]): Patch {
    const beforeHash = hashRows(changes.map((c) => c.before));
    const afterHash = hashRows(changes.map((c) => c.after));
    const byBlock = new Map<number, Map<number, RowData>>();
    for (const c of changes) {
      const bi = Math.floor(c.y / this.blockRows);
      let m = byBlock.get(bi);
      if (!m) byBlock.set(bi, (m = new Map()));
      m.set(c.y - bi * this.blockRows, c.after);
    }
    const next = this.blocks.slice();
    for (const [bi, m] of byBlock) {
      const rows = next[bi].rows.slice();
      for (const [ri, row] of m) rows[ri] = row;
      next[bi] = buildBlock(rows);
    }
    this.blocks = next;
    const baseline: PatchBaseline = {
      width: this.width,
      height: this.height,
      baseSeq: this.seq,
      resultSeq: this.seq + 1,
      affectedRows: changes.length,
      beforeHash,
      afterHash,
    };
    this.seq++;
    return { baseline, changes };
  }
}
