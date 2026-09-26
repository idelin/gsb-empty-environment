import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { Stats } from './stats.ts';

// 可溢写 tile 缓存: 内存占用 = 缓存 +  scratch 预留, 超出预算时 LRU 溢写到临时文件
export class TileStore {
  readonly dir: string;
  readonly budgetBytes: number;
  readonly tileBytes: number;
  readonly stats: Stats;
  private cache = new Map<string, { data: Float32Array; tick: number }>();
  private spilled = new Map<string, string>();
  private tick = 0;
  private scratchReserve = 0;

  constructor(dir: string, budgetBytes: number, tileBytes: number, stats: Stats) {
    this.dir = dir;
    this.budgetBytes = budgetBytes;
    this.tileBytes = tileBytes;
    this.stats = stats;
    mkdirSync(dir, { recursive: true });
  }

  setScratchReserve(bytes: number): void {
    this.scratchReserve = Math.max(0, bytes);
  }

  private cacheLimit(): number {
    return Math.max(0, this.budgetBytes - this.scratchReserve);
  }

  has(key: string): boolean {
    return this.cache.has(key) || this.spilled.has(key);
  }

  get(key: string): Float32Array | undefined {
    const hit = this.cache.get(key);
    if (hit) {
      hit.tick = ++this.tick;
      return hit.data;
    }
    const path = this.spilled.get(key);
    if (path && existsSync(path)) {
      const buf = readFileSync(path);
      const copy = new Uint8Array(buf.byteLength);
      copy.set(buf);
      const data = new Float32Array(copy.buffer);
      this.spilled.delete(key);
      rmSync(path, { force: true });
      this.stats.loadCount += 1;
      this.insert(key, data);
      return data;
    }
    return undefined;
  }

  put(key: string, data: Float32Array): void {
    if (data.byteLength !== this.tileBytes) {
      throw new Error(`tile 字节数不符: 期望 ${this.tileBytes}, 实际 ${data.byteLength}`);
    }
    this.insert(key, data);
  }

  delete(key: string): void {
    if (this.cache.delete(key)) {
      this.stats.cacheBytes -= this.tileBytes;
    }
    const path = this.spilled.get(key);
    if (path) {
      this.spilled.delete(key);
      rmSync(path, { force: true });
    }
  }

  private insert(key: string, data: Float32Array): void {
    if (this.cache.has(key)) this.stats.cacheBytes -= this.tileBytes;
    this.cache.set(key, { data, tick: ++this.tick });
    this.stats.cacheBytes += this.tileBytes;
    this.evict();
    this.stats.trackPeak();
  }

  private evict(): void {
    while (this.stats.cacheBytes > this.cacheLimit() && this.cache.size > 0) {
      let oldestKey: string | undefined;
      let oldestTick = Number.POSITIVE_INFINITY;
      for (const [k, e] of this.cache) {
        if (e.tick < oldestTick) {
          oldestTick = e.tick;
          oldestKey = k;
        }
      }
      if (oldestKey === undefined) break;
      const entry = this.cache.get(oldestKey);
      this.cache.delete(oldestKey);
      this.stats.cacheBytes -= this.tileBytes;
      if (entry) {
        const path = join(this.dir, `tile-${oldestKey.replace(/[^0-9a-zA-Z_-]/g, '_')}.f32`);
        writeFileSync(path, Buffer.from(entry.data.buffer, entry.data.byteOffset, this.tileBytes));
        this.spilled.set(oldestKey, path);
        this.stats.spillCount += 1;
        this.stats.spillBytes += this.tileBytes;
      }
    }
  }

  reserveScratch(bytes: number): void {
    this.stats.scratchBytes += bytes;
    this.stats.trackPeak();
  }

  releaseScratch(bytes: number): void {
    this.stats.scratchBytes = Math.max(0, this.stats.scratchBytes - bytes);
  }

  // 取消/失败/结束: 释放缓存并删除全部临时溢写文件
  releaseAll(): void {
    this.cache.clear();
    this.stats.cacheBytes = 0;
    for (const path of this.spilled.values()) rmSync(path, { force: true });
    this.spilled.clear();
  }

  dispose(): void {
    this.releaseAll();
    rmSync(this.dir, { recursive: true, force: true });
  }

  get cachedTiles(): number {
    return this.cache.size;
  }

  get spilledTiles(): number {
    return this.spilled.size;
  }
}
