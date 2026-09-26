/**
 * 逐层降采样瓦片金字塔。
 *
 * 设计要点：
 * - 每层尺寸 ceil(W/2^L) x ceil(H/2^L)，瓦片边缘可小于 tileSize（精确覆盖）。
 * - 瓦片数据不可变（编辑写入 base，瓦片全部重新拷贝/发布新版本），保证读快照隔离。
 * - 每个瓦片键保存版本链（data 版本 + 墓碑版本，按代际 gen 升序）：
 *   局部编辑仅沿“包含该像素”的祖先瓦片逐层打墓碑；旧代际任务完成时若
 *   已出现更高墓碑，则禁止发布，旧像素无法复活。
 * - 同一瓦片的并发请求合并到一个在飞任务（waiters 引用计数）；
 *   所有等待者取消后任务中止，临时缓冲立即释放并计入统计。
 * - 缓存有字节额度，超限时按 LRU 淘汰未被活动快照钉住的版本；统计峰值。
 * - 任意区域读：先获取快照代际，跨层按需选取/生成瓦片，精确裁剪瓦片边缘
 *   与图像边界，读期间快照代际钉住所需旧版本（MVCC 风格原子隔离）。
 */

import type { RGBAImage } from './image.ts';
import { levelDim, setPixel as setBasePixel } from './image.ts';

export interface RegionResult {
  data: Uint8Array;
  width: number;
  height: number;
  x: number;
  y: number;
  level: number;
}

export interface PyramidStats {
  hits: number;
  misses: number;
  /** 实际完成并发布的瓦片计算次数 */
  computes: number;
  evictions: number;
  invalidations: number;
  cancels: number;
  /** 因取消（旧代际丢弃不计）而释放的临时缓冲字节数 */
  releasedBytes: number;
  currentBytes: number;
  peakBytes: number;
}

export interface Hooks {
  /** 瓦片任务为瓦片缓冲分配空间后、真正计算前等待（测试用于确定性交错） */
  gate?: (key: string) => void | Promise<void>;
  /** 每次实际开始计算一个瓦片（含合并后仅一次） */
  onCompute?: (key: string) => void;
}

export class SnapshotExpiredError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SnapshotExpiredError';
  }
}

interface Version {
  gen: number;
  /** null 表示墓碑（该代际瓦片失效标记） */
  data: Uint8Array | null;
  lastUsed: number;
}

interface InflightTask {
  key: string;
  startGen: number;
  waiters: number;
  aborted: boolean;
  settled: boolean;
  tempBytes: number;
  promise: Promise<Uint8Array | null>;
}

function abortError(): Error {
  const e = new Error('The tile request was aborted');
  e.name = 'AbortError';
  return e;
}

export class TilePyramid {
  readonly base: RGBAImage;
  readonly tileSize: number;
  readonly maxCacheBytes: number;
  readonly levels: { width: number; height: number }[];
  readonly hooks: Hooks;
  readonly stats: PyramidStats = {
    hits: 0,
    misses: 0,
    computes: 0,
    evictions: 0,
    invalidations: 0,
    cancels: 0,
    releasedBytes: 0,
    currentBytes: 0,
    peakBytes: 0,
  };

  /** 当前图像代际；每次编辑 +1 */
  generation = 0;

  private versions = new Map<string, Version[]>();
  private inflight = new Map<string, InflightTask>();
  /** 活动读快照代际 -> 引用计数 */
  private activeSnapshots = new Map<number, number>();
  private clock = 0;

  constructor(base: RGBAImage, options: { tileSize?: number; maxCacheBytes?: number; hooks?: Hooks } = {}) {
    this.base = base;
    this.tileSize = options.tileSize ?? 4;
    this.maxCacheBytes = options.maxCacheBytes ?? 1 << 20;
    this.hooks = options.hooks ?? {};

    const levels: { width: number; height: number }[] = [];
    for (let l = 0; ; l++) {
      const width = levelDim(base.width, l);
      const height = levelDim(base.height, l);
      levels.push({ width, height });
      if (width === 1 && height === 1) break;
    }
    this.levels = levels;
  }

  levelCount(): number {
    return this.levels.length;
  }

  levelWidth(level: number): number {
    return this.levels[level].width;
  }

  levelHeight(level: number): number {
    return this.levels[level].height;
  }

  tilesX(level: number): number {
    return Math.ceil(this.levels[level].width / this.tileSize);
  }

  tilesY(level: number): number {
    return Math.ceil(this.levels[level].height / this.tileSize);
  }

  keyOf(level: number, tx: number, ty: number): string {
    return `${level}:${tx}:${ty}`;
  }

