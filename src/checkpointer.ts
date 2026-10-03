import { mkdir, writeFile, appendFile } from 'node:fs/promises';
import path from 'node:path';
import { sha256, chainHash, GENESIS_HASH } from './hash.ts';
import { StateTree, type PageSnapshot } from './stateTree.ts';

export interface ChunkMeta {
  index: number;        // 1-based 分块序号
  file: string;
  hash: string;         // 链式哈希：sha256(prevChunkHash || chunkBytes)
  pageCount: number;
  bytes: number;
}

export interface Manifest {
  version: 1;
  id: string;                 // chk-000001
  type: 'full' | 'incr';
  createdAt: string;
  counterStart: number;       // 覆盖的写入计数区间
  counterEnd: number;
  prevManifestHash: string;   // 指向上一个清单，构成清单级哈希链
  chunks: ChunkMeta[];
  pages: Record<string, { chunk: number; hash: string }>; // 截至本检查点的全量页目录
  manifestHash: string;       // sha256(prevManifestHash || canonical body)
}

export interface CheckpointOptions {
  pagesPerChunk?: number;     // 每个分块包含的页数
  yieldEveryPages?: number;   // 每序列化多少页让出一次事件循环
}

const MAGIC = Buffer.from('CKPT');

function encodeChunk(chunkIndex: number, pages: PageSnapshot[]): Buffer {
  const head = Buffer.alloc(12);
  MAGIC.copy(head, 0);
  head.writeUInt32LE(chunkIndex, 4);
  head.writeUInt32LE(pages.length, 8);
  const parts: Buffer[] = [head];
  for (const page of pages) {
    const meta = Buffer.alloc(8);
    meta.writeUInt32LE(page.pageId, 0);
    meta.writeUInt32LE(page.data.length, 4);
    parts.push(meta, page.data);
  }
  return Buffer.concat(parts);
}

export interface DecodedChunk {
  chunkIndex: number;
  pages: PageSnapshot[];
}

export function decodeChunk(buf: Buffer): DecodedChunk {
  if (buf.length < 12 || !buf.subarray(0, 4).equals(MAGIC)) {
    throw new Error('invalid chunk: bad magic');
  }
  const chunkIndex = buf.readUInt32LE(4);
  const pageCount = buf.readUInt32LE(8);
  let offset = 12;
  const pages: PageSnapshot[] = [];
  for (let i = 0; i < pageCount; i++) {
    const pageId = buf.readUInt32LE(offset);
    const dataLen = buf.readUInt32LE(offset + 4);
    offset += 8;
    const data = buf.subarray(offset, offset + dataLen);
    offset += dataLen;
    pages.push({ pageId, hash: sha256(data), data: Buffer.from(data), entryCount: -1 });
  }
  return { chunkIndex, pages };
}

const yieldToLoop = () => new Promise<void>((resolve) => setImmediate(resolve));

/**
 * 日志结构检查点生成器。
 * - 全量：序列化所有页；增量：仅序列化上次检查点以来的脏页（差量包）。
 * - 分块并行落盘；块间以链式哈希连接，清单间再构成一级哈希链。
 * - 序列化过程周期性让出事件循环，写入线程不被长时间阻塞。
 */
export class Checkpointer {
  private lastCounter = 0;
  private prevManifestHash = GENESIS_HASH;
  private pageDirectory = new Map<number, { chunk: number; hash: string }>();
  private sequence = 0;
  private running: Promise<Manifest> | null = null;

  readonly pagesPerChunk: number;
  readonly yieldEveryPages: number;
  private tree: StateTree;
  private dir: string;

  constructor(tree: StateTree, dir: string, opts: CheckpointOptions = {}) {
    this.tree = tree;
    this.dir = dir;
    this.pagesPerChunk = opts.pagesPerChunk ?? 32;
    this.yieldEveryPages = opts.yieldEveryPages ?? 16;
  }

  get manifestHistoryPath(): string {
    return path.join(this.dir, 'manifests.log');
  }

  /** 触发一次检查点；并发触发时复用进行中的任务 */
  checkpoint(): Promise<Manifest> {
    if (!this.running) {
      this.running = this.doCheckpoint().finally(() => {
        this.running = null;
      });
    }
    return this.running;
  }

  private async doCheckpoint(): Promise<Manifest> {
    await mkdir(this.dir, { recursive: true });
    const counterStart = this.lastCounter;
    const counterEnd = this.tree.writeCounter;
    const isFull = this.sequence === 0;
    const pageIds = isFull ? this.tree.allPageIds() : this.tree.dirtyPages(this.lastCounter);

    // 1) 逐页生成只读快照（周期性让出事件循环，控制写入延迟抖动）
    const snapshots: PageSnapshot[] = [];
    for (let i = 0; i < pageIds.length; i++) {
      const snap = this.tree.snapshotPage(pageIds[i]);
      if (snap) snapshots.push(snap);
      if ((i + 1) % this.yieldEveryPages === 0) await yieldToLoop();
    }

    // 2) 切片成分块并计算链式哈希
    const chunkBuffers: Buffer[] = [];
    const chunks: ChunkMeta[] = [];
    let prevChunkHash = this.prevManifestHash; // 与上一个清单衔接，形成跨检查点哈希链
    for (let i = 0; i < snapshots.length; i += this.pagesPerChunk) {
      const index = chunks.length + 1;
      const slice = snapshots.slice(i, i + this.pagesPerChunk);
      const buf = encodeChunk(index, slice);
      const hash = chainHash(prevChunkHash, buf);
      prevChunkHash = hash;
      chunkBuffers.push(buf);
      chunks.push({ index, file: '', hash, pageCount: slice.length, bytes: buf.length });
      await yieldToLoop();
    }

    const id = `chk-${String(this.sequence + 1).padStart(6, '0')}`;
    for (const chunk of chunks) {
      chunk.file = `${id}-chunk-${String(chunk.index).padStart(4, '0')}.bin`;
    }

    // 3) 分块并行写盘
    await Promise.all(
      chunks.map((chunk, i) => writeFile(path.join(this.dir, chunk.file), chunkBuffers[i])),
    );

    // 4) 更新页目录并生成清单（清单自身也入哈希链）
    snapshots.forEach((snap, i) => {
      const chunk = chunks[Math.floor(i / this.pagesPerChunk)];
      this.pageDirectory.set(snap.pageId, { chunk: chunk.index, hash: snap.hash });
    });

    const body = {
      version: 1 as const,
      id,
      type: (isFull ? 'full' : 'incr') as 'full' | 'incr',
      createdAt: new Date().toISOString(),
      counterStart,
      counterEnd,
      prevManifestHash: this.prevManifestHash,
      chunks,
      pages: Object.fromEntries(
        [...this.pageDirectory.entries()].map(([pid, meta]) => [String(pid), meta]),
      ),
    };
    const manifestHash = chainHash(
      this.prevManifestHash,
      Buffer.from(JSON.stringify(body), 'utf8'),
    );
    const manifest: Manifest = { ...body, manifestHash };

    await writeFile(
      path.join(this.dir, `${id}.manifest.json`),
      JSON.stringify(manifest, null, 2),
    );
    await appendFile(this.manifestHistoryPath, `${id}\n`, 'utf8');

    this.prevManifestHash = manifestHash;
    this.lastCounter = counterEnd;
    this.sequence += 1;
    return manifest;
  }
}
