import * as fs from 'node:fs';
import * as path from 'node:path';
import { createHash } from 'node:crypto';
import { bytesEqual, type Tile } from './image.ts';

export type HashFn = (data: Uint8Array) => string;

export const sha256Hash: HashFn = (data) =>
  createHash('sha256').update(data).digest('hex').slice(0, 32);

export interface BlockEntry {
  id: string;
  digest: string;
  width: number;
  height: number;
  bytes: number;
  createdInGeneration: number;
  file: string;
}

export interface RootEntry {
  kind: 'layer' | 'snapshot';
  tiles: string[];
}

export interface LeaseEntry {
  tiles: string[];
}

interface Manifest {
  formatVersion: 1;
  generation: number;
  meta: Record<string, unknown>;
  blocks: Record<string, BlockEntry>;
  roots: Record<string, RootEntry>;
  leases: Record<string, LeaseEntry>;
  ledger: Record<string, number>;
}

export interface GcReport {
  generation: number;
  reclaimed: string[];
  sweptOrphanFiles: string[];
  liveBlocks: number;
}

export interface StoreStats {
  generation: number;
  blocks: number;
  roots: number;
  leases: number;
  ledgerRefs: number;
}

export interface Tx {
  publishRoot(rootId: string, kind: 'layer' | 'snapshot', tiles: string[]): void;
  removeRoot(rootId: string): void;
  acquireLease(leaseId: string, tiles: string[]): void;
  releaseLease(leaseId: string): void;
}

function emptyManifest(): Manifest {
  return {
    formatVersion: 1,
    generation: 0,
    meta: {},
    blocks: {},
    roots: {},
    leases: {},
    ledger: {},
  };
}

export class TileStore {
  readonly dir: string;
  readonly hashFn: HashFn;
  private manifest: Manifest;
  private digestIndex: Map<string, Set<string>>;

  private constructor(dir: string, hashFn: HashFn, manifest: Manifest) {
    this.dir = dir;
    this.hashFn = hashFn;
    this.manifest = manifest;
    this.digestIndex = new Map();
    for (const entry of Object.values(manifest.blocks)) {
      let bucket = this.digestIndex.get(entry.digest);
      if (!bucket) {
        bucket = new Set();
        this.digestIndex.set(entry.digest, bucket);
      }
      bucket.add(entry.id);
    }
  }

  static open(dir: string, hashFn: HashFn = sha256Hash): TileStore {
    fs.mkdirSync(path.join(dir, 'blocks'), { recursive: true });
    const manifestPath = path.join(dir, 'manifest.json');
    let manifest: Manifest;
    if (fs.existsSync(manifestPath)) {
      manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8')) as Manifest;
    } else {
      manifest = emptyManifest();
    }
    const store = new TileStore(dir, hashFn, manifest);
    store.persist();
    return store;
  }

  private persist(): void {
    const manifestPath = path.join(this.dir, 'manifest.json');
    const tmpPath = manifestPath + '.tmp';
    fs.writeFileSync(tmpPath, JSON.stringify(this.manifest, null, 2));
    fs.renameSync(tmpPath, manifestPath);
  }

  private blockPath(entry: BlockEntry): string {
    return path.join(this.dir, entry.file);
  }

  putTile(tile: Tile): string {
    const digest = this.hashFn(tile.pixels);
    const bucket = this.digestIndex.get(digest);
    if (bucket) {
      for (const id of bucket) {
        const entry = this.manifest.blocks[id];
        const existing = this.readBlockBytes(entry);
        if (
          entry.width === tile.width &&
          entry.height === tile.height &&
          bytesEqual(existing, tile.pixels)
        ) {
          return id;
        }
      }
    }
    let id = digest;
    if (this.manifest.blocks[id]) {
      let n = 1;
      while (this.manifest.blocks[`${digest}#${n}`]) n++;
      id = `${digest}#${n}`;
    }
    const file = `blocks/${id}.rgba`;
    const fullPath = path.join(this.dir, file);
    const tmpPath = fullPath + '.tmp';
    fs.writeFileSync(tmpPath, tile.pixels);
    fs.renameSync(tmpPath, fullPath);
    const entry: BlockEntry = {
      id,
      digest,
      width: tile.width,
      height: tile.height,
      bytes: tile.pixels.length,
      createdInGeneration: this.manifest.generation,
      file,
    };
    this.manifest.blocks[id] = entry;
    let bucketSet = this.digestIndex.get(digest);
    if (!bucketSet) {
      bucketSet = new Set();
      this.digestIndex.set(digest, bucketSet);
    }
    bucketSet.add(id);
    this.persist();
    return id;
  }

  private readBlockBytes(entry: BlockEntry): Uint8Array {
    return new Uint8Array(fs.readFileSync(this.blockPath(entry)));
  }

  getTile(id: string): Tile {
    const entry = this.manifest.blocks[id];
    if (!entry) throw new Error(`block not found: ${id}`);
    return { width: entry.width, height: entry.height, pixels: this.readBlockBytes(entry) };
  }

  hasBlock(id: string): boolean {
    return id in this.manifest.blocks;
  }

  blockFileExists(id: string): boolean {
    const entry = this.manifest.blocks[id];
    if (!entry) return false;
    return fs.existsSync(this.blockPath(entry));
  }

  getRoot(rootId: string): RootEntry | undefined {
    const root = this.manifest.roots[rootId];
    return root ? { kind: root.kind, tiles: [...root.tiles] } : undefined;
  }

  listRoots(): string[] {
    return Object.keys(this.manifest.roots);
  }

  listBlocks(): string[] {
    return Object.keys(this.manifest.blocks);
  }