  // ---------------------------------------------------------------- 快照

  /** 捕获当前代际作为读快照；读完必须 release。 */
  acquireSnapshot(): number {
    const g = this.generation;
    this.activeSnapshots.set(g, (this.activeSnapshots.get(g) ?? 0) + 1);
    return g;
  }

  releaseSnapshot(gen: number): void {
    const n = this.activeSnapshots.get(gen);
    if (n === undefined) return;
    if (n <= 1) this.activeSnapshots.delete(gen);
    else this.activeSnapshots.set(gen, n - 1);
  }

  // ---------------------------------------------------------------- 编辑

  /**
   * 修改一个基准像素，然后仅对每一层“包含该像素投影”的瓦片打墓碑。
   * 返回被失效的瓦片键（祖先链）。
   */
  setPixel(x: number, y: number, r: number, g: number, b: number, a: number): string[] {
    setBasePixel(this.base, x, y, r, g, b, a);
    this.generation++;
    const invalidated: string[] = [];
    for (let l = 0; l < this.levels.length; l++) {
      const lx = Math.floor(x / 2 ** l);
      const ly = Math.floor(y / 2 ** l);
      const tx = Math.floor(lx / this.tileSize);
      const ty = Math.floor(ly / this.tileSize);
      const key = this.keyOf(l, tx, ty);
      invalidated.push(key);
      this.pushTombstone(key);
    }
    return invalidated;
  }

  private pushTombstone(key: string): void {
    let arr = this.versions.get(key);
    if (!arr) {
      arr = [];
      this.versions.set(key, arr);
    }
    arr.push({ gen: this.generation, data: null, lastUsed: 0 });
    this.stats.invalidations++;
  }

  // ---------------------------------------------------------------- 读取

  /**
   * 获取瓦片。
   * - 不传 snapshotGen：读取“当前代际”，遇到被旧任务丢弃等情况自动重试。
   * - 传 snapshotGen：严格 MVCC 读；若该瓦片在快照代际之前已失效且旧版本
   *   未被缓存钉住，抛 SnapshotExpiredError。
   */
  async getTile(
    level: number,
    tx: number,
    ty: number,
    opts: { snapshotGen?: number; signal?: AbortSignal } = {},
  ): Promise<Uint8Array> {
    const key = this.keyOf(level, tx, ty);
    for (;;) {
      if (opts.signal?.aborted) throw abortError();
      const fixed = opts.snapshotGen !== undefined;
      const gen = opts.snapshotGen ?? this.generation;
      const hit = this.lookupHit(key, gen);
      if (hit && hit.data) {
        this.stats.hits++;
        hit.lastUsed = ++this.clock;
        return hit.data;
      }
      this.stats.misses++;

      let task: InflightTask;
      try {
        task = this.obtainTask(key, level, tx, ty, gen, fixed);
      } catch (e) {
        if (!fixed && e instanceof SnapshotExpiredError) continue;
        throw e;
      }

      let abortedHere = false;
      const onAbort = () => {
        abortedHere = true;
        this.detachWaiter(task);
      };
      opts.signal?.addEventListener('abort', onAbort, { once: true });
      task.waiters++;
      try {
        const data = await task.promise;
        if (abortedHere) throw abortError();
        if (data) return data;
      } catch (e) {
        // 非固定代际请求：任务因旧版本不可恢复而失败时，按当代际重试。
        if (!fixed && e instanceof SnapshotExpiredError) continue;
        throw e;
      } finally {
        opts.signal?.removeEventListener('abort', onAbort);
        if (!abortedHere) this.detachWaiter(task);
      }
    }
  }

  /**
   * 读取任意矩形区域（坐标为该层像素坐标），结果严格裁剪到层边界，
   * 只复制与请求矩形相交的瓦片片段（精确边缘覆盖）。
   * 整个读取持有同一个快照代际，与期间发生的编辑原子隔离。
   */
  async readRegion(level: number, x: number, y: number, w: number, h: number): Promise<RegionResult> {
    const gen = this.acquireSnapshot();
    try {
      const lw = this.levelWidth(level);
      const lh = this.levelHeight(level);
      const x0 = Math.max(0, Math.min(x, lw));
      const y0 = Math.max(0, Math.min(y, lh));
      const x1 = Math.max(0, Math.min(x + w, lw));
      const y1 = Math.max(0, Math.min(y + h, lh));
      const outW = x1 - x0;
      const outH = y1 - y0;
      const out = new Uint8Array(outW * outH * 4);

      const tx0 = Math.floor(x0 / this.tileSize);
      const ty0 = Math.floor(y0 / this.tileSize);
      const tx1 = outW > 0 ? Math.floor((x1 - 1) / this.tileSize) : tx0 - 1;
      const ty1 = outH > 0 ? Math.floor((y1 - 1) / this.tileSize) : ty0 - 1;

      for (let ty = ty0; ty <= ty1; ty++) {
        for (let tx = tx0; tx <= tx1; tx++) {
          const tile = await this.getTile(level, tx, ty, { snapshotGen: gen });
          this.blitTile(tile, level, tx, ty, x0, y0, x1, y1, out, outW);
        }
      }
      return { data: out, width: outW, height: outH, x: x0, y: y0, level };
    } finally {
      this.releaseSnapshot(gen);
    }
  }

