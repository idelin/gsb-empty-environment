import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { sha256, chainHash, GENESIS_HASH } from './hash.ts';
import { decodeChunk, type Manifest } from './checkpointer.ts';
import { decodePage } from './stateTree.ts';

export interface CorruptionDetails {
  checkpointId: string;
  chunkIndex?: number;   // 1-based 坏块序号
  file?: string;
  pageId?: number;
  expected?: string;
  actual?: string;
}

export class CheckpointCorruptionError extends Error {
  readonly details: CorruptionDetails;

  constructor(message: string, details: CorruptionDetails) {
    super(message);
    this.name = 'CheckpointCorruptionError';
    this.details = details;
  }
}

export interface RestoreResult {
  state: Map<string, Buffer>;
  checkpointsApplied: string[];
  pagesVerified: number;
  chunksVerified: number;
}

async function loadManifest(dir: string, id: string): Promise<Manifest> {
  const raw = await readFile(path.join(dir, `${id}.manifest.json`), 'utf8');
  return JSON.parse(raw) as Manifest;
}

/**
 * 恢复流程：
 * 1. 沿清单日志重放，校验清单级哈希链；
 * 2. 逐块读取，校验块级链式哈希 —— 坏块可精确定位到分块序号；
 * 3. 逐页校验页哈希 —— 坏页可精确定位到 pageId；
 * 4. 全部通过后才应用数据，任何损坏都会拒绝并抛出 CheckpointCorruptionError。
 */
export async function restore(dir: string): Promise<RestoreResult> {
  const historyRaw = await readFile(path.join(dir, 'manifests.log'), 'utf8');
  const ids = historyRaw.split('\n').map((s) => s.trim()).filter(Boolean);
  if (ids.length === 0) throw new Error(`no checkpoints found in ${dir}`);

  // 阶段一：清单哈希链
  const manifests: Manifest[] = [];
  let prevManifestHash = GENESIS_HASH;
  for (const id of ids) {
    const manifest = await loadManifest(dir, id);
    if (manifest.prevManifestHash !== prevManifestHash) {
      throw new CheckpointCorruptionError(
        `manifest chain broken at ${id}: prevManifestHash mismatch`,
        { checkpointId: id, expected: prevManifestHash, actual: manifest.prevManifestHash },
      );
    }
    const { manifestHash, ...body } = manifest;
    const recomputed = chainHash(prevManifestHash, Buffer.from(JSON.stringify(body), 'utf8'));
    if (recomputed !== manifestHash) {
      throw new CheckpointCorruptionError(
        `manifest ${id} tampered: hash mismatch`,
        { checkpointId: id, expected: manifestHash, actual: recomputed },
      );
    }
    prevManifestHash = manifestHash;
    manifests.push(manifest);
  }

  // 阶段二：分块链式哈希 + 页哈希校验，全部通过才落数据
  const state = new Map<string, Buffer>();
  let pagesVerified = 0;
  let chunksVerified = 0;

  for (const manifest of manifests) {
    let prevChunkHash = manifest.prevManifestHash;
    const staged: Array<{ pageId: number; entries: Map<string, Buffer> }> = [];

    const bufs = await Promise.all(
      manifest.chunks.map((chunk) => readFile(path.join(dir, chunk.file))),
    );

    for (let i = 0; i < manifest.chunks.length; i++) {
      const chunk = manifest.chunks[i];
      const actual = chainHash(prevChunkHash, bufs[i]);
      if (actual !== chunk.hash) {
        const decoded = safeDecode(bufs[i]);
        throw new CheckpointCorruptionError(
          `chunk #${chunk.index} of checkpoint ${manifest.id} failed hash-chain verification ` +
            `(file=${chunk.file}, pages=[${decoded?.pages.map((p) => p.pageId).join(',') ?? 'unknown'}])`,
          {
            checkpointId: manifest.id,
            chunkIndex: chunk.index,
            file: chunk.file,
            expected: chunk.hash,
            actual,
          },
        );
      }
      prevChunkHash = actual;
      chunksVerified += 1;

      const { pages } = decodeChunk(bufs[i]);
      for (const page of pages) {
        const dirEntry = manifest.pages[String(page.pageId)];
        if (!dirEntry || dirEntry.hash !== page.hash) {
          throw new CheckpointCorruptionError(
            `page ${page.pageId} in chunk #${chunk.index} of ${manifest.id} failed page-hash verification`,
            {
              checkpointId: manifest.id,
              chunkIndex: chunk.index,
              file: chunk.file,
              pageId: page.pageId,
              expected: dirEntry?.hash,
              actual: page.hash,
            },
          );
        }
        staged.push({ pageId: page.pageId, entries: decodePage(page.data) });
        pagesVerified += 1;
      }
    }

    // 本检查点全部校验通过，应用（后序检查点覆盖先序，日志结构语义）
    for (const { entries } of staged) {
      for (const [key, value] of entries) state.set(key, value);
    }
  }

  return { state, checkpointsApplied: ids, pagesVerified, chunksVerified };
}

function safeDecode(buf: Buffer) {
  try {
    return decodeChunk(buf);
  } catch {
    return null;
  }
}
