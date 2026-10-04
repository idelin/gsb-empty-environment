// Standalone segment validator. Never throws: every structural or
// integrity problem is reported as a ValidationError carrying the file
// byte range [offset, offset+length) and the owning term when known.

import {
  BLOCK_HEADER_SIZE,
  BLOCK_MAGIC,
  DIGEST_SIZE,
  FormatError,
  HEADER_SIZE,
  MAGIC,
  MAX_DOC_ID,
  VERSION,
  compareTerms,
  crc32,
  decodeVarint,
  termHash,
} from "./format.ts";

export interface ValidationError {
  code: string;
  message: string;
  offset: number;
  length: number;
  term?: string;
}

export interface ValidationResult {
  ok: boolean;
  errors: ValidationError[];
}

interface JumpEntry {
  offset: number; // relative to the term's postings span
  maxDocId: number;
  count: number;
  entryOffset: number; // absolute file offset of this jump-table entry
}

interface TermEntry {
  term: string;
  entryOffset: number; // absolute file offset of the dictionary entry
  postingsOffset: number; // relative to the postings region
  postingsLength: number;
  totalCount: number;
  jump: JumpEntry[];
}

class Collector {
  errors: ValidationError[] = [];
  add(code: string, offset: number, length: number, message: string, term?: string): void {
    this.errors.push({ code, offset, length, message, term });
  }
}

export function validateSegment(data: Buffer): ValidationResult {
  const c = new Collector();
  try {
    validate(data, c);
  } catch (e) {
    c.add("internal", 0, 0, `validator internal error: ${(e as Error).message}`);
  }
  return { ok: c.errors.length === 0, errors: c.errors };
}

function validate(data: Buffer, c: Collector): void {
  const fileSize = data.length;

  // ---- header ----
  if (fileSize < HEADER_SIZE + DIGEST_SIZE) {
    c.add("truncated", 0, fileSize, `file of ${fileSize} bytes is smaller than header+digest (${HEADER_SIZE + DIGEST_SIZE})`);
    return;
  }
  if (data.readUInt32LE(0) !== MAGIC) {
    c.add("bad-magic", 0, 4, "bad magic, not a CIDX segment");
    return;
  }
  const version = data.readUInt32LE(4);
  if (version !== VERSION) {
    c.add("bad-version", 4, 4, `unsupported version ${version}`);
    return;
  }
  const termCount = data.readUInt32LE(8);
  const dictOffset = data.readUInt32LE(12);
  const dictLength = data.readUInt32LE(16);
  const postingsOffset = data.readUInt32LE(20);
  const postingsLength = data.readUInt32LE(24);
  const bodyEnd = fileSize - DIGEST_SIZE;

  // ---- region bounds & overlap ----
  const dictEnd = dictOffset + dictLength;
  const postingsEnd = postingsOffset + postingsLength;
  if (dictEnd < dictOffset || dictEnd > bodyEnd) {
    c.add("region-out-of-bounds", 12, 8, `dictionary region [${dictOffset}, ${dictEnd}) exceeds body end ${bodyEnd}`);
    return;
  }
  if (postingsEnd < postingsOffset || postingsEnd > bodyEnd) {
    c.add("region-out-of-bounds", 20, 8, `postings region [${postingsOffset}, ${postingsEnd}) exceeds body end ${bodyEnd}`);
    return;
  }
  if (dictOffset < postingsEnd && postingsOffset < dictEnd) {
    c.add("region-overlap", dictOffset, dictLength, `dictionary region overlaps postings region [${postingsOffset}, ${postingsEnd})`);
  }

  // ---- digest over everything before the trailing 4 bytes ----
  const expected = data.readUInt32LE(bodyEnd);
  const actual = crc32(data, 0, bodyEnd);
  if (expected !== actual) {
    c.add("digest-mismatch", bodyEnd, DIGEST_SIZE,
      `CRC-32 mismatch: stored 0x${expected.toString(16)}, computed 0x${actual.toString(16)} (file truncated or modified)`);
  }

  // ---- dictionary ----
  const terms: TermEntry[] = [];
  let pos = dictOffset;
  let prevTerm: string | undefined;
  for (let i = 0; i < termCount; i++) {
    const entryOffset = pos;
    if (pos + 2 > dictEnd) {
      c.add("truncated", pos, dictEnd - pos, `dictionary ends inside term entry #${i}`);
      break;
    }
    const termLen = data.readUInt16LE(pos); pos += 2;
    if (pos + termLen + 16 > dictEnd) {
      c.add("truncated", entryOffset, dictEnd - entryOffset, `dictionary ends inside term entry #${i}`);
      break;
    }
    const term = data.toString("utf8", pos, pos + termLen); pos += termLen;
    const postingsOff = data.readUInt32LE(pos); pos += 4;
    const postingsLen = data.readUInt32LE(pos); pos += 4;
    const totalCount = data.readUInt32LE(pos); pos += 4;
    const blockCount = data.readUInt32LE(pos); pos += 4;
    if (pos + blockCount * 12 > dictEnd) {
      c.add("truncated", entryOffset, dictEnd - entryOffset,
        `jump table of term ${JSON.stringify(term)} (${blockCount} blocks) overruns dictionary`, term);
      break;
    }
    const jump: JumpEntry[] = [];
    for (let b = 0; b < blockCount; b++) {
      jump.push({
        offset: data.readUInt32LE(pos),
        maxDocId: data.readUInt32LE(pos + 4),
        count: data.readUInt32LE(pos + 8),
        entryOffset: pos,
      });
      pos += 12;
    }
    if (prevTerm !== undefined && compareTerms(prevTerm, term) >= 0) {
      c.add("dictionary-order", entryOffset, 2 + termLen,
        `term ${JSON.stringify(term)} is not strictly after ${JSON.stringify(prevTerm)}`, term);
    }
    prevTerm = term;
    terms.push({ term, entryOffset, postingsOffset: postingsOff, postingsLength: postingsLen, totalCount, jump });
  }
  if (pos !== dictEnd) {
    c.add("dictionary-size", pos, dictEnd - pos, `dictionary has ${dictEnd - pos} trailing bytes after ${termCount} entries`);
  }

  // ---- per-term span bounds and overlap between terms ----
  const spans = terms.map((t) => ({
    term: t.term,
    start: postingsOffset + t.postingsOffset,
    end: postingsOffset + t.postingsOffset + t.postingsLength,
  }));
  for (const s of spans) {
    if (s.end < s.start || s.end > postingsEnd) {
      c.add("span-out-of-bounds", Math.max(s.start, 0), Math.max(s.end - s.start, 0),
        `postings span [${s.start}, ${s.end}) exceeds postings region end ${postingsEnd}`, s.term);
    }
  }
  const sorted = [...spans].sort((a, b) => a.start - b.start);
  for (let i = 1; i < sorted.length; i++) {
    if (sorted[i].start < sorted[i - 1].end) {
      c.add("span-overlap", sorted[i].start, sorted[i - 1].end - sorted[i].start,
        `postings spans of terms ${JSON.stringify(sorted[i - 1].term)} and ${JSON.stringify(sorted[i].term)} overlap`,
        sorted[i].term);
    }
  }

  // ---- per-term jump table and blocks ----
  for (const t of terms) {
    validateTerm(data, c, t, postingsOffset, postingsEnd);
  }
}

