import { promises as fs } from "node:fs";
import path from "node:path";
import { fnv1aDigest, type DigestFn } from "./hash.ts";
import { pixelsEqual } from "./pixels.ts";

export interface Lease {
  readonly blockId: string;
  release(): void;
}

interface BlockRec {
  id: string;
  digest: string;
  pixels: Uint8Array;
  refs: number;
  gen: number;
  orphanMark: number | null;
}

interface ManifestBlock {
  digest: string;
  refs: number;
  gen: number;
  orphanMark: number | null;
  pixels: string;
}

interface Manifest {
  version: 1;
  epoch: number;
  nextNum: number;
  blocks: Record<string, ManifestBlock>;
  roots: Record<string, string[]>;
}

export interface VerifyReport {
  ok: boolean;
  errors: string[];
  liveBlocks: number;
  unreachableBlocks: number;
  leasedBlocks: number;
}

export interface GcResult {
  epoch: number;
  marked: string[];
  reclaimed: string[];
}

export interface StoreOptions {
  digest?: DigestFn;
  durability?: "fsync" | "atomic-rename";
}

const MANIFEST_FILE = "manifest.json";

export class BlockStore {
  private blocks = new Map<string, BlockRec>();
  private buckets = new Map<string, Set<string>>();
  private roots = new Map<string, string[]>();
  private leases = new Map<string, number>();
  private epoch = 0;
  private nextNum = 0;
  private readonly dir: string;
  private readonly digestFn: DigestFn;
  private readonly fsync: boolean;

  private constructor(dir: string, digestFn: DigestFn, fsync: boolean) {
    this.dir = dir;
    this.digestFn = digestFn;
    this.fsync = fsync;
  }

