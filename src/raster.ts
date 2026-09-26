import { CHANNELS } from './types.ts';
import type { Region } from './types.ts';
import type { TileStore } from './store.ts';

export interface PixelSource {
  readonly width: number;
  readonly height: number;
  readRegion(x0: number, y0: number, w: number, h: number): Float32Array;
}

export class ArraySource implements PixelSource {
  readonly data: Float32Array;
  readonly width: number;
  readonly height: number;

  constructor(data: Float32Array, width: number, height: number) {
    if (data.length !== width * height * CHANNELS) {
      throw new Error(`像素数据长度 ${data.length} 与尺寸 ${width}x${height}x${CHANNELS} 不符`);
    }
    this.data = data;
    this.width = width;
    this.height = height;
  }

  readRegion(x0: number, y0: number, w: number, h: number): Float32Array {
    const out = new Float32Array(w * h * CHANNELS);
    for (let y = 0; y < h; y++) {
      const srcOff = ((y0 + y) * this.width + x0) * CHANNELS;
      out.set(this.data.subarray(srcOff, srcOff + w * CHANNELS), y * w * CHANNELS);
    }
    return out;
  }
}

// 以 tile 为单位存放在 TileStore 中的栅格; scope 隔离不同栅格的 key
export class Raster implements PixelSource {
  readonly scope: string;
  readonly store: TileStore;
  readonly width: number;
  readonly height: number;
  readonly tileSize: number;
  readonly tilesX: number;
  readonly tilesY: number;

  constructor(scope: string, store: TileStore, width: number, height: number, tileSize: number) {
    this.scope = scope;
    this.store = store;
    this.width = width;
    this.height = height;
    this.tileSize = tileSize;
    this.tilesX = Math.ceil(width / tileSize);
    this.tilesY = Math.ceil(height / tileSize);
  }

  key(tx: number, ty: number): string {
    return `${this.scope}:${tx},${ty}`;
  }

  tileRegion(tx: number, ty: number): Region {
    const x0 = tx * this.tileSize;
    const y0 = ty * this.tileSize;
    return {
      x0,
      y0,
      w: Math.min(this.tileSize, this.width - x0),
      h: Math.min(this.tileSize, this.height - y0),
    };
  }

  writeTile(tx: number, ty: number, data: Float32Array): void {
    this.store.put(this.key(tx, ty), data);
  }

  writeRegion(x0: number, y0: number, w: number, h: number, data: Float32Array): void {
    const tx0 = Math.floor(x0 / this.tileSize);
    const ty0 = Math.floor(y0 / this.tileSize);
    const tx1 = Math.floor((x0 + w - 1) / this.tileSize);
    const ty1 = Math.floor((y0 + h - 1) / this.tileSize);
    for (let ty = ty0; ty <= ty1; ty++) {
      for (let tx = tx0; tx <= tx1; tx++) {
        const region = this.tileRegion(tx, ty);
        const ix0 = Math.max(x0, region.x0);
        const iy0 = Math.max(y0, region.y0);
        const ix1 = Math.min(x0 + w, region.x0 + region.w);
        const iy1 = Math.min(y0 + h, region.y0 + region.h);
        if (ix0 >= ix1 || iy0 >= iy1) continue;
        let tile = this.store.get(this.key(tx, ty));
        if (!tile) tile = new Float32Array(this.store.tileBytes / 4);
        for (let y = iy0; y < iy1; y++) {
          const srcOff = ((y - y0) * w + (ix0 - x0)) * CHANNELS;
          const dstOff = ((y - region.y0) * this.tileSize + (ix0 - region.x0)) * CHANNELS;
          tile.set(data.subarray(srcOff, srcOff + (ix1 - ix0) * CHANNELS), dstOff);
        }
        this.store.put(this.key(tx, ty), tile);
      }
    }
  }

  readRegion(x0: number, y0: number, w: number, h: number): Float32Array {
    const out = new Float32Array(w * h * CHANNELS);
    const tx0 = Math.floor(x0 / this.tileSize);
    const ty0 = Math.floor(y0 / this.tileSize);
    const tx1 = Math.floor((x0 + w - 1) / this.tileSize);
    const ty1 = Math.floor((y0 + h - 1) / this.tileSize);
    for (let ty = ty0; ty <= ty1; ty++) {
      for (let tx = tx0; tx <= tx1; tx++) {
        const tile = this.store.get(this.key(tx, ty));
        if (!tile) throw new Error(`tile (${tx},${ty}) @${this.scope} 缺失`);
        const region = this.tileRegion(tx, ty);
        const ix0 = Math.max(x0, region.x0);
        const iy0 = Math.max(y0, region.y0);
        const ix1 = Math.min(x0 + w, region.x0 + region.w);
        const iy1 = Math.min(y0 + h, region.y0 + region.h);
        for (let y = iy0; y < iy1; y++) {
          const srcOff = ((y - region.y0) * this.tileSize + (ix0 - region.x0)) * CHANNELS;
          const dstOff = ((y - y0) * w + (ix0 - x0)) * CHANNELS;
          out.set(tile.subarray(srcOff, srcOff + (ix1 - ix0) * CHANNELS), dstOff);
        }
      }
    }
    return out;
  }

  dispose(): void {
    for (let ty = 0; ty < this.tilesY; ty++) {
      for (let tx = 0; tx < this.tilesX; tx++) {
        this.store.delete(this.key(tx, ty));
      }
    }
  }
}
