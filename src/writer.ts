// Segment writer: term -> sorted unique doc ids -> compact index file.

import { writeFile } from 'node:fs/promises';
import { crc32 } from './crc32.ts';
import {
  BLOCK_META_SIZE,
  DEFAULT_BLOCK_SIZE,
  HEADER_SIZE,
  MAX_DOC_ID,
  MAX_TERM_BYTES,
  SUMMARY_SIZE,
  encodeBlock,
  encodeHeader,
  encodeSummary,
} from './format.ts';

export interface SegmentWriteStats {
  termCount: number;
  blockCount: number;
  postingCount: number;
  fileBytes: number;
}

export interface WriteOptions {
  blockSize?: number;
}

// Sorts ids, merges duplicates, validates range. Duplicate term keys are merged.
export function normalizePostings(
  input: Iterable<readonly [string, Iterable<number>]>,
): [string, number[]][] {
  const byTerm = new Map<string, Set<number>>();
  for (const [term, rawIds] of input) {
    if (Buffer.byteLength(term, 'utf8') > MAX_TERM_BYTES) {
      throw new Error(`term too long (> ${MAX_TERM_BYTES} bytes): ${JSON.stringify(term.slice(0, 32))}`);
    }
    let set = byTerm.get(term);
    if (!set) {
      set = new Set();
      byTerm.set(term, set);
    }
    for (const id of rawIds) {
      if (!Number.isInteger(id) || id < 0 || id > MAX_DOC_ID) {
        throw new Error(`illegal doc id ${id} for term ${JSON.stringify(term)}: must be integer in [0, ${MAX_DOC_ID}]`);
      }
      set.add(id);
    }
  }
  const entries: [string, number[]][] = [...byTerm.entries()].map(([term, set]) => [
    term,
    [...set].sort((a, b) => a - b),
  ]);
  // Deterministic order: by UTF-8 bytes.
  entries.sort((a, b) => Buffer.compare(Buffer.from(a[0], 'utf8'), Buffer.from(b[0], 'utf8')));
  return entries;
}

export function buildSegmentBuffer(
  input: Iterable<readonly [string, Iterable<number>]>,
  options: WriteOptions = {},
): { buf: Buffer; stats: SegmentWriteStats } {
  const blockSize = options.blockSize ?? DEFAULT_BLOCK_SIZE;
  if (!Number.isInteger(blockSize) || blockSize < 1) {
    throw new Error(`blockSize must be a positive integer, got ${blockSize}`);
  }
  const entries = normalizePostings(input);

  // Chunk each term's ids into blocks.
  const perTerm: { term: string; termBuf: Buffer; chunks: number[][] }[] = entries.map(([term, ids]) => {
    const chunks: number[][] = [];
    for (let i = 0; i < ids.length; i += blockSize) chunks.push(ids.slice(i, i + blockSize));
    return { term, termBuf: Buffer.from(term, 'utf8'), chunks };
  });

  let dictLength = 0;
  let totalBlocks = 0;
  for (const { termBuf, chunks } of perTerm) {
    dictLength += 2 + termBuf.length + 4 + chunks.length * BLOCK_META_SIZE;
    totalBlocks += chunks.length;
  }

  const dictOffset = HEADER_SIZE;
  const postingsOffset = dictOffset + dictLength;

  // Postings section + per-block skip-table metadata.
  const postingsParts: Buffer[] = [];
  let postingsLength = 0;
  const metas: { lastDocId: number; count: number; offset: number; length: number }[][] = [];
  for (const { term, chunks } of perTerm) {
    const termMetas = [];
    for (const chunk of chunks) {
      const block = encodeBlock(term, chunk);
      termMetas.push({
        lastDocId: chunk[chunk.length - 1],
        count: chunk.length,
        offset: postingsOffset + postingsLength,
        length: block.length,
      });
      postingsParts.push(block);
      postingsLength += block.length;
    }
    metas.push(termMetas);
  }

  // Dictionary section.
  const dict = Buffer.alloc(dictLength);
  let pos = 0;
  for (let i = 0; i < perTerm.length; i++) {
    const { termBuf, chunks } = perTerm[i];
    dict.writeUInt16LE(termBuf.length, pos);
    pos += 2;
    termBuf.copy(dict, pos);
    pos += termBuf.length;
    dict.writeUInt32LE(chunks.length, pos);
    pos += 4;
    for (const m of metas[i]) {
      dict.writeUInt32LE(m.lastDocId, pos);
      dict.writeUInt32LE(m.count, pos + 4);
      dict.writeBigUInt64LE(BigInt(m.offset), pos + 8);
      dict.writeUInt32LE(m.length, pos + 16);
      pos += BLOCK_META_SIZE;
    }
  }

  const header = encodeHeader({
    termCount: entries.length,
    dictOffset,
    dictLength,
    postingsOffset,
    postingsLength,
    summaryOffset: postingsOffset + postingsLength,
    summaryLength: SUMMARY_SIZE,
  });

  const postings = Buffer.concat(postingsParts);
  const fileLength = HEADER_SIZE + dictLength + postingsLength + SUMMARY_SIZE;
  const summary = encodeSummary({
    termCount: entries.length,
    totalBlocks,
    fileLength,
    headerCrc: crc32(header),
    dictCrc: crc32(dict),
    postingsCrc: crc32(postings),
    summaryCrc: 0, // filled below
  });
  summary.writeUInt32LE(crc32(summary, 0, SUMMARY_SIZE - 4), 32);

  const buf = Buffer.concat([header, dict, postings, summary]);
  let postingCount = 0;
  for (const [, ids] of entries) postingCount += ids.length;
  return {
    buf,
    stats: { termCount: entries.length, blockCount: totalBlocks, postingCount, fileBytes: buf.length },
  };
}

export async function writeSegment(
  input: Iterable<readonly [string, Iterable<number>]>,
  path: string,
  options: WriteOptions = {},
): Promise<SegmentWriteStats> {
  const { buf, stats } = buildSegmentBuffer(input, options);
  await writeFile(path, buf);
  return stats;
}