  ledgerRef(blockId: string): number {
    return this.manifest.ledger[blockId] ?? 0;
  }

  get generation(): number {
    return this.manifest.generation;
  }

  getMeta<T>(key: string): T | undefined {
    return this.manifest.meta[key] as T | undefined;
  }

  setMeta(key: string, value: unknown): void {
    this.manifest.meta[key] = value;
    this.persist();
  }

  commit(mutate: (tx: Tx) => void): void {
    const nextRoots: Record<string, RootEntry> = JSON.parse(JSON.stringify(this.manifest.roots));
    const nextLeases: Record<string, LeaseEntry> = JSON.parse(JSON.stringify(this.manifest.leases));
    const store = this;
    const tx: Tx = {
      publishRoot(rootId, kind, tiles) {
        for (const id of tiles) {
          if (!store.manifest.blocks[id]) {
            throw new Error(`cannot publish root ${rootId}: unknown block ${id}`);
          }
        }
        nextRoots[rootId] = { kind, tiles: [...tiles] };
      },
      removeRoot(rootId) {
        delete nextRoots[rootId];
      },
      acquireLease(leaseId, tiles) {
        for (const id of tiles) {
          if (!store.manifest.blocks[id]) {
            throw new Error(`cannot lease unknown block ${id}`);
          }
        }
        nextLeases[leaseId] = { tiles: [...tiles] };
      },
      releaseLease(leaseId) {
        delete nextLeases[leaseId];
      },
    };
    mutate(tx);
    const ledger: Record<string, number> = {};
    const bump = (id: string, delta: number) => {
      ledger[id] = (ledger[id] ?? 0) + delta;
    };
    for (const root of Object.values(nextRoots)) {
      for (const id of root.tiles) bump(id, 1);
    }
    for (const lease of Object.values(nextLeases)) {
      for (const id of lease.tiles) bump(id, 1);
    }
    for (const id of Object.keys(ledger)) {
      if (ledger[id] === 0) delete ledger[id];
    }
    this.manifest.roots = nextRoots;
    this.manifest.leases = nextLeases;
    this.manifest.ledger = ledger;
    this.persist();
  }

  acquireLease(leaseId: string, rootId: string): void {
    const root = this.manifest.roots[rootId];
    if (!root) throw new Error(`cannot lease unknown root: ${rootId}`);
    this.commit((tx) => tx.acquireLease(leaseId, root.tiles));
  }

  releaseLease(leaseId: string): void {
    this.commit((tx) => tx.releaseLease(leaseId));
  }

  hasLease(leaseId: string): boolean {
    return leaseId in this.manifest.leases;
  }

  private traverseLive(): Set<string> {
    const live = new Set<string>();
    for (const root of Object.values(this.manifest.roots)) {
      for (const id of root.tiles) live.add(id);
    }
    for (const lease of Object.values(this.manifest.leases)) {
      for (const id of lease.tiles) live.add(id);
    }
    return live;
  }

  gc(): GcReport {
    this.manifest.generation += 1;
    const generation = this.manifest.generation;
    const live = this.traverseLive();
    const reclaimed: string[] = [];
    for (const entry of Object.values(this.manifest.blocks)) {
      if (entry.createdInGeneration >= generation - 1) continue;
      if ((this.manifest.ledger[entry.id] ?? 0) > 0) continue;
      if (live.has(entry.id)) continue;
      reclaimed.push(entry.id);
    }
    const reclaimedFiles: string[] = [];
    for (const id of reclaimed) {
      const entry = this.manifest.blocks[id];
      reclaimedFiles.push(this.blockPath(entry));
      delete this.manifest.blocks[id];
      delete this.manifest.ledger[id];
      const bucket = this.digestIndex.get(entry.digest);
      if (bucket) {
        bucket.delete(id);
        if (bucket.size === 0) this.digestIndex.delete(entry.digest);
      }
    }
    this.persist();
    for (const file of reclaimedFiles) {
      if (fs.existsSync(file)) fs.unlinkSync(file);
    }
    const sweptOrphanFiles: string[] = [];
    const blocksDir = path.join(this.dir, 'blocks');
    const knownFiles = new Set(
      Object.values(this.manifest.blocks).map((e) => path.basename(e.file)),
    );
    for (const name of fs.readdirSync(blocksDir)) {
      if (!knownFiles.has(name)) {
        fs.unlinkSync(path.join(blocksDir, name));
        sweptOrphanFiles.push(name);
      }
    }
    return {
      generation,
      reclaimed,
      sweptOrphanFiles,
      liveBlocks: Object.keys(this.manifest.blocks).length,
    };
  }

  stats(): StoreStats {
    return {
      generation: this.manifest.generation,
      blocks: Object.keys(this.manifest.blocks).length,
      roots: Object.keys(this.manifest.roots).length,
      leases: Object.keys(this.manifest.leases).length,
      ledgerRefs: Object.values(this.manifest.ledger).reduce((a, b) => a + b, 0),
    };
  }

  checkInvariant(): string[] {
    const problems: string[] = [];
    for (const [rootId, root] of Object.entries(this.manifest.roots)) {
      for (const id of root.tiles) {
        if (!this.manifest.blocks[id]) {
          problems.push(`root ${rootId} references missing block ${id}`);
        } else if (!this.blockFileExists(id)) {
          problems.push(`root ${rootId} references block ${id} whose file is gone`);
        }
      }
    }
    for (const [leaseId, lease] of Object.entries(this.manifest.leases)) {
      for (const id of lease.tiles) {
        if (!this.manifest.blocks[id]) {
          problems.push(`lease ${leaseId} references missing block ${id}`);
        }
      }
    }
    return problems;
  }
}
