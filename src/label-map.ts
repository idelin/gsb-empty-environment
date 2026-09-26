/**
 * 二值图连通分量增量标注。
 *
 * - 支持 4 / 8 连通。
 * - setPixel() 局部修改后只更新受影响的分量（合并 / 拆分），无关分量标签不变。
 * - 代表 ID 稳定：合并时保留参与合并的最小 ID；拆分时最大的一块保留原 ID
 *   （并列时保留最小像素索引所在块），其余块分配新 ID。
 */

export type Connectivity = 4 | 8;

const DIRS_4: ReadonlyArray<readonly [number, number]> = [
  [1, 0],
  [-1, 0],
  [0, 1],
  [0, -1],
];

const DIRS_8: ReadonlyArray<readonly [number, number]> = [
  ...DIRS_4,
  [1, 1],
  [1, -1],
  [-1, 1],
  [-1, -1],
];

export class LabelMap {
  readonly width: number;
  readonly height: number;
  readonly connectivity: Connectivity;

  /** 0/1 前景掩码，按行优先存储。 */
  private readonly mask: Uint8Array;
  /** 每个像素的分量 ID，0 表示背景。 */
  private readonly labels: Int32Array;
  /** 分量 ID -> 像素数。 */
  private readonly sizes = new Map<number, number>();
  private nextId = 1;

  constructor(width: number, height: number, connectivity: Connectivity = 4) {
    if (!Number.isInteger(width) || width <= 0) throw new RangeError("width must be a positive integer");
    if (!Number.isInteger(height) || height <= 0) throw new RangeError("height must be a positive integer");
    if (connectivity !== 4 && connectivity !== 8) throw new RangeError("connectivity must be 4 or 8");
    this.width = width;
    this.height = height;
    this.connectivity = connectivity;
    this.mask = new Uint8Array(width * height);
    this.labels = new Int32Array(width * height);
  }

  /** 由 0/1 掩码构建，内部做一次全量洪泛标注。 */
  static fromMask(mask: Uint8Array, width: number, height: number, connectivity: Connectivity = 4): LabelMap {
    if (mask.length !== width * height) throw new RangeError("mask length does not match width*height");
    const map = new LabelMap(width, height, connectivity);
    map.mask.set(mask);
    for (let idx = 0; idx < mask.length; idx++) {
      if (mask[idx] === 1 && map.labels[idx] === 0) {
        const id = map.nextId++;
        let count = 0;
        const stack = [idx];
        map.labels[idx] = id;
        while (stack.length > 0) {
          const cur = stack.pop()!;
          count++;
          for (const nb of map.neighborIndices(cur)) {
            if (map.mask[nb] === 1 && map.labels[nb] === 0) {
              map.labels[nb] = id;
              stack.push(nb);
            }
          }
        }
        map.sizes.set(id, count);
      }
    }
    return map;
  }

  /** 当前存活的分量数量。 */
  get componentCount(): number {
    return this.sizes.size;
  }

  /** 所有存活分量 ID（升序）。 */
  ids(): number[] {
    return [...this.sizes.keys()].sort((a, b) => a - b);
  }

  /** 某分量的像素数；不存在返回 0。 */
  sizeOf(id: number): number {
    return this.sizes.get(id) ?? 0;
  }

  isForeground(x: number, y: number): boolean {
    return this.mask[this.indexOf(x, y)] === 1;
  }

  /** 像素所属分量 ID，背景为 0。 */
  labelAt(x: number, y: number): number {
    return this.labels[this.indexOf(x, y)];
  }

  /** 整幅标签图的拷贝（0 = 背景）。 */
  labelsArray(): Int32Array {
    return this.labels.slice();
  }

  /** 某分量的全部像素索引（行优先）。 */
  pixelsOf(id: number): number[] {
    const out: number[] = [];
    for (let i = 0; i < this.labels.length; i++) {
      if (this.labels[i] === id) out.push(i);
    }
    return out;
  }

  /**
   * 局部修改一个像素并增量维护分量。
   * 无变化时直接返回。
   */
  setPixel(x: number, y: number, foreground: boolean): void {
    const idx = this.indexOf(x, y);
    const value = foreground ? 1 : 0;
    if (this.mask[idx] === value) return;
    if (value === 1) {
      this.addPixel(idx);
    } else {
      this.removePixel(idx);
    }
  }

