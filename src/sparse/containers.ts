/**
 * 稀疏数组的分块容器：Map / RLE / Dense 三种表示，
 * 依据密度与游程结构自适应转换，追求空间利用率极致。
 *
 * 自适应策略（每次写入后按块评估，或 optimize() 全量评估）：
 *  - 密度 >= DENSE_THRESHOLD        → DenseChunk（数组寻址 O(1)）
 *  - 游程数 * RUN_COST <= 元素数     → RleChunk（行程编码，重复值场景最优）
 *  - 否则                            → MapChunk（散列，极低密度最优）
 *  - 空块                            → 释放（null）
 */

export const CHUNK_BITS = 12;
export const CHUNK_SIZE = 1 << CHUNK_BITS; // 4096
export const CHUNK_MASK = CHUNK_SIZE - 1;

const DENSE_THRESHOLD = 0.75; // 密度 >= 75% 转 Dense
const RUN_COST = 3; // 每条游程约折合 3 个 Map 项的开销
const MAP_ENTRY_BYTES = 24; // 散列项估算
const RUN_BYTES = 40; // 游程项估算
const DENSE_SLOT_BYTES = 8;
const CHUNK_HEADER_BYTES = 32;

/** 泄漏追踪：所有活块注册，dispose 后必须归零。 */
export class LeakRegistry {
  private static liveChunks = 0;
  private static liveArrays = 0;

  static acquireChunk(): void { LeakRegistry.liveChunks++; }
  static releaseChunk(): void {
    LeakRegistry.liveChunks--;
    if (LeakRegistry.liveChunks < 0) throw new Error("chunk released twice");
  }
  static acquireArray(): void { LeakRegistry.liveArrays++; }
  static releaseArray(): void {
    LeakRegistry.liveArrays--;
    if (LeakRegistry.liveArrays < 0) throw new Error("array released twice");
  }
  static get chunks(): number { return LeakRegistry.liveChunks; }
  static get arrays(): number { return LeakRegistry.liveArrays; }
  static assertNoLeaks(): void {
    if (LeakRegistry.liveChunks !== 0 || LeakRegistry.liveArrays !== 0) {
      throw new Error(
        `leak detected: ${LeakRegistry.liveChunks} chunks, ${LeakRegistry.liveArrays} arrays still live`,
      );
    }
  }
}

export interface Chunk<T> {
  readonly kind: "map" | "rle" | "dense";
  readonly count: number;
  get(offset: number): T | undefined;
  has(offset: number): boolean;
  set(offset: number, value: T): Chunk<T>; // 可能触发容器转换
  delete(offset: number): Chunk<T> | null; // null 表示块已空
  entries(): IterableIterator<[number, T]>;
  memoryBytes(): number;
  dispose(): void;
}

export class MapChunk<T> implements Chunk<T> {
  readonly kind = "map" as const;
  private readonly map = new Map<number, T>();

  constructor(entries?: Iterable<[number, T]>) {
    LeakRegistry.acquireChunk();
    if (entries) for (const [k, v] of entries) this.map.set(k, v);
  }

  get count(): number { return this.map.size; }
  get(offset: number): T | undefined { return this.map.get(offset); }
  has(offset: number): boolean { return this.map.has(offset); }

  set(offset: number, value: T): Chunk<T> {
    this.map.set(offset, value);
    return maybeConvert(this);
  }

  delete(offset: number): Chunk<T> | null {
    this.map.delete(offset);
    if (this.map.size === 0) { this.dispose(); return null; }
    return maybeConvert(this);
  }

  *entries(): IterableIterator<[number, T]> {
    // 按下标升序（压缩 delta 编码与游程估算依赖有序性）
    yield* [...this.map.entries()].sort((a, b) => a[0] - b[0]);
  }

  memoryBytes(): number {
    return CHUNK_HEADER_BYTES + this.map.size * MAP_ENTRY_BYTES;
  }

  dispose(): void { this.map.clear(); LeakRegistry.releaseChunk(); }
}

export class RleChunk<T> implements Chunk<T> {
  readonly kind = "rle" as const;
  /** 升序不相交游程：[start, end] 闭区间共享同一值。 */
  private runs: { start: number; end: number; value: T }[] = [];
  private size = 0;

  constructor(entries?: Iterable<[number, T]>) {
    LeakRegistry.acquireChunk();
    if (entries) {
      const sorted = [...entries].sort((a, b) => a[0] - b[0]);
      for (const [offset, value] of sorted) this.insertSorted(offset, value);
    }
  }

  private insertSorted(offset: number, value: T): void {
    // 构造期专用：offset 严格递增
    const last = this.runs[this.runs.length - 1];
    if (last && last.end + 1 === offset && Object.is(last.value, value)) {
      last.end = offset;
    } else {
      this.runs.push({ start: offset, end: offset, value });
    }
    this.size++;
  }

  get count(): number { return this.size; }
  get runCount(): number { return this.runs.length; }

  private locate(offset: number): number {
    // 二分查找：返回包含 offset 的游程下标，或 -(插入点)-1
    let lo = 0, hi = this.runs.length - 1;
    while (lo <= hi) {
      const mid = (lo + hi) >>> 1;
      const r = this.runs[mid];
      if (offset < r.start) hi = mid - 1;
      else if (offset > r.end) lo = mid + 1;
      else return mid;
    }
    return ~lo;
  }

  get(offset: number): T | undefined {
    const i = this.locate(offset);
    return i >= 0 ? this.runs[i].value : undefined;
  }

  has(offset: number): boolean { return this.locate(offset) >= 0; }

