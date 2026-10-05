/**
 * SetStore opens sharded set files read-only, loads header + ordered directory
 * once, and serves decoded blocks on demand (through the shared BlockCache).
 * Whole sets are never expanded in memory: fragments are read from disk per
 * shard and decoded block by block.
 */
import * as fs from 'fs';
import {
  DecodedBlock,
  FormatError,
  HEADER_SIZE,
  DIR_ENTRY_SIZE,
  SetFileHeader,
  decodeFragment,
  parseDirectory,
  parseHeader,
} from './format';
import { BlockCache } from './cache';

export interface OpenedSet {
  fileKey: string;
  setId: string;
  version: string;
  header: SetFileHeader;
}

interface OpenedEntry {
  opened: OpenedSet;
  mtimeMs: number;
  size: number;
  stamp: string; // cache-key prefix; changes when the file changes
}

export class SetStore {
  private opened = new Map<string, OpenedEntry>();
  readonly cache: BlockCache;
  /** counts blocks actually decoded from disk bytes (cache misses) */
  blocksDecoded = 0;

  constructor(cache: BlockCache) {
    this.cache = cache;
  }

  open(path: string): OpenedSet {
    const fileKey = fs.realpathSync(path);
    const stat = fs.statSync(fileKey);
    const cached = this.opened.get(fileKey);
    if (cached && cached.mtimeMs === stat.mtimeMs && cached.size === stat.size) {
      return cached.opened;
    }
    const fd = fs.openSync(fileKey, 'r');
    try {
      const headBuf = Buffer.alloc(HEADER_SIZE);
      readFully(fd, headBuf, 0);
      const head = parseHeader(headBuf);
      const dirBuf = Buffer.alloc(head.shardCount * DIR_ENTRY_SIZE);
      readFully(fd, dirBuf, HEADER_SIZE);
      const shards = parseDirectory(Buffer.concat([headBuf, dirBuf]), head.shardCount);
      const opened: OpenedSet = {
        fileKey,
        setId: head.setId,
        version: head.version,
        header: { setId: head.setId, version: head.version, shards },
      };
      this.opened.set(fileKey, {
        opened,
        mtimeMs: stat.mtimeMs,
        size: stat.size,
        stamp: fileKey + '@' + stat.mtimeMs + ':' + stat.size,
      });
      return opened;
    } finally {
      fs.closeSync(fd);
    }
  }

  private entry(fileKey: string): OpenedEntry {
    const e = this.opened.get(fileKey);
    if (!e) throw new FormatError('set file not open: ' + fileKey);
    return e;
  }

  readFragment(fileKey: string, shardIndex: number): Buffer {
    const opened = this.entry(fileKey).opened;
    const shard = opened.header.shards[shardIndex];
    if (!shard) throw new FormatError('shard index out of range: ' + shardIndex);
    const fd = fs.openSync(fileKey, 'r');
    try {
      const buf = Buffer.alloc(shard.fragLength);
      readFully(fd, buf, shard.fragOffset);
      return buf;
    } finally {
      fs.closeSync(fd);
    }
  }

  /**
   * Return the decoded block, serving from the shared cache when possible.
   * On a miss the whole fragment is decoded once and every block is cached,
   * which matches the sequential access pattern of streaming merges.
   */
  getBlock(fileKey: string, shardIndex: number, blockIndex: number): DecodedBlock {
    const stamp = this.entry(fileKey).stamp;
    const key = stamp + '#' + shardIndex + ':' + blockIndex;
    const hit = this.cache.get(key);
    if (hit) return { ids: hit, byteLength: hit.length * 4 };
    const frag = this.readFragment(fileKey, shardIndex);
    const blocks = decodeFragment(frag);
    for (let i = 0; i < blocks.length; i++) {
      this.cache.put(stamp + '#' + shardIndex + ':' + i, blocks[i].ids);
    }
    this.blocksDecoded += blocks.length;
    const block = blocks[blockIndex];
    if (!block) throw new FormatError('block index out of range: ' + blockIndex + ' in shard ' + shardIndex);
    return block;
  }

  blockCountOf(fileKey: string, shardIndex: number): number {
    const opened = this.entry(fileKey).opened;
    const shard = opened.header.shards[shardIndex];
    if (shard.count === 0) return 0;
    return Math.ceil(shard.count / 128);
  }
}

function readFully(fd: number, buf: Buffer, position: number): void {
  let offset = 0;
  while (offset < buf.length) {
    const n = fs.readSync(fd, buf, offset, buf.length - offset, position + offset);
    if (n === 0) throw new FormatError('unexpected end of file');
    offset += n;
  }
}
