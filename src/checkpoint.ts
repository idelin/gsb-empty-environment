import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { Worker } from "worker_threads";
import { PageInput } from "./format";
import { sha256Hex } from "./hash";
import { StateTree } from "./stateTree";
import { ChunkResult, ChunkTask } from "./worker";

export interface CheckpointOptions {
  pagesPerChunk?: number;
  workers?: number;
}

export interface ChunkManifestEntry {
  index: number;
  hash: string;
  stored: boolean;
  ref: number | null;
}

export interface Manifest {
  version: 1;
  id: number;
  seq: number;
  keyCount: number;
  createdAt: string;
  pageCapacity: number;
  pagesPerChunk: number;
  prevManifestHash: string | null;
  pageHashes: string[];
  chunks: ChunkManifestEntry[];
}

export interface CheckpointInfo {
  id: number;
  seq: number;
  keyCount: number;
  pageCount: number;
  chunkCount: number;
  chunksStored: number;
  chunksReused: number;
  bytesWritten: number;
  durationMs: number;
  dir: string;
}

interface PendingTask {
  task: ChunkTask;
  resolve: (result: ChunkResult) => void;
  reject: (err: Error) => void;
}

class WorkerPool {
  private workers: Worker[] = [];
  private idle: Worker[] = [];
  private queue: PendingTask[] = [];
  private failed: Error | null = null;

  constructor(size: number, workerPath: string) {
    for (let i = 0; i < size; i++) {
      const worker = new Worker(workerPath);
      worker.on("error", (err: Error) => {
        this.failed = err;
        const pending = this.queue.splice(0);
        for (const p of pending) {
          p.reject(err);
        }
      });
      this.workers.push(worker);
      this.idle.push(worker);
    }
  }

  run(task: ChunkTask): Promise<ChunkResult> {
    if (this.failed) {
      return Promise.reject(this.failed);
    }
    return new Promise<ChunkResult>((resolve, reject) => {
      this.queue.push({ task, resolve, reject });
      this.pump();
    });
  }

  private pump(): void {
    while (this.idle.length > 0 && this.queue.length > 0) {
      const worker = this.idle.pop() as Worker;
      const pending = this.queue.shift() as PendingTask;
      const onMessage = (result: ChunkResult) => {
        worker.removeListener("message", onMessage);
        this.idle.push(worker);
        pending.resolve(result);
        this.pump();
      };
      worker.on("message", onMessage);
      worker.postMessage(pending.task);
    }
  }

  async close(): Promise<void> {
    await Promise.all(this.workers.map((w) => w.terminate()));
  }
}

export function chunkFileName(index: number): string {
  let name = String(index);
  while (name.length < 4) {
    name = "0" + name;
  }
  return "chunk-" + name + ".bin";
}

export function checkpointDirName(id: number): string {
  return "cp-" + id;
}

export class CheckpointManager {
  private readonly dir: string;
  private readonly pagesPerChunk: number;
  private readonly workerCount: number;
  private prevPageHashes: string[] | null = null;
  private prevChunkHashes: string[] | null = null;
  private prevManifestHash: string | null = null;
  private chunkStoredAt: number[] = [];
  private lastId = 0;

  constructor(private readonly tree: StateTree, checkpointsDir: string, options: CheckpointOptions = {}) {
    this.dir = checkpointsDir;
    this.pagesPerChunk = options.pagesPerChunk || 16;
    this.workerCount = options.workers || Math.max(2, os.cpus().length - 1);
    fs.mkdirSync(this.dir, { recursive: true });
  }

  get checkpointsDir(): string {
    return this.dir;
  }

  async createCheckpoint(): Promise<CheckpointInfo> {
    const started = Date.now();
    const snapshot = this.tree.snapshot();
    try {
      const id = this.lastId + 1;
      const cpDir = path.join(this.dir, checkpointDirName(id));
      fs.mkdirSync(cpDir, { recursive: true });

      const chunkCount = Math.ceil(snapshot.pages.length / this.pagesPerChunk) || 0;
      const pool = new WorkerPool(Math.min(this.workerCount, Math.max(1, chunkCount)), path.join(__dirname, "worker.js"));
      let results: ChunkResult[];
      try {
        const jobs: Array<Promise<ChunkResult>> = [];
        for (let c = 0; c < chunkCount; c++) {
          const pages: PageInput[] = [];
          const start = c * this.pagesPerChunk;
          const end = Math.min(start + this.pagesPerChunk, snapshot.pages.length);
          for (let p = start; p < end; p++) {
            pages.push({ pageId: p, entries: snapshot.pages[p] });
          }
          jobs.push(pool.run({ chunkIndex: c, pages }));
        }
        results = await Promise.all(jobs);
      } finally {
        await pool.close();
      }
      results.sort((a, b) => a.chunkIndex - b.chunkIndex);

      const pageHashes: string[] = [];
      for (const result of results) {
        for (const hash of result.pageHashes) {
          pageHashes.push(hash);
        }
      }

      const chunks: ChunkManifestEntry[] = [];
      let chunksStored = 0;
      let chunksReused = 0;
      let bytesWritten = 0;
      for (const result of results) {
        const reused =
          this.prevChunkHashes !== null &&
          result.chunkIndex < this.prevChunkHashes.length &&
          this.prevChunkHashes[result.chunkIndex] === result.chunkHash;
        if (reused) {
          chunks.push({ index: result.chunkIndex, hash: result.chunkHash, stored: false, ref: this.chunkStoredAt[result.chunkIndex] });
          chunksReused++;
        } else {
          const file = path.join(cpDir, chunkFileName(result.chunkIndex));
          fs.writeFileSync(file, result.buffer);
          bytesWritten += result.buffer.length;
          this.chunkStoredAt[result.chunkIndex] = id;
          chunks.push({ index: result.chunkIndex, hash: result.chunkHash, stored: true, ref: null });
          chunksStored++;
        }
      }

      const manifest: Manifest = {
        version: 1,
        id,
        seq: snapshot.seq,
        keyCount: snapshot.keyCount,
        createdAt: new Date().toISOString(),
        pageCapacity: snapshot.pageCapacity,
        pagesPerChunk: this.pagesPerChunk,
        prevManifestHash: this.prevManifestHash,
        pageHashes,
        chunks,
      };
      const manifestRaw = JSON.stringify(manifest, null, 2);
      fs.writeFileSync(path.join(cpDir, "manifest.json"), manifestRaw);

      this.prevPageHashes = pageHashes;
      this.prevChunkHashes = results.map((r) => r.chunkHash);
      this.prevManifestHash = sha256Hex(manifestRaw);
      this.lastId = id;

      return {
        id,
        seq: snapshot.seq,
        keyCount: snapshot.keyCount,
        pageCount: snapshot.pages.length,
        chunkCount,
        chunksStored,
        chunksReused,
        bytesWritten,
        durationMs: Date.now() - started,
        dir: cpDir,
      };
    } finally {
      this.tree.releaseSnapshot();
    }
  }

}