  static async open(dir: string, opts: StoreOptions = {}): Promise<BlockStore> {
    const store = new BlockStore(
      dir,
      opts.digest ?? fnv1aDigest,
      (opts.durability ?? "fsync") === "fsync",
    );
    await fs.mkdir(dir, { recursive: true });
    const file = path.join(dir, MANIFEST_FILE);
    let raw: string | null = null;
    try {
      raw = await fs.readFile(file, "utf8");
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== "ENOENT") throw err;
    }
    if (raw !== null) {
      const m = JSON.parse(raw) as Manifest;
      store.epoch = m.epoch;
      store.nextNum = m.nextNum;
      for (const [id, b] of Object.entries(m.blocks)) {
        const rec: BlockRec = {
          id,
          digest: b.digest,
          refs: b.refs,
          gen: b.gen,
          orphanMark: b.orphanMark,
          pixels: new Uint8Array(Buffer.from(b.pixels, "base64")),
        };
        store.blocks.set(id, rec);
        let bucket = store.buckets.get(b.digest);
        if (!bucket) {
          bucket = new Set();
          store.buckets.set(b.digest, bucket);
        }
        bucket.add(id);
      }
      for (const [name, tiles] of Object.entries(m.roots)) {
        store.roots.set(name, tiles.slice());
      }
    }
    return store;
  }

  private async persist(): Promise<void> {
    const blocks: Record<string, ManifestBlock> = {};
    for (const [id, b] of this.blocks) {
      blocks[id] = {
        digest: b.digest,
        refs: b.refs,
        gen: b.gen,
        orphanMark: b.orphanMark,
        pixels: Buffer.from(b.pixels).toString("base64"),
      };
    }
    const roots: Record<string, string[]> = {};
    for (const [name, tiles] of this.roots) roots[name] = tiles.slice();
    const manifest: Manifest = {
      version: 1,
      epoch: this.epoch,
      nextNum: this.nextNum,
      blocks,
      roots,
    };
    const finalPath = path.join(this.dir, MANIFEST_FILE);
    const tmpPath = finalPath + ".tmp";
    const fh = await fs.open(tmpPath, "w");
    try {
      await fh.writeFile(JSON.stringify(manifest));
      if (this.fsync) await fh.sync();
    } finally {
      await fh.close();
    }
    await fs.rename(tmpPath, finalPath);
    if (!this.fsync) return;
    const dh = await fs.open(this.dir, "r");
    try {
      await dh.sync();
    } finally {
      await dh.close();
    }
  }

  async intern(pixels: Uint8Array): Promise<string> {
    const digest = this.digestFn(pixels);
    const bucket = this.buckets.get(digest);
    if (bucket) {
      for (const id of bucket) {
        const rec = this.blocks.get(id)!;
        if (pixelsEqual(rec.pixels, pixels)) return id;
      }
    }
    const id = "blk" + this.nextNum++;
    const rec: BlockRec = {
      id,
      digest,
      pixels: pixels.slice(),
      refs: 0,
      gen: this.epoch,
      orphanMark: null,
    };
    this.blocks.set(id, rec);
    let b = bucket;
    if (!b) {
      b = new Set();
      this.buckets.set(digest, b);
    }
    b.add(id);
    await this.persist();
    return id;
  }

  read(id: string): Uint8Array {
    const rec = this.blocks.get(id);
    if (!rec) throw new Error("unknown block: " + id);
    return rec.pixels.slice();
  }

  has(id: string): boolean {
    return this.blocks.has(id);
  }

  digestOf(id: string): string {
    const rec = this.blocks.get(id);
    if (!rec) throw new Error("unknown block: " + id);
    return rec.digest;
  }

  refsOf(id: string): number {
    const rec = this.blocks.get(id);
    if (!rec) throw new Error("unknown block: " + id);
    return rec.refs;
  }

  lease(id: string): Lease {
    if (!this.blocks.has(id)) throw new Error("cannot lease unknown block: " + id);
    this.leases.set(id, (this.leases.get(id) ?? 0) + 1);
    const store = this;
    let active = true;
    return {
      blockId: id,
      release(): void {
        if (!active) throw new Error("lease already released: " + id);
        active = false;
        const n = store.leases.get(id)! - 1;
        if (n === 0) store.leases.delete(id);
        else store.leases.set(id, n);
      },
    };
  }

  isLeased(id: string): boolean {
    return this.leases.has(id);
  }

  private rebindRoot(name: string, tiles: string[]): void {
    const delta = new Map<string, number>();
    for (const id of tiles) delta.set(id, (delta.get(id) ?? 0) + 1);
    const old = this.roots.get(name);
    if (old) {
      for (const id of old) delta.set(id, (delta.get(id) ?? 0) - 1);
    }
    for (const [id, d] of delta) {
      if (d === 0) continue;
      const rec = this.blocks.get(id);
      if (!rec) throw new Error("root references unknown block: " + id);
      rec.refs += d;
      if (rec.refs < 0) throw new Error("refcount underflow on " + id);
    }
    this.roots.set(name, tiles.slice());
  }

  async registerRoot(name: string, tiles: string[]): Promise<void> {
    if (this.roots.has(name)) throw new Error("root already exists: " + name);
    this.rebindRoot(name, tiles);
    await this.persist();
  }

  async rebind(name: string, tiles: string[]): Promise<void> {
    if (!this.roots.has(name)) throw new Error("unknown root: " + name);
    this.rebindRoot(name, tiles);
    await this.persist();
  }

  async removeRoot(name: string): Promise<void> {
    if (!this.roots.has(name)) throw new Error("unknown root: " + name);
    this.rebindRoot(name, []);
    this.roots.delete(name);
    await this.persist();
  }

  rootTiles(name: string): string[] {
    const tiles = this.roots.get(name);
    if (!tiles) throw new Error("unknown root: " + name);
    return tiles.slice();
  }

  rootNames(): string[] {
    return [...this.roots.keys()];
  }

  private liveSet(): Map<string, number> {
    const counts = new Map<string, number>();
    for (const tiles of this.roots.values()) {
      for (const id of tiles) counts.set(id, (counts.get(id) ?? 0) + 1);
    }
    return counts;
  }

  async gc(): Promise<GcResult> {
    this.epoch++;
    const live = this.liveSet();
    const marked: string[] = [];
    const reclaimed: string[] = [];
    for (const [id, rec] of this.blocks) {
      if (live.has(id)) {
        rec.orphanMark = null;
        continue;
      }
      if (this.leases.has(id)) continue;
      if (rec.orphanMark === null) {
        rec.orphanMark = this.epoch;
        marked.push(id);
      } else {
        reclaimed.push(id);
      }
    }
    for (const id of reclaimed) {
      const rec = this.blocks.get(id)!;
      this.blocks.delete(id);
      const bucket = this.buckets.get(rec.digest);
      if (bucket) {
        bucket.delete(id);
        if (bucket.size === 0) this.buckets.delete(rec.digest);
      }
    }
    await this.persist();
    return { epoch: this.epoch, marked, reclaimed };
  }

  verify(): VerifyReport {
    const errors: string[] = [];
    const expected = new Map<string, number>();
    for (const [name, tiles] of this.roots) {
      for (const id of tiles) {
        if (!this.blocks.has(id)) {
          errors.push("root " + name + " references missing block " + id);
          continue;
        }
        expected.set(id, (expected.get(id) ?? 0) + 1);
      }
    }
    for (const [id, rec] of this.blocks) {
      const want = expected.get(id) ?? 0;
      if (rec.refs !== want) {
        errors.push(
          "ledger mismatch on " + id + ": refs=" + rec.refs + " traversal=" + want,
        );
      }
      const bucket = this.buckets.get(rec.digest);
      if (!bucket || !bucket.has(id)) {
        errors.push("block " + id + " missing from digest bucket");
      }
    }
    for (const id of this.leases.keys()) {
      if (!this.blocks.has(id)) errors.push("lease on missing block " + id);
    }
    let unreachable = 0;
    for (const id of this.blocks.keys()) {
      if (!expected.has(id)) unreachable++;
    }
    return {
      ok: errors.length === 0,
      errors,
      liveBlocks: expected.size,
      unreachableBlocks: unreachable,
      leasedBlocks: this.leases.size,
    };
  }

  stats(): { blocks: number; roots: number; buckets: number; epoch: number; leases: number } {
    return {
      blocks: this.blocks.size,
      roots: this.roots.size,
      buckets: this.buckets.size,
      epoch: this.epoch,
      leases: this.leases.size,
    };
  }

  blockIds(): string[] {
    return [...this.blocks.keys()];
  }

  bucketSize(digest: string): number {
    return this.buckets.get(digest)?.size ?? 0;
  }

  debugTamperRefs(id: string, delta: number): void {
    const rec = this.blocks.get(id);
    if (!rec) throw new Error("unknown block: " + id);
    rec.refs += delta;
  }
}
