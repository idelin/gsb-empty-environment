// Standalone segment validator. Never throws on malformed input: every
// structural or integrity problem is reported as a ValidationIssue carrying
// the file byte range and (when known) the term involved.

import { readFile } from 'node:fs/promises';
import { crc32 } from './crc32.ts';
import {
  FormatError,
  HEADER_SIZE,
  MAGIC,
  SUMMARY_MAGIC,
  SUMMARY_SIZE,
  VERSION,
  decodeBlockHeader,
  decodeHeader,
  decodePayloadIds,
  decodeSummary,
  parseDictionary,
  type TermEntry,
} from './format.ts';

export type IssueCode =
  | 'TRUNCATED'
  | 'BAD_MAGIC'
  | 'BAD_VERSION'
  | 'BAD_SUMMARY_MAGIC'
  | 'SECTION_OUT_OF_BOUNDS'
  | 'SECTION_OVERLAP'
  | 'BLOCK_OUT_OF_BOUNDS'
  | 'BLOCK_OVERLAP'
  | 'FILE_LENGTH_MISMATCH'
  | 'COUNT_MISMATCH'
  | 'CRC_MISMATCH'
  | 'PAYLOAD_CRC_MISMATCH'
  | 'TERM_MISMATCH'
  | 'BLOCK_HEADER_MISMATCH'
  | 'NON_INCREASING_ID'
  | 'INTEGER_OVERFLOW'
  | 'MALFORMED';

export interface ValidationIssue {
  code: IssueCode;
  message: string;
  byteStart: number; // inclusive, clamped to file size
  byteEnd: number; // exclusive, clamped to file size
  term?: string;
}

export interface ValidationReport {
  ok: boolean;
  issues: ValidationIssue[];
  termCount: number;
  blockCount: number;
  fileBytes: number;
}

