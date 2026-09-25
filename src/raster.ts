import type { BaselineSummary, Patch, RowChange, Run, Snapshot } from "./types.ts";
import {
  DecodeError,
  assertValidLabel,
  checksumRows,
  decodeRuns,
  rowDataToRuns,
  runsLength,
  sliceRuns,
  spliceRow,
  validateRuns,
} from "./rle.ts";

export interface RasterOptions {
  labelCount?: number;
  blockSize?: number;
  fill?: number;
}

export interface ValidationReport {
  ok: boolean;
  corruptedBlocks: number[];
  errors: string[];
}

export interface ShareReport {
  sharedBlocks: number;
  totalBlocks: number;
  sharedRows: number;
  totalRows: number;
}

export class Raster {
  readonly width: number;
  readonly height: number;
  readonly labelCount: number;
  readonly blockSize: number;
  readonly stats = { rowsDecoded: 0 };

  private rows: Run[][];
  private blockPixels: number[];
  private snapshots: Snapshot[] = [];
  private patches: Patch[] = [];
  private seq = 0;

  constructor(width: number, height: number, options: RasterOptions = {}) {
    if (!Number.isInteger(width) || width <= 0) throw new RangeError(`bad width ${width}`);
    if (!Number.isInteger(height) || height <= 0) throw new RangeError(`bad height ${height}`);
    this.width = width;
    this.height = height;
    this.labelCount = options.labelCount ?? 256;
    this.blockSize = options.blockSize ?? 16;
    if (!Number.isInteger(this.blockSize) || this.blockSize <= 0) {
      throw new RangeError(`bad blockSize ${this.blockSize}`);
    }
    const fill = options.fill ?? 0;
    assertValidLabel(fill, this.labelCount);
    this.rows = [];
    for (let y = 0; y < height; y++) this.rows.push([{ label: fill, length: width }]);
    const blockCount = Math.ceil(height / this.blockSize);
    this.blockPixels = new Array<number>(blockCount).fill(0);
    for (let y = 0; y < height; y++) {
      this.blockPixels[Math.floor(y / this.blockSize)] += width;
    }
  }

  private assertRect(x: number, y: number, w: number, h: number): void {
    for (const [name, v] of [["x", x], ["y", y], ["w", w], ["h", h]] as const) {
      if (!Number.isInteger(v)) throw new RangeError(`${name} must be an integer, got ${v}`);
    }
    if (x < 0 || y < 0 || w < 0 || h < 0 || x + w > this.width || y + h > this.height) {
      throw new RangeError(
        `rect (${x}, ${y}, ${w}, ${h}) out of bounds for ${this.width}x${this.height}`,
      );
    }
  }

  private setRow(rowIndex: number, runs: Run[]): void {
    const before = this.rows[rowIndex];
    const delta = runsLength(runs) - runsLength(before);
    this.rows[rowIndex] = runs;
    if (delta !== 0) {
      this.blockPixels[Math.floor(rowIndex / this.blockSize)] += delta;
    }
  }

  private summarize(): BaselineSummary {
    let totalPixels = 0;
    for (const pixels of this.blockPixels) totalPixels += pixels;
    return {
      width: this.width,
      height: this.height,
      totalPixels,
      blockPixels: this.blockPixels.slice(),
      checksum: checksumRows(this.rows),
    };
  }

  summary(): BaselineSummary {
    return this.summarize();
  }

  blockTotals(): number[] {
    return this.blockPixels.slice();
  }

  getRowRuns(y: number): Run[] {
    if (!Number.isInteger(y) || y < 0 || y >= this.height) {
      throw new RangeError(`row ${y} out of bounds`);
    }
    return this.rows[y].map((run) => ({ ...run }));
  }

  private recordPatch(
    kind: string,
    changes: RowChange[],
    baseline: BaselineSummary,
    result: BaselineSummary,
  ): Patch {
    const patch: Patch = { seq: this.seq++, kind, changes, baseline, result };
    this.patches.push(patch);
    return patch;
  }

  private updateRows(
    x: number,
    y: number,
    w: number,
    h: number,
    kind: string,
    replacementForRow: (dy: number) => Run[],
  ): Patch {
    const baseline = this.summarize();
    if (w === 0 || h === 0) {
      return this.recordPatch(kind, [], baseline, baseline);
    }
    const changes: RowChange[] = [];
    try {
      for (let dy = 0; dy < h; dy++) {
        const rowIndex = y + dy;
        const replacement = replacementForRow(dy);
        const before = this.rows[rowIndex];
        const after = spliceRow(before, x, w, replacement);
        this.setRow(rowIndex, after);
        changes.push({ row: rowIndex, before, after });
      }
    } catch (err) {
      for (let i = changes.length - 1; i >= 0; i--) {
        this.setRow(changes[i].row, changes[i].before);
      }
      throw err;
    }
    return this.recordPatch(kind, changes, baseline, this.summarize());
  }

  writeRect(x: number, y: number, w: number, h: number, data: ArrayLike<number>, kind = "writeRect"): Patch {
    this.assertRect(x, y, w, h);
    if (data.length !== w * h) {
      throw new DecodeError(`writeRect: expected ${w * h} pixels, received ${data.length}`);
    }
    return this.updateRows(x, y, w, h, kind, (dy) =>
      rowDataToRuns(data, dy * w, w, this.labelCount),
    );
  }

  fillRect(x: number, y: number, w: number, h: number, label: number): Patch {
    this.assertRect(x, y, w, h);
    assertValidLabel(label, this.labelCount);
    return this.updateRows(x, y, w, h, "fillRect", () => [{ label, length: w }]);
  }