  /**
   * 跨层读取：给定基准层矩形和目标输出尺寸，选择分辨率不低于目标的最粗层，
   * 从该层瓦片读取后做最近邻重采样。
   */
  async readScaled(x: number, y: number, w: number, h: number, outW: number, outH: number): Promise<RegionResult> {
    let level = 0;
    for (let l = 0; l < this.levels.length; l++) {
      if (this.levels[l].width >= outW && this.levels[l].height >= outH) level = l;
    }
    const s = 2 ** level;
    const lx0 = Math.floor(x / s);
    const ly0 = Math.floor(y / s);
    const lx1 = Math.ceil((x + w) / s);
    const ly1 = Math.ceil((y + h) / s);
    const region = await this.readRegion(level, lx0, ly0, lx1 - lx0, ly1 - ly0);

    const out = new Uint8Array(outW * outH * 4);
    for (let oy = 0; oy < outH; oy++) {
      const sy = Math.min(region.height - 1, Math.floor(((oy + 0.5) * region.height) / outH));
      for (let ox = 0; ox < outW; ox++) {
        const sx = Math.min(region.width - 1, Math.floor(((ox + 0.5) * region.width) / outW));
        const si = (sy * region.width + sx) * 4;
        const di = (oy * outW + ox) * 4;
        out[di] = region.data[si];
        out[di + 1] = region.data[si + 1];
        out[di + 2] = region.data[si + 2];
        out[di + 3] = region.data[si + 3];
      }
    }
    return { data: out, width: outW, height: outH, x, y, level };
  }

  private blitTile(
    tile: Uint8Array,
    level: number,
    tx: number,
    ty: number,
    x0: number,
    y0: number,
    x1: number,
    y1: number,
    out: Uint8Array,
    outW: number,
  ): void {
    const tw = Math.min(this.tileSize, this.levels[level].width - tx * this.tileSize);
    const th = Math.min(this.tileSize, this.levels[level].height - ty * this.tileSize);
    const ix0 = Math.max(tx * this.tileSize, x0);
    const iy0 = Math.max(ty * this.tileSize, y0);
    const ix1 = Math.min(tx * this.tileSize + tw, x1);
    const iy1 = Math.min(ty * this.tileSize + th, y1);
    for (let y = iy0; y < iy1; y++) {
      const src = ((y - ty * this.tileSize) * tw + (ix0 - tx * this.tileSize)) * 4;
      const dst = ((y - y0) * outW + (ix0 - x0)) * 4;
      out.set(tile.subarray(src, src + (ix1 - ix0) * 4), dst);
    }
  }

  // ---------------------------------------------------------------- 任务调度

  private obtainTask(
    key: string,
    level: number,
    tx: number,
    ty: number,
    gen: number,
    fixed: boolean,
  ): InflightTask {
    if (fixed && this.lastTombstoneGen(key) > gen) {
      throw new SnapshotExpiredError(`tile ${key} has no data version for generation ${gen}`);
    }
    const existing = this.inflight.get(key);
    if (existing && !existing.aborted) {
      const lo = Math.min(existing.startGen, gen);
      const hi = Math.max(existing.startGen, gen);
      // 区间 (lo, hi] 内无墓碑：在飞任务的数据对请求代际同样有效，直接共享。
      if (!this.hasTombstoneIn(key, lo, hi)) return existing;
      // 否则在飞任务注定被丢弃（或其数据对请求者太旧），另起新任务。
    }
    const task: InflightTask = {
      key,
      startGen: gen,
      waiters: 0,
      aborted: false,
      settled: false,
      tempBytes: 0,
      promise: Promise.resolve(null),
    };
    task.promise = this.runTask(task, level, tx, ty);
    this.inflight.set(key, task);
    return task;
  }

