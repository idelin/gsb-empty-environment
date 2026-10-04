// Dictionary parser for segments that already passed validation.
// Shared by the query reader and by tests.

import { HEADER_SIZE } from "./format.ts";

export interface JumpEntry {
  offset: number; // absolute file offset of the block
  maxDocId: number;
  count: number;
}

export interface TermMeta {
  term: string;
  offset: number; // absolute file offset of the term's postings span
  length: number;
  totalCount: number;
  jump: JumpEntry[];
}

export function parseDictionary(data: Buffer): TermMeta[] {
  const termCount = data.readUInt32LE(8);
  const dictOffset = data.readUInt32LE(12);
  const postingsOffset = data.readUInt32LE(20);
  const metas: TermMeta[] = [];
  let pos = dictOffset;
  for (let i = 0; i < termCount; i++) {
    const termLen = data.readUInt16LE(pos); pos += 2;
    const term = data.toString("utf8", pos, pos + termLen); pos += termLen;
    const relOffset = data.readUInt32LE(pos); pos += 4;
    const length = data.readUInt32LE(pos); pos += 4;
    const totalCount = data.readUInt32LE(pos); pos += 4;
    const blockCount = data.readUInt32LE(pos); pos += 4;
    const jump: JumpEntry[] = [];
    for (let b = 0; b < blockCount; b++) {
      jump.push({
        offset: postingsOffset + relOffset + data.readUInt32LE(pos),
        maxDocId: data.readUInt32LE(pos + 4),
        count: data.readUInt32LE(pos + 8),
      });
      pos += 12;
    }
    metas.push({ term, offset: postingsOffset + relOffset, length, totalCount, jump });
  }
  void HEADER_SIZE;
  return metas;
}