  set(offset: number, value: T): Chunk<T> {
    const i = this.locate(offset);
    if (i >= 0) {
      const r = this.runs[i];
      if (Object.is(r.value, value)) return this;
      // 分裂游程
      const newRuns: typeof this.runs = [];
      if (r.start < offset) newRuns.push({ start: r.start, end: offset - 1, value: r.value });
      newRuns.push({ start: offset, end: offset, value });
      if (offset < r.end) newRuns.push({ start: offset + 1, end: r.end, value: r.value });
      this.runs.splice(i, 1, ...newRuns);
      return maybeConvert(this);
    }
    const at = ~i;
    this.size++;
    const prev = this.runs[at - 1];
    const next = this.runs[at];
    const mergePrev = prev && prev.end + 1 === offset && Object.is(prev.value, value);
    const mergeNext = next && next.start - 1 === offset && Object.is(next.value, value);
    if (mergePrev && mergeNext) {
      prev.end = next.end;
      this.runs.splice(at, 1);
    } else if (mergePrev) {
      prev.end = offset;
    } else if (mergeNext) {
      next.start = offset;
    } else {
      this.runs.splice(at, 0, { start: offset, end: offset, value });
    }
    return maybeConvert(this);
  }

  delete(offset: number): Chunk<T> | null {
    const i = this.locate(offset);
    if (i < 0) return this;
    const r = this.runs[i];
    this.size--;
    if (r.start === r.end) this.runs.splice(i, 1);
    else if (offset === r.start) r.start++;
    else if (offset === r.end) r.end--;
    else {
      const right = { start: offset + 1, end: r.end, value: r.value };
      r.end = offset - 1;
      this.runs.splice(i + 1, 0, right);
    }
    if (this.size === 0) { this.dispose(); return null; }
    return maybeConvert(this);
  }

  *entries(): IterableIterator<[number, T]> {
    for (const r of this.runs) {
      for (let o = r.start; o <= r.end; o++) yield [o, r.value];
    }
  }

  memoryBytes(): number {
    return CHUNK_HEADER_BYTES + this.runs.length * RUN_BYTES;
  }

  dispose(): void { this.runs = []; LeakRegistry.releaseChunk(); }
}

export class DenseChunk<T> implements Chunk<T> {
  readonly kind = "dense" as const;
  private slots: (T | undefined)[] = new Array<T | undefined>(CHUNK_SIZE);
  private readonly present = new Uint8Array(CHUNK_SIZE);
  private size = 0;

  constructor(entries?: Iterable<[number, T]>) {
    LeakRegistry.acquireChunk();
    if (entries) for (const [o, v] of entries) this.set(o, v);
  }

  get count(): number { return this.size; }
  get(offset: number): T | undefined { return this.present[offset] ? this.slots[offset] : undefined; }
  has(offset: number): boolean { return this.present[offset] === 1; }

  set(offset: number, value: T): Chunk<T> {
    if (!this.present[offset]) { this.present[offset] = 1; this.size++; }
    this.slots[offset] = value;
    return this;
  }

  delete(offset: number): Chunk<T> | null {
    if (this.present[offset]) {
      this.present[offset] = 0;
      this.slots[offset] = undefined;
      this.size--;
    }
    if (this.size === 0) { this.dispose(); return null; }
    return maybeConvert(this);
  }

  *entries(): IterableIterator<[number, T]> {
    for (let o = 0; o < CHUNK_SIZE; o++) {
      if (this.present[o]) yield [o, this.slots[o] as T];
    }
  }

  memoryBytes(): number {
    return CHUNK_HEADER_BYTES + CHUNK_SIZE * DENSE_SLOT_BYTES + CHUNK_SIZE;
  }

  dispose(): void {
    this.slots = [];
    LeakRegistry.releaseChunk();
  }
}

/**
 * 依据密度与游程结构选择最优容器；转换时释放旧容器。
 * 游程估算为 O(块大小)，仅在写入计数对齐 64 边界时采样，
 * 避免批量写入退化为 O(n^2)；optimize() 会强制全量评估。
 */
function maybeConvert<T>(chunk: Chunk<T>, force = false): Chunk<T> {
  const count = chunk.count;
  if (count === 0) return chunk;
  const density = count / CHUNK_SIZE;

  let target: "map" | "rle" | "dense";
  if (density >= DENSE_THRESHOLD) {
    target = "dense";
  } else if (chunk.kind === "rle") {
    const runCount = (chunk as RleChunk<T>).runCount;
    target = runCount * RUN_COST <= count ? "rle" : "map";
  } else if (force || (count & 63) === 0) {
    const runCount = estimateRuns(chunk);
    target = runCount * RUN_COST <= count ? "rle" : "map";
  } else {
    target = chunk.kind;
  }
  if (target === chunk.kind) return chunk;
  return convert(chunk, target);
}

/** 强制全量评估（供 optimize 使用）。 */
export function optimizeChunk<T>(chunk: Chunk<T>): Chunk<T> {
  return maybeConvert(chunk, true);
}

function estimateRuns<T>(chunk: Chunk<T>): number {
  let runs = 0;
  let prevOffset = -2;
  let prevValue: T | undefined;
  for (const [offset, value] of chunk.entries()) {
    if (offset !== prevOffset + 1 || !Object.is(value, prevValue)) runs++;
    prevOffset = offset;
    prevValue = value;
  }
  return runs;
}

function convert<T>(chunk: Chunk<T>, target: "map" | "rle" | "dense"): Chunk<T> {
  const entries = [...chunk.entries()];
  const next: Chunk<T> = target === "dense"
    ? new DenseChunk(entries)
    : target === "rle"
      ? new RleChunk(entries)
      : new MapChunk(entries);
  chunk.dispose();
  return next;
}