  private detachWaiter(task: InflightTask): void {
    task.waiters--;
    if (task.waiters > 0 || task.settled) return;
    if (!task.aborted) {
      task.aborted = true;
      this.stats.cancels++;
      this.stats.releasedBytes += task.tempBytes;
      task.tempBytes = 0;
      if (this.inflight.get(task.key) === task) this.inflight.delete(task.key);
    }
  }

  private async runTask(task: InflightTask, level: number, tx: number, ty: number): Promise<Uint8Array | null> {
    const key = task.key;
    try {
      const data = await this.computeTile(task, level, tx, ty);
      if (data === null || task.aborted) return null;
      // 旧代际任务：计算期间该瓦片已被失效，丢弃结果，旧像素不得复活。
      if (this.lastTombstoneGen(key) > task.startGen) return null;
      this.publishVersion(key, task.startGen, data);
      this.stats.computes++;
      return data;
    } finally {
      task.settled = true;
      if (this.inflight.get(key) === task) this.inflight.delete(key);
      task.tempBytes = 0;
    }
  }

  /**
   * 计算一个瓦片：level 0 从基准图拷贝；更高层先取父层覆盖源区域的瓦片
   * （递归走同一套缓存/快照逻辑），拼成源区域后做 2x2 盒式降采样。
   * 边缘块按实际覆盖像素数加权，不补零。
   */
  private async computeTile(task: InflightTask, level: number, tx: number, ty: number): Promise<Uint8Array | null> {
    const lw = this.levels[level].width;
    const lh = this.levels[level].height;
    const x0 = tx * this.tileSize;
    const y0 = ty * this.tileSize;
    const tw = Math.min(this.tileSize, lw - x0);
    const th = Math.min(this.tileSize, lh - y0);
    const out = new Uint8Array(tw * th * 4);
    task.tempBytes += out.length;
    this.hooks.onCompute?.(task.key);
    await this.hooks.gate?.(task.key);
    if (task.aborted) return null;

    if (level === 0) {
      for (let y = 0; y < th; y++) {
        const src = ((y0 + y) * this.base.width + x0) * 4;
        out.set(this.base.data.subarray(src, src + tw * 4), y * tw * 4);
      }
      return out;
    }

    const pl = level - 1;
    const pw = this.levels[pl].width;
    const ph = this.levels[pl].height;
    const sx0 = x0 * 2;
    const sy0 = y0 * 2;
    const sx1 = Math.min(sx0 + tw * 2, pw);
    const sy1 = Math.min(sy0 + th * 2, ph);
    const rw = sx1 - sx0;
    const rh = sy1 - sy0;
    const region = new Uint8Array(rw * rh * 4);
    task.tempBytes += region.length;

    const ptx0 = Math.floor(sx0 / this.tileSize);
    const pty0 = Math.floor(sy0 / this.tileSize);
    const ptx1 = Math.floor((sx1 - 1) / this.tileSize);
    const pty1 = Math.floor((sy1 - 1) / this.tileSize);
    for (let pty = pty0; pty <= pty1; pty++) {
      for (let ptx = ptx0; ptx <= ptx1; ptx++) {
        if (task.aborted) return null;
        const pdata = await this.getTile(pl, ptx, pty, { snapshotGen: task.startGen });
        const px0 = ptx * this.tileSize;
        const py0 = pty * this.tileSize;
        const pw2 = Math.min(this.tileSize, pw - px0);
        const ph2 = Math.min(this.tileSize, ph - py0);
        const ix0 = Math.max(px0, sx0);
        const iy0 = Math.max(py0, sy0);
        const ix1 = Math.min(px0 + pw2, sx1);
        const iy1 = Math.min(py0 + ph2, sy1);
        for (let y = iy0; y < iy1; y++) {
          const src = ((y - py0) * pw2 + (ix0 - px0)) * 4;
          const dst = ((y - sy0) * rw + (ix0 - sx0)) * 4;
          region.set(pdata.subarray(src, src + (ix1 - ix0) * 4), dst);
        }
      }
    }

    for (let oy = 0; oy < th; oy++) {
      for (let ox = 0; ox < tw; ox++) {
        const rx0 = ox * 2;
        const ry0 = oy * 2;
        const rx1 = Math.min(rx0 + 2, rw);
        const ry1 = Math.min(ry0 + 2, rh);
        const count = (rx1 - rx0) * (ry1 - ry0);
        let r = 0;
        let g = 0;
        let b = 0;
        let a = 0;
        for (let y = ry0; y < ry1; y++) {
          for (let x = rx0; x < rx1; x++) {
            const i = (y * rw + x) * 4;
            r += region[i];
            g += region[i + 1];
            b += region[i + 2];
            a += region[i + 3];
          }
        }
        const o = (oy * tw + ox) * 4;
        out[o] = Math.round(r / count);
        out[o + 1] = Math.round(g / count);
        out[o + 2] = Math.round(b / count);
        out[o + 3] = Math.round(a / count);
      }
    }
    return out;
  }

