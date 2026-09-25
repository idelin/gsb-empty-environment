import type { BlockStore } from "./store.ts";
import { blankPixels, CHANNELS } from "./pixels.ts";

export function imageRoot(name: string): string {
  return "image:" + name;
}

export function snapshotRoot(name: string): string {
  return "snap:" + name;
}

export class TiledImage {
  readonly store: BlockStore;
  readonly name: string;
  readonly width: number;
  readonly height: number;
  readonly tileSize: number;
  private grid: string[];

  private constructor(
    store: BlockStore,
    name: string,
    width: number,
    height: number,
    tileSize: number,
    grid: string[],
  ) {
    this.store = store;
    this.name = name;
    this.width = width;
    this.height = height;
    this.tileSize = tileSize;
    this.grid = grid;
  }

  static async createBlank(
    store: BlockStore,
    name: string,
    width: number,
    height: number,
    tileSize: number,
  ): Promise<TiledImage> {
    if (width % tileSize !== 0 || height % tileSize !== 0) {
      throw new Error("image dimensions must be multiples of tileSize");
    }
    const blankId = await store.intern(blankPixels(tileSize));
    const cols = width / tileSize;
    const rows = height / tileSize;
    const grid = new Array<string>(cols * rows).fill(blankId);
    await store.registerRoot(imageRoot(name), grid);
    return new TiledImage(store, name, width, height, tileSize, grid);
  }

  static async attach(
    store: BlockStore,
    name: string,
    width: number,
    height: number,
    tileSize: number,
  ): Promise<TiledImage> {
    const grid = store.rootTiles(imageRoot(name));
    return new TiledImage(store, name, width, height, tileSize, grid);
  }

  get cols(): number {
    return this.width / this.tileSize;
  }

  get rows(): number {
    return this.height / this.tileSize;
  }

  tileId(tx: number, ty: number): string {
    return this.grid[ty * this.cols + tx];
  }

  tileIds(): string[] {
    return this.grid.slice();
  }

  async setTile(tx: number, ty: number, pixels: Uint8Array): Promise<string> {
    if (pixels.length !== this.tileSize * this.tileSize * CHANNELS) {
      throw new Error("tile pixel buffer has wrong size");
    }
    const id = await this.store.intern(pixels);
    const next = this.grid.slice();
    next[ty * this.cols + tx] = id;
    await this.store.rebind(imageRoot(this.name), next);
    this.grid = next;
    return id;
  }

  async getTile(tx: number, ty: number): Promise<Uint8Array> {
    return this.store.read(this.tileId(tx, ty));
  }

  async snapshot(name: string): Promise<void> {
    await this.store.registerRoot(snapshotRoot(name), this.grid);
  }

  async render(): Promise<Uint8Array> {
    const out = new Uint8Array(this.width * this.height * CHANNELS);
    for (let ty = 0; ty < this.rows; ty++) {
      for (let tx = 0; tx < this.cols; tx++) {
        const tile = this.store.read(this.tileId(tx, ty));
        for (let y = 0; y < this.tileSize; y++) {
          const srcOff = y * this.tileSize * CHANNELS;
          const dstOff =
            ((ty * this.tileSize + y) * this.width + tx * this.tileSize) * CHANNELS;
          out.set(tile.subarray(srcOff, srcOff + this.tileSize * CHANNELS), dstOff);
        }
      }
    }
    return out;
  }
}

export async function deleteSnapshot(store: BlockStore, name: string): Promise<void> {
  await store.removeRoot(snapshotRoot(name));
}

export function snapshotTiles(store: BlockStore, name: string): string[] {
  return store.rootTiles(snapshotRoot(name));
}
