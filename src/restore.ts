import * as fs from "fs";
import * as path from "path";
import { checkpointDirName, chunkFileName, Manifest } from "./checkpoint";
import { parseChunk } from "./format";
import { sha256Hex } from "./hash";

export interface BadPageInfo {
  pageId: number;
  expectedHash: string;
  actualHash: string | null;
  keyCount: number | null;
  firstKey: string | null;
  lastKey: string | null;
}

export interface CorruptionDetails {
  checkpointId: number;
  chunkIndex: number;
  chunkFile: string;
  expectedChunkHash: string;
  actualChunkHash: string;
  badPages: BadPageInfo[];
}

export class CorruptionError extends Error {
  constructor(public readonly details: CorruptionDetails) {
    super(
      "checkpoint corruption detected in checkpoint cp-" +
        details.checkpointId +
        ", chunk " +
        details.chunkIndex +
        " (" +
        details.chunkFile +
        "): " +
        details.badPages.length +
        " bad page(s): " +
        details.badPages.map((p) => "page " + p.pageId).join(", ")
    );
    this.name = "CorruptionError";
  }
}

export class HashChainError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "HashChainError";
  }
}

export interface RestoreResult {
  state: Map<string, string>;
  checkpointId: number;
  seq: number;
  keyCount: number;
  verifiedChunks: number;
  verifiedPages: number;
}

interface LoadedManifest {
  manifest: Manifest;
  raw: string;
  dir: string;
}

function loadManifest(dir: string, id: number): LoadedManifest {
  const cpDir = path.join(dir, checkpointDirName(id));
  const file = path.join(cpDir, "manifest.json");
  const raw = fs.readFileSync(file, "utf8");
  return { manifest: JSON.parse(raw) as Manifest, raw, dir: cpDir };
}

export function latestCheckpointId(dir: string): number | null {
  if (!fs.existsSync(dir)) {
    return null;
  }
  let latest: number | null = null;
  for (const name of fs.readdirSync(dir)) {
    const match = /^cp-(\d+)$/.exec(name);
    if (match) {
      const id = parseInt(match[1], 10);
      if (latest === null || id > latest) {
        latest = id;
      }
    }
  }
  return latest;
}

/** Load the manifest chain from target back to genesis, verifying hash links. */
export function verifyManifestChain(dir: string, targetId: number): LoadedManifest[] {
  const chain: LoadedManifest[] = [];
  let currentId: number | null = targetId;
  while (currentId !== null) {
    const loaded = loadManifest(dir, currentId);
    if (loaded.manifest.id !== currentId) {
      throw new HashChainError("manifest id mismatch in cp-" + currentId);
    }
    if (chain.length > 0) {
      const child = chain[0];
      if (child.manifest.prevManifestHash !== sha256Hex(loaded.raw)) {
        throw new HashChainError(
          "hash chain broken between cp-" +
            loaded.manifest.id +
            " and cp-" +
            child.manifest.id +
            ": prevManifestHash does not match parent manifest content"
        );
      }
    }
    chain.unshift(loaded);
    currentId = loaded.manifest.prevManifestHash === null ? null : loaded.manifest.id - 1;
  }
  return chain;
}

function resolveChunkFile(chain: LoadedManifest[], targetIndex: number, chunkIndex: number): string {
  const target = chain[targetIndex];
  const entry = target.manifest.chunks[chunkIndex];
  if (entry.stored) {
    return path.join(target.dir, chunkFileName(chunkIndex));
  }
  if (entry.ref !== null) {
    for (let i = chain.length - 1; i >= 0; i--) {
      const candidate = chain[i];
      if (candidate.manifest.id === entry.ref && candidate.manifest.chunks[chunkIndex].stored) {
        return path.join(candidate.dir, chunkFileName(chunkIndex));
      }
    }
  }
  for (let i = targetIndex - 1; i >= 0; i--) {
    if (chain[i].manifest.chunks[chunkIndex].stored) {
      return path.join(chain[i].dir, chunkFileName(chunkIndex));
    }
  }
  throw new HashChainError("no stored copy found for chunk " + chunkIndex);
}