  // ---------------------------------------------------------------- 版本与缓存

  /** 快照可见性查询：返回快照 gen 时刻有效的数据版本（不存在返回 null）。 */
  private lookupHit(key: string, gen: number): Version | null {
    const arr = this.versions.get(key);
    if (!arr || arr.length === 0) return null;
    let best: Version | null = null;
    let lastData: Version | null = null;
    let lastTomb = -1;
    for (const v of arr) {
      if (v.data === null) {
        lastTomb = v.gen;
        if (v.gen <= gen) best = null; // 快照时刻该数据已失效
      } else {
        lastData = v;
        if (v.gen <= gen) best = v;
      }
    }
    if (best) return best;
    // 快照之后才计算的版本：只要 (gen, v.gen] 之间无墓碑，数据与快照时刻一致
    if (lastData && lastData.gen > gen && (lastTomb <= gen || lastTomb > lastData.gen)) return lastData;
    return null;
  }

  private lastTombstoneGen(key: string): number {
    const arr = this.versions.get(key);
    if (!arr) return -1;
    for (let i = arr.length - 1; i >= 0; i--) {
      if (arr[i].data === null) return arr[i].gen;
    }
    return -1;
  }

  private hasTombstoneIn(key: string, loExclusive: number, hiInclusive: number): boolean {
    const arr = this.versions.get(key);
    if (!arr) return false;
    for (const v of arr) {
      if (v.data === null && v.gen > loExclusive && v.gen <= hiInclusive) return true;
    }
    return false;
  }

  private publishVersion(key: string, gen: number, data: Uint8Array): void {
    let arr = this.versions.get(key);
    if (!arr) {
      arr = [];
      this.versions.set(key, arr);
    }
    const version: Version = { gen, data, lastUsed: ++this.clock };
    let i = arr.length;
    while (i > 0 && arr[i - 1].gen > gen) i--;
    if (i < arr.length && arr[i].gen === gen && arr[i].data) {
      this.stats.currentBytes -= arr[i].data!.length;
      arr[i] = version;
    } else {
      arr.splice(i, 0, version);
    }
    this.stats.currentBytes += data.length;
    if (this.stats.currentBytes > this.stats.peakBytes) this.stats.peakBytes = this.stats.currentBytes;
    this.evictIfNeeded();
  }

  /** 活动快照钉住的版本：每个键上 <= 快照代际的最新版本。 */
  private computePinned(): Set<Version> {
    const pinned = new Set<Version>();
    if (this.activeSnapshots.size === 0) return pinned;
    const gens = [...this.activeSnapshots.keys()];
    for (const arr of this.versions.values()) {
      for (const g of gens) {
        let need: Version | null = null;
        for (const v of arr) {
          if (v.gen <= g) need = v;
          else break;
        }
        if (need && need.data) pinned.add(need);
      }
    }
    return pinned;
  }

  private evictIfNeeded(): void {
    if (this.stats.currentBytes <= this.maxCacheBytes) return;
    const pinned = this.computePinned();
    const candidates: { key: string; v: Version }[] = [];
    for (const [key, arr] of this.versions) {
      for (const v of arr) {
        if (v.data && !pinned.has(v)) candidates.push({ key, v });
      }
    }
    candidates.sort((a, b) => a.v.lastUsed - b.v.lastUsed);
    for (const { key, v } of candidates) {
      if (this.stats.currentBytes <= this.maxCacheBytes) break;
      const arr = this.versions.get(key)!;
      const idx = arr.indexOf(v);
      if (idx >= 0) arr.splice(idx, 1);
      if (arr.length === 0) this.versions.delete(key);
      this.stats.currentBytes -= v.data!.length;
      this.stats.evictions++;
    }
  }

  // ---------------------------------------------------------------- 调试/统计

  statsSnapshot(): PyramidStats {
    return { ...this.stats };
  }

  /** 调试：某瓦片的所有版本代际（含墓碑） */
  debugVersionGens(key: string): number[] {
    return (this.versions.get(key) ?? []).map((v) => v.gen);
  }

  /** 调试：当前缓存中实际持有数据的瓦片键 */
  debugCachedKeys(): string[] {
    const keys: string[] = [];
    for (const [key, arr] of this.versions) {
      if (arr.some((v) => v.data !== null)) keys.push(key);
    }
    return keys.sort();
  }
}