export function validateSegmentBuffer(buf: Buffer): ValidationReport {
  const issues: ValidationIssue[] = [];
  const fail = (code: IssueCode, message: string, byteStart: number, byteEnd: number, term?: string) => {
    issues.push({
      code,
      message,
      byteStart: Math.max(0, Math.min(byteStart, buf.length)),
      byteEnd: Math.max(0, Math.min(byteEnd, buf.length)),
      term,
    });
  };
  const report = (termCount = 0, blockCount = 0): ValidationReport => ({
    ok: issues.length === 0,
    issues,
    termCount,
    blockCount,
    fileBytes: buf.length,
  });

  // ---- Header ----
  if (buf.length < HEADER_SIZE) {
    fail('TRUNCATED', `file is ${buf.length} bytes, header needs ${HEADER_SIZE}`, 0, buf.length);
    return report();
  }
  if (!buf.subarray(0, 4).equals(MAGIC)) {
    fail('BAD_MAGIC', 'missing CIDX magic', 0, 4);
    return report();
  }
  const version = buf.readUInt8(4);
  if (version !== VERSION) {
    fail('BAD_VERSION', `unsupported version ${version}`, 4, 5);
    return report();
  }

  let header;
  try {
    header = decodeHeader(buf);
  } catch (e) {
    if (e instanceof FormatError) {
      fail('INTEGER_OVERFLOW', e.message, e.offset, e.offset + 8);
      return report();
    }
    throw e;
  }

  // ---- Section bounds and overlap ----
  const sections = [
    { name: 'dictionary', offset: header.dictOffset, length: header.dictLength },
    { name: 'postings', offset: header.postingsOffset, length: header.postingsLength },
    { name: 'summary', offset: header.summaryOffset, length: header.summaryLength },
  ];
  let structuralFailure = false;
  for (const s of sections) {
    if (s.offset < HEADER_SIZE || s.offset + s.length > buf.length) {
      fail(
        'SECTION_OUT_OF_BOUNDS',
        `${s.name} section [${s.offset}, ${s.offset + s.length}) outside file (header ends at ${HEADER_SIZE}, file is ${buf.length} bytes)`,
        s.offset,
        s.offset + s.length,
      );
      structuralFailure = true;
    }
  }
  const sorted = [...sections].sort((a, b) => a.offset - b.offset);
  for (let i = 1; i < sorted.length; i++) {
    const prev = sorted[i - 1];
    const next = sorted[i];
    if (prev.offset + prev.length > next.offset) {
      fail(
        'SECTION_OVERLAP',
        `${prev.name} and ${next.name} sections overlap`,
        next.offset,
        prev.offset + prev.length,
      );
      structuralFailure = true;
    }
  }
  if (structuralFailure) return report();
  if (header.summaryLength !== SUMMARY_SIZE) {
    fail('MALFORMED', `summary section is ${header.summaryLength} bytes, expected ${SUMMARY_SIZE}`, header.summaryOffset, header.summaryOffset + header.summaryLength);
    return report();
  }

  // ---- Summary and checksums ----
  const summaryOffset = header.summaryOffset;
  let summary;
  try {
    summary = decodeSummary(buf, summaryOffset);
  } catch (e) {
    if (e instanceof FormatError) {
      fail('INTEGER_OVERFLOW', e.message, e.offset, e.offset + 8);
      return report();
    }
    throw e;
  }
  const summaryOk = buf.subarray(summaryOffset, summaryOffset + 4).equals(SUMMARY_MAGIC);
  if (!summaryOk) {
    fail('BAD_SUMMARY_MAGIC', 'missing CSUM magic', summaryOffset, summaryOffset + 4);
  }
  if (summary.fileLength !== buf.length) {
    fail(
      'FILE_LENGTH_MISMATCH',
      `summary records ${summary.fileLength} bytes but file has ${buf.length} (truncated or appended)`,
      summaryOffset + 12,
      summaryOffset + 20,
    );
  }
  if (summary.termCount !== header.termCount) {
    fail('COUNT_MISMATCH', `summary termCount ${summary.termCount} != header ${header.termCount}`, summaryOffset + 4, summaryOffset + 8);
  }
  const crcChecks = [
    { name: 'summary', stored: summary.summaryCrc, start: summaryOffset, end: summaryOffset + SUMMARY_SIZE - 4, rangeStart: summaryOffset + 32, rangeEnd: summaryOffset + 36 },
    { name: 'header', stored: summary.headerCrc, start: 0, end: HEADER_SIZE, rangeStart: summaryOffset + 20, rangeEnd: summaryOffset + 24 },
    { name: 'dictionary', stored: summary.dictCrc, start: header.dictOffset, end: header.dictOffset + header.dictLength, rangeStart: summaryOffset + 24, rangeEnd: summaryOffset + 28 },
    { name: 'postings', stored: summary.postingsCrc, start: header.postingsOffset, end: header.postingsOffset + header.postingsLength, rangeStart: summaryOffset + 28, rangeEnd: summaryOffset + 32 },
  ];
  for (const c of crcChecks) {
    if (crc32(buf, c.start, c.end) !== c.stored) {
      fail(
        'CRC_MISMATCH',
        `${c.name} crc mismatch: section [${c.start}, ${c.end}) was modified, truncated or reordered after writing`,
        c.start,
        c.end,
      );
    }
  }

  // ---- Dictionary ----
  let entries: TermEntry[];
  try {
    entries = parseDictionary(buf, header.dictOffset, header.dictLength, header.termCount);
  } catch (e) {
    if (e instanceof FormatError) {
      fail(e.code as IssueCode, `dictionary: ${e.message}`, e.offset, e.offset + 1);
      return report(header.termCount);
    }
    throw e;
  }
  const seen = new Set<string>();
  let totalBlocks = 0;
  for (const e of entries) {
    if (seen.has(e.term)) {
      fail('MALFORMED', `duplicate dictionary term ${JSON.stringify(e.term)}`, header.dictOffset, header.dictOffset + header.dictLength, e.term);
    }
    seen.add(e.term);
    totalBlocks += e.blocks.length;
  }
  if (summaryOk && summary.totalBlocks !== totalBlocks) {
    fail('COUNT_MISMATCH', `summary totalBlocks ${summary.totalBlocks} != dictionary ${totalBlocks}`, summaryOffset + 8, summaryOffset + 12);
  }

  // ---- Block placement: inside postings section, no overlaps ----
  const postingsEnd = header.postingsOffset + header.postingsLength;
  const placed: { term: string; meta: TermEntry['blocks'][number] }[] = [];
  for (const e of entries) {
    for (const meta of e.blocks) {
      if (meta.offset < header.postingsOffset || meta.offset + meta.length > postingsEnd) {
        fail(
          'BLOCK_OUT_OF_BOUNDS',
          `block [${meta.offset}, ${meta.offset + meta.length}) outside postings section [${header.postingsOffset}, ${postingsEnd})`,
          meta.offset,
          meta.offset + meta.length,
          e.term,
        );
      } else {
        placed.push({ term: e.term, meta });
      }
    }
  }
  placed.sort((a, b) => a.meta.offset - b.meta.offset);
  for (let i = 1; i < placed.length; i++) {
    const prev = placed[i - 1];
    const next = placed[i];
    if (prev.meta.offset + prev.meta.length > next.meta.offset) {
      fail(
        'BLOCK_OVERLAP',
        `blocks of terms ${JSON.stringify(prev.term)} and ${JSON.stringify(next.term)} overlap`,
        next.meta.offset,
        prev.meta.offset + prev.meta.length,
        next.term,
      );
    }
  }

  // ---- Per-block content: term binding, range, count, crc, ids ----
  for (const { term, meta } of placed) {
    const start = meta.offset;
    const end = start + meta.length;
    let bh;
    try {
      bh = decodeBlockHeader(buf, start, end);
    } catch (e) {
      if (e instanceof FormatError) {
        fail(e.code as IssueCode, `block of ${JSON.stringify(term)}: ${e.message}`, start, end, term);
        continue;
      }
      throw e;
    }
    if (bh.term !== term) {
      fail(
        'TERM_MISMATCH',
        `block is bound to term ${JSON.stringify(bh.term)} but dictionary expects ${JSON.stringify(term)} at this offset (bytes swapped or offset corrupted)`,
        start,
        end,
        term,
      );
    }
    if (bh.count !== meta.count || bh.lastDocId !== meta.lastDocId) {
      fail(
        'BLOCK_HEADER_MISMATCH',
        `block header (lastDocId=${bh.lastDocId}, count=${bh.count}) != skip table (lastDocId=${meta.lastDocId}, count=${meta.count})`,
        start,
        end,
        term,
      );
    }
    if (bh.count < 1) {
      fail('MALFORMED', 'block declares zero postings', start, end, term);
    }
    if (bh.firstDocId > bh.lastDocId) {
      fail('MALFORMED', `block firstDocId ${bh.firstDocId} > lastDocId ${bh.lastDocId}`, start, end, term);
    }
    if (crc32(buf, bh.payloadOffset, end) !== bh.payloadCrc) {
      fail('PAYLOAD_CRC_MISMATCH', 'block payload crc mismatch', bh.payloadOffset, end, term);
    }
    try {
      decodePayloadIds(buf, bh.payloadOffset, end, bh.count, bh.firstDocId, bh.lastDocId);
    } catch (e) {
      if (e instanceof FormatError) {
        fail(e.code as IssueCode, `block of ${JSON.stringify(term)}: ${e.message}`, e.offset, e.offset + 1, term);
      } else {
        throw e;
      }
    }
  }

  return report(entries.length, totalBlocks);
}

export async function validateSegment(path: string): Promise<ValidationReport> {
  return validateSegmentBuffer(await readFile(path));
}