function locateBadPages(
  buffer: Buffer,
  manifest: Manifest
): BadPageInfo[] {
  const parsed = parseChunk(buffer);
  const bad: BadPageInfo[] = [];
  const seen = new Set<number>();
  for (const page of parsed.pages) {
    seen.add(page.pageId);
    const expected = page.pageId < manifest.pageHashes.length ? manifest.pageHashes[page.pageId] : null;
    if (expected === null || expected !== page.hash) {
      bad.push({
        pageId: page.pageId,
        expectedHash: expected === null ? "<unknown page>" : expected,
        actualHash: page.hash,
        keyCount: page.entries.length,
        firstKey: page.entries.length > 0 ? page.entries[0][0] : null,
        lastKey: page.entries.length > 0 ? page.entries[page.entries.length - 1][0] : null,
      });
    }
  }
  if (bad.length === 0) {
    // Chunk structure too damaged to isolate a page: report whole chunk range.
    const chunkIndex = parsed.chunkIndex >= 0 ? parsed.chunkIndex : -1;
    const perChunk = manifest.pagesPerChunk;
    const start = chunkIndex >= 0 ? chunkIndex * perChunk : 0;
    const end = Math.min(start + perChunk, manifest.pageHashes.length);
    for (let pageId = start; pageId < end; pageId++) {
      if (!seen.has(pageId)) {
        bad.push({
          pageId,
          expectedHash: manifest.pageHashes[pageId],
          actualHash: null,
          keyCount: null,
          firstKey: null,
          lastKey: null,
        });
      }
    }
  }
  return bad;
}

/**
 * Restore state from a checkpoint, verifying the manifest hash chain and
 * every chunk/page hash. Corrupted chunks are located precisely (chunk index
 * + page ids + key ranges) and rejected via CorruptionError.
 */
export function restoreState(dir: string, checkpointId?: number): RestoreResult {
  const targetId = checkpointId !== undefined ? checkpointId : latestCheckpointId(dir);
  if (targetId === null) {
    throw new Error("no checkpoints found in " + dir);
  }
  const chain = verifyManifestChain(dir, targetId);
  const target = chain[chain.length - 1];
  const manifest = target.manifest;

  const state = new Map<string, string>();
  let verifiedChunks = 0;
  let verifiedPages = 0;

  for (const chunk of manifest.chunks) {
    const file = resolveChunkFile(chain, chain.length - 1, chunk.index);
    let buffer: Buffer;
    try {
      buffer = fs.readFileSync(file);
    } catch (err) {
      throw new CorruptionError({
        checkpointId: manifest.id,
        chunkIndex: chunk.index,
        chunkFile: file,
        expectedChunkHash: chunk.hash,
        actualChunkHash: "<unreadable>",
        badPages: [],
      });
    }
    const actualChunkHash = sha256Hex(buffer);
    if (actualChunkHash !== chunk.hash) {
      throw new CorruptionError({
        checkpointId: manifest.id,
        chunkIndex: chunk.index,
        chunkFile: file,
        expectedChunkHash: chunk.hash,
        actualChunkHash,
        badPages: locateBadPages(buffer, manifest),
      });
    }
    const parsed = parseChunk(buffer);
    for (const page of parsed.pages) {
      const expected = manifest.pageHashes[page.pageId];
      if (expected !== page.hash) {
        throw new CorruptionError({
          checkpointId: manifest.id,
          chunkIndex: chunk.index,
          chunkFile: file,
          expectedChunkHash: chunk.hash,
          actualChunkHash,
          badPages: [
            {
              pageId: page.pageId,
              expectedHash: expected,
              actualHash: page.hash,
              keyCount: page.entries.length,
              firstKey: page.entries.length > 0 ? page.entries[0][0] : null,
              lastKey: page.entries.length > 0 ? page.entries[page.entries.length - 1][0] : null,
            },
          ],
        });
      }
      for (const [key, value] of page.entries) {
        state.set(key, value);
      }
      verifiedPages++;
    }
    verifiedChunks++;
  }

  return {
    state,
    checkpointId: manifest.id,
    seq: manifest.seq,
    keyCount: manifest.keyCount,
    verifiedChunks,
    verifiedPages,
  };
}