function validateTerm(data: Buffer, c: Collector, t: TermEntry, postingsOffset: number, postingsEnd: number): void {
  const termBase = postingsOffset + t.postingsOffset;
  const termEnd = termBase + t.postingsLength;

  if ((t.jump.length === 0) !== (t.totalCount === 0)) {
    c.add("count-inconsistent", t.entryOffset, 8,
      `blockCount=${t.jump.length} but totalCount=${t.totalCount}`, t.term);
  }
  let countSum = 0;
  let prevJumpMax = -1;
  let expectedBlockStart = termBase;
  let prevBlockLast = -1;

  for (let b = 0; b < t.jump.length; b++) {
    const j = t.jump[b];
    countSum += j.count;
    if (countSum > MAX_DOC_ID + 1) {
      c.add("count-overflow", j.entryOffset, 12, `cumulative block counts overflow`, t.term);
      break;
    }
    if (j.maxDocId <= prevJumpMax) {
      c.add("jump-order", j.entryOffset, 12,
        `jump table maxDocID ${j.maxDocId} not greater than previous ${prevJumpMax}`, t.term);
    }
    prevJumpMax = j.maxDocId;

    const blockStart = termBase + j.offset;
    if (j.offset >= t.postingsLength || blockStart + BLOCK_HEADER_SIZE > termEnd || blockStart + BLOCK_HEADER_SIZE > postingsEnd) {
      c.add("block-out-of-bounds", j.entryOffset, 12,
        `block #${b} at ${blockStart} lies outside the term postings span [${termBase}, ${termEnd})`, t.term);
      continue;
    }
    if (blockStart !== expectedBlockStart) {
      c.add("block-overlap", Math.min(blockStart, expectedBlockStart), Math.abs(blockStart - expectedBlockStart),
        `block #${b} starts at ${blockStart}, expected ${expectedBlockStart} (gap or overlap with previous block)`, t.term);
    }

    if (data.readUInt32LE(blockStart) !== BLOCK_MAGIC) {
      c.add("block-magic", blockStart, 4, `block #${b} has bad magic`, t.term);
      continue;
    }
    const hash = data.readUInt32LE(blockStart + 4);
    const firstDocId = data.readUInt32LE(blockStart + 8);
    const lastDocId = data.readUInt32LE(blockStart + 12);
    const count = data.readUInt32LE(blockStart + 16);
    const payloadLength = data.readUInt32LE(blockStart + 20);
    const payloadStart = blockStart + BLOCK_HEADER_SIZE;
    const payloadEnd = payloadStart + payloadLength;

    if (hash !== termHash(t.term)) {
      c.add("term-binding", blockStart + 4, 4,
        `block #${b} term hash 0x${hash.toString(16)} does not match dictionary term ${JSON.stringify(t.term)} (block moved from another term?)`, t.term);
    }
    if (payloadEnd < payloadStart || payloadEnd > termEnd || payloadEnd > postingsEnd) {
      c.add("block-out-of-bounds", blockStart, BLOCK_HEADER_SIZE,
        `block #${b} payload [${payloadStart}, ${payloadEnd}) overruns the term postings span`, t.term);
      continue;
    }
    if (lastDocId < firstDocId) {
      c.add("id-order", blockStart + 8, 8, `block #${b} lastDocID ${lastDocId} < firstDocID ${firstDocId}`, t.term);
    }
    if (j.maxDocId !== lastDocId) {
      c.add("jump-mismatch", j.entryOffset, 12,
        `jump table maxDocID ${j.maxDocId} != block #${b} lastDocID ${lastDocId}`, t.term);
    }
    if (j.count !== count) {
      c.add("jump-mismatch", j.entryOffset, 12,
        `jump table count ${j.count} != block #${b} count ${count}`, t.term);
    }
    if (prevBlockLast >= 0 && firstDocId <= prevBlockLast) {
      c.add("id-order", blockStart + 8, 4,
        `block #${b} firstDocID ${firstDocId} is not greater than previous block lastDocID ${prevBlockLast} (blocks swapped?)`, t.term);
    }

    // ---- decode payload: strictly increasing deltas, no u32 overflow ----
    let p = payloadStart;
    let prev = -1;
    let decoded = 0;
    let decodeFailed = false;
    for (let k = 0; k < count; k++) {
      if (p >= payloadEnd) {
        c.add("truncated", payloadStart, payloadLength,
          `block #${b} payload ended after ${k} of ${count} deltas`, t.term);
        decodeFailed = true;
        break;
      }
      let delta: number;
      try {
        const r = decodeVarint(data, p, payloadEnd);
        delta = r.value;
        p = r.next;
      } catch (e) {
        if (e instanceof FormatError) {
          c.add("varint-overflow", p, payloadEnd - p, `block #${b}: ${e.message}`, t.term);
          decodeFailed = true;
          break;
        }
        throw e;
      }
      if (k > 0 && delta === 0) {
        c.add("id-order", p - 1, 1, `block #${b} delta #${k} is 0: docIDs not strictly increasing`, t.term);
      }
      // First delta is the absolute first ID, later deltas are gaps.
      const docId = k === 0 ? delta : prev + delta;
      if (docId > MAX_DOC_ID) {
        c.add("id-overflow", p - 1, 1,
          `block #${b} docID #${k} exceeds max legal ID ${MAX_DOC_ID} (integer overflow)`, t.term);
        decodeFailed = true;
        break;
      }
      prev = docId;
      decoded++;
    }
    if (!decodeFailed) {
      if (p !== payloadEnd) {
        c.add("payload-size", p, payloadEnd - p,
          `block #${b} has ${payloadEnd - p} undecoded trailing payload bytes`, t.term);
      }
      // Cross-check decoded extremes against the block header.
      if (decoded === count && count > 0) {
        const first = decodeVarint(data, payloadStart, payloadEnd).value;
        if (first !== firstDocId) {
          c.add("header-mismatch", blockStart + 8, 4,
            `block #${b} header firstDocID ${firstDocId} != decoded first ID ${first}`, t.term);
        }
        if (prev !== lastDocId) {
          c.add("header-mismatch", blockStart + 12, 4,
            `block #${b} header lastDocID ${lastDocId} != decoded last ID ${prev}`, t.term);
        }
      }
    }
    prevBlockLast = lastDocId;
    expectedBlockStart = payloadEnd;
  }

  if (t.jump.length > 0 && expectedBlockStart !== termEnd) {
    c.add("span-size", expectedBlockStart, Math.abs(termEnd - expectedBlockStart),
      `blocks end at ${expectedBlockStart} but term postings span ends at ${termEnd}`, t.term);
  }
  if (countSum !== t.totalCount) {
    c.add("count-inconsistent", t.entryOffset, 8,
      `jump table counts sum to ${countSum} but totalCount is ${t.totalCount}`, t.term);
  }
}
