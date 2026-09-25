import { TileStore } from './store.ts';
import {
  createImage,
  extractTile,
  blitTile,
  type RgbaImage,
  type Tile,
} from './image.ts';

interface DocMeta {
  width: number;
  height: number;
  tileSize: number;
}

const META_KEY = 'document';

export class Document {
  readonly store: TileStore;
  readonly width: number;
  readonly height: number;
  readonly tileSize: number;

  private constructor(store: TileStore, width: number, height: number, tileSize: number) {
    this.store = store;
    this.width = width;
    this.height = height;
    this.tileSize = tileSize;
  }

  static create(store: TileStore, width: number, height: number, tileSize = 8): Document {
    const meta: DocMeta = { width, height, tileSize };
    store.setMeta(META_KEY, meta);
    return new Document(store, width, height, tileSize);
  }

  static open(store: TileStore): Document {
    const meta = store.getMeta<DocMeta>(META_KEY);
    if (!meta) throw new Error('no document metadata in store');
    return new Document(store, meta.width, meta.height, meta.tileSize);
  }

  get tilesX(): number {
    return Math.ceil(this.width / this.tileSize);
  }

  get tilesY(): number {
    return Math.ceil(this.height / this.tileSize);
  }

  private tileCount(): number {
    return this.tilesX * this.tilesY;
  }

  createLayer(layerId: string): void {
    const blank = createImage(this.tileSize, this.tileSize);
    const blankTile: Tile = { width: this.tileSize, height: this.tileSize, pixels: blank.pixels };
    const blankId = this.store.putTile(blankTile);
    const tiles: string[] = [];
    for (let ty = 0; ty < this.tilesY; ty++) {
      for (let tx = 0; tx < this.tilesX; tx++) {
        const w = Math.min(this.tileSize, this.width - tx * this.tileSize);
        const h = Math.min(this.tileSize, this.height - ty * this.tileSize);
        if (w === this.tileSize && h === this.tileSize) {
          tiles.push(blankId);
        } else {
          tiles.push(this.store.putTile({ width: w, height: h, pixels: new Uint8Array(w * h * 4) }));
        }
      }
    }
    this.store.commit((tx) => tx.publishRoot(layerId, 'layer', tiles));
  }

  private layerTiles(layerId: string): string[] {
    const root = this.store.getRoot(layerId);
    if (!root || root.kind !== 'layer') throw new Error(`unknown layer: ${layerId}`);
    return root.tiles;
  }

  editTile(layerId: string, tileX: number, tileY: number, tile: Tile): void {
    const tiles = this.layerTiles(layerId);
    const index = tileY * this.tilesX + tileX;
    const id = this.store.putTile(tile);
    const next = [...tiles];
    next[index] = id;
    this.store.commit((tx) => tx.publishRoot(layerId, 'layer', next));
  }

  cloneLayer(srcLayerId: string, dstLayerId: string): void {
    const tiles = this.layerTiles(srcLayerId);
    this.store.commit((tx) => tx.publishRoot(dstLayerId, 'layer', tiles));
  }

  snapshot(layerId: string, snapshotId: string): void {
    const tiles = this.layerTiles(layerId);
    this.store.commit((tx) => tx.publishRoot(snapshotId, 'snapshot', tiles));
  }

  deleteSnapshot(snapshotId: string): void {
    this.store.commit((tx) => tx.removeRoot(snapshotId));
  }

  deleteLayer(layerId: string): void {
    this.store.commit((tx) => tx.removeRoot(layerId));
  }

  private renderTiles(tiles: string[]): RgbaImage {
    if (tiles.length !== this.tileCount()) {
      throw new Error(`root has ${tiles.length} tiles, expected ${this.tileCount()}`);
    }
    const img = createImage(this.width, this.height);
    for (let ty = 0; ty < this.tilesY; ty++) {
      for (let tx = 0; tx < this.tilesX; tx++) {
        const id = tiles[ty * this.tilesX + tx];
        blitTile(img, this.store.getTile(id), tx, ty, this.tileSize);
      }
    }
    return img;
  }

  renderLayer(layerId: string): RgbaImage {
    return this.renderTiles(this.layerTiles(layerId));
  }

  renderSnapshot(snapshotId: string): RgbaImage {
    const root = this.store.getRoot(snapshotId);
    if (!root || root.kind !== 'snapshot') throw new Error(`unknown snapshot: ${snapshotId}`);
    return this.renderTiles(root.tiles);
  }

  snapshotTiles(snapshotId: string): string[] {
    const root = this.store.getRoot(snapshotId);
    if (!root || root.kind !== 'snapshot') throw new Error(`unknown snapshot: ${snapshotId}`);
    return root.tiles;
  }
}