  /** 打开一个前景像素：可能新建分量或把多个相邻分量合并。 */
  private addPixel(idx: number): void {
    this.mask[idx] = 1;
    // 记录每个相邻分量的 ID 和一个种子像素（即该邻居本身），合并时纯局部洪泛。
    const seeds = new Map<number, number>();
    for (const nb of this.neighborIndices(idx)) {
      const id = this.labels[nb];
      if (id !== 0 && !seeds.has(id)) seeds.set(id, nb);
    }

    if (seeds.size === 0) {
      const id = this.nextId++;
      this.labels[idx] = id;
      this.sizes.set(id, 1);
      return;
    }

    // 稳定代表：保留最小 ID，其余分量整体改标到 keep。
    const keep = Math.min(...seeds.keys());
    this.labels[idx] = keep;
    let total = 1;
    for (const [id, seed] of seeds) {
      total += this.sizes.get(id)!;
      if (id !== keep) {
        this.relabelFrom(seed, id, keep);
        this.sizes.delete(id);
      }
    }
    this.sizes.set(keep, total);
  }

  /** 关闭一个前景像素：其分量可能保持连通或被拆分。 */
  private removePixel(idx: number): void {
    const oldId = this.labels[idx];
    this.mask[idx] = 0;
    this.labels[idx] = 0;
    const remaining = this.sizes.get(oldId)! - 1;
    if (remaining === 0) {
      this.sizes.delete(oldId);
      return;
    }

    // 原分量被删掉一个像素后，每个碎片都至少有一个像素与被删像素相邻，
    // 因此只需从被删像素的邻居出发做局部洪泛即可找齐所有碎片。
    const visited = new Set<number>();
    const parts: number[][] = [];
    for (const seed of this.neighborIndices(idx)) {
      if (this.labels[seed] !== oldId || visited.has(seed)) continue;
      const part: number[] = [];
      const stack = [seed];
      visited.add(seed);
      while (stack.length > 0) {
        const cur = stack.pop()!;
        part.push(cur);
        for (const nb of this.neighborIndices(cur)) {
          if (this.labels[nb] === oldId && !visited.has(nb)) {
            visited.add(nb);
            stack.push(nb);
          }
        }
      }
      parts.push(part);
    }

    if (parts.length <= 1) {
      // 仍然连通，只更新大小。
      this.sizes.set(oldId, remaining);
      return;
    }

    // 拆分：最大的一块保留原 ID（并列取最小像素索引所在块），其余分配新 ID。
    parts.sort((a, b) => b.length - a.length || a[0] - b[0]);
    this.sizes.set(oldId, parts[0].length);
    for (let i = 1; i < parts.length; i++) {
      const id = this.nextId++;
      for (const p of parts[i]) this.labels[p] = id;
      this.sizes.set(id, parts[i].length);
    }
  }

  /** 从种子像素出发，把分量 from 的全部像素改标为 to（仅遍历 from 的像素）。 */
  private relabelFrom(seed: number, from: number, to: number): void {
    const stack = [seed];
    this.labels[seed] = to;
    while (stack.length > 0) {
      const cur = stack.pop()!;
      for (const nb of this.neighborIndices(cur)) {
        if (this.labels[nb] === from) {
          this.labels[nb] = to;
          stack.push(nb);
        }
      }
    }
  }

  private indexOf(x: number, y: number): number {
    if (x < 0 || x >= this.width || y < 0 || y >= this.height) {
      throw new RangeError(`pixel (${x}, ${y}) out of bounds ${this.width}x${this.height}`);
    }
    return y * this.width + x;
  }

  private neighborIndices(idx: number): number[] {
    const x = idx % this.width;
    const y = (idx / this.width) | 0;
    const dirs = this.connectivity === 4 ? DIRS_4 : DIRS_8;
    const out: number[] = [];
    for (const [dx, dy] of dirs) {
      const nx = x + dx;
      const ny = y + dy;
      if (nx >= 0 && nx < this.width && ny >= 0 && ny < this.height) {
        out.push(ny * this.width + nx);
      }
    }
    return out;
  }
}