  readRect(x: number, y: number, w: number, h: number): number[] {
    this.assertRect(x, y, w, h);
    const out = new Array<number>(w * h);
    for (let dy = 0; dy < h; dy++) {
      const decoded = decodeRuns(sliceRuns(this.rows[y + dy], x, w), w, this.labelCount);
      this.stats.rowsDecoded += 1;
      for (let dx = 0; dx < w; dx++) out[dy * w + dx] = decoded[dx];
    }
    return out;
  }

  translate(sx: number, sy: number, w: number, h: number, dx: number, dy: number): Patch {
    this.assertRect(sx, sy, w, h);
    this.assertRect(sx + dx, sy + dy, w, h);
    if (w === 0 || h === 0) {
      return this.updateRows(sx + dx, sy + dy, 0, 0, "translate", () => []);
    }
    // Read the full source region BEFORE any write, so overlapping
    // translations always see the pre-modification state.
    const data = this.readRect(sx, sy, w, h);
    return this.writeRect(sx + dx, sy + dy, w, h, data, "translate");
  }

  applyMask(
    x: number,
    y: number,
    w: number,
    h: number,
    mask: ArrayLike<boolean | number>,
    label: number,
  ): Patch {
    this.assertRect(x, y, w, h);
    assertValidLabel(label, this.labelCount);
    if (mask.length !== w * h) {
      throw new DecodeError(`applyMask: expected ${w * h} mask entries, received ${mask.length}`);
    }
    if (w === 0 || h === 0) return this.writeRect(x, y, w, h, [], "mask");
    const current = this.readRect(x, y, w, h);
    for (let i = 0; i < w * h; i++) {
      if (mask[i]) current[i] = label;
    }
    return this.writeRect(x, y, w, h, current, "mask");
  }

  applyPatch(patch: Patch, options?: { reverse?: boolean }): void {
    const reverse = options?.reverse === true;
    const expected = reverse ? patch.result : patch.baseline;
    const current = this.summarize();
    if (
      current.width !== expected.width ||
      current.height !== expected.height ||
      current.checksum !== expected.checksum
    ) {
      throw new Error(
        `applyPatch: ${reverse ? "result" : "baseline"} summary mismatch; ` +
          `patch #${patch.seq} does not apply to the current state`,
      );
    }
    for (const change of patch.changes) {
      this.setRow(change.row, reverse ? change.before : change.after);
    }
  }

  patchHistory(): Patch[] {
    return this.patches.slice();
  }

  snapshot(): number {
    const id = this.snapshots.length;
    this.snapshots.push({
      id,
      seq: this.seq,
      rows: this.rows.slice(),
      blockPixels: this.blockPixels.slice(),
      summary: this.summarize(),
    });
    return id;
  }

  restore(id: number): void {
    const snap = this.snapshots[id];
    if (!snap) throw new RangeError(`unknown snapshot ${id}`);
    this.rows = snap.rows.slice();
    this.blockPixels = snap.blockPixels.slice();
  }

  snapshotSummary(id: number): BaselineSummary {
    const snap = this.snapshots[id];
    if (!snap) throw new RangeError(`unknown snapshot ${id}`);
    return { ...snap.summary, blockPixels: snap.summary.blockPixels.slice() };
  }

  sharedBlocks(aId: number, bId: number): ShareReport {
    const a = this.snapshots[aId];
    const b = this.snapshots[bId];
    if (!a || !b) throw new RangeError("unknown snapshot");
    let sharedRows = 0;
    for (let y = 0; y < this.height; y++) {
      if (a.rows[y] === b.rows[y]) sharedRows++;
    }
    let sharedBlocks = 0;
    for (let block = 0; block < this.blockPixels.length; block++) {
      const start = block * this.blockSize;
      const end = Math.min(start + this.blockSize, this.height);
      let all = true;
      for (let y = start; y < end; y++) {
        if (a.rows[y] !== b.rows[y]) {
          all = false;
          break;
        }
      }
      if (all) sharedBlocks++;
    }
    return {
      sharedBlocks,
      totalBlocks: this.blockPixels.length,
      sharedRows,
      totalRows: this.height,
    };
  }

  validate(): ValidationReport {
    const errors: string[] = [];
    const corrupted = new Set<number>();
    const actualBlockPixels = new Array<number>(this.blockPixels.length).fill(0);
    for (let y = 0; y < this.height; y++) {
      const block = Math.floor(y / this.blockSize);
      try {
        validateRuns(this.rows[y], this.width, this.labelCount);
      } catch (err) {
        errors.push(`row ${y}: ${(err as Error).message}`);
        corrupted.add(block);
      }
      actualBlockPixels[block] += runsLength(this.rows[y]);
    }
    for (let block = 0; block < this.blockPixels.length; block++) {
      if (this.blockPixels[block] !== actualBlockPixels[block]) {
        errors.push(
          `block ${block}: index holds ${this.blockPixels[block]} pixels, actual ${actualBlockPixels[block]}`,
        );
        corrupted.add(block);
      }
    }
    return {
      ok: errors.length === 0,
      corruptedBlocks: [...corrupted].sort((a, b) => a - b),
      errors,
    };
  }

  decodeAll(): number[] {
    const out = new Array<number>(this.width * this.height);
    for (let y = 0; y < this.height; y++) {
      const decoded = decodeRuns(this.rows[y], this.width, this.labelCount);
      this.stats.rowsDecoded += 1;
      for (let x = 0; x < this.width; x++) out[y * this.width + x] = decoded[x];
    }
    return out;
  }

  resetStats(): void {
    this.stats.rowsDecoded = 0;
  }
}
