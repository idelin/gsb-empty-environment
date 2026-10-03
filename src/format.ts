import { sha256Hex, sha256Raw } from "./hash";

export const CHUNK_MAGIC = 0x43504b31; // "CPK1"
export const PAGE_HASH_BYTES = 32;

export interface PageInput {
  pageId: number;
  entries: Array<[string, string]>;
}

export interface ParsedPage {
  pageId: number;
  keyCount: number;
  hash: string;
  storedHash: string;
  entries: Array<[string, string]>;
}

export interface ParsedChunk {
  chunkIndex: number;
  pages: ParsedPage[];
  complete: boolean;
}

export interface SerializedChunk {
  buffer: Buffer;
  chunkHash: string;
  pageHashes: string[];
}

export function serializePagePayload(entries: Array<[string, string]>): Buffer {
  const parts: Buffer[] = [];
  for (const [key, value] of entries) {
    const keyBuf = Buffer.from(key, "utf8");
    const valBuf = Buffer.from(value, "utf8");
    const head = Buffer.alloc(8);
    head.writeUInt32LE(keyBuf.length, 0);
    head.writeUInt32LE(valBuf.length, 4);
    parts.push(head, keyBuf, valBuf);
  }
  return Buffer.concat(parts);
}

export function hashPage(pageId: number, payload: Buffer): Buffer {
  const idBuf = Buffer.alloc(4);
  idBuf.writeUInt32LE(pageId, 0);
  return sha256Raw(Buffer.concat([idBuf, payload]));
}

export function serializeChunk(chunkIndex: number, pages: PageInput[]): SerializedChunk {
  const pageHashes: string[] = [];
  const body: Buffer[] = [];
  for (const page of pages) {
    const payload = serializePagePayload(page.entries);
    const pageHash = hashPage(page.pageId, payload);
    pageHashes.push(pageHash.toString("hex"));
    const head = Buffer.alloc(12);
    head.writeUInt32LE(page.pageId, 0);
    head.writeUInt32LE(page.entries.length, 4);
    head.writeUInt32LE(payload.length, 8);
    body.push(head, pageHash, payload);
  }
  const header = Buffer.alloc(12);
  header.writeUInt32LE(CHUNK_MAGIC, 0);
  header.writeUInt32LE(chunkIndex, 4);
  header.writeUInt32LE(pages.length, 8);
  const buffer = Buffer.concat([header, ...body]);
  return { buffer, chunkHash: sha256Hex(buffer), pageHashes };
}

export function parseChunk(buffer: Buffer): ParsedChunk {
  const pages: ParsedPage[] = [];
  if (buffer.length < 12 || buffer.readUInt32LE(0) !== CHUNK_MAGIC) {
    return { chunkIndex: -1, pages, complete: false };
  }
  const chunkIndex = buffer.readUInt32LE(4);
  const pageCount = buffer.readUInt32LE(8);
  let offset = 12;
  let complete = true;
  for (let i = 0; i < pageCount; i++) {
    if (offset + 12 + PAGE_HASH_BYTES > buffer.length) {
      complete = false;
      break;
    }
    const pageId = buffer.readUInt32LE(offset);
    const keyCount = buffer.readUInt32LE(offset + 4);
    const payloadLen = buffer.readUInt32LE(offset + 8);
    const storedHash = buffer.slice(offset + 12, offset + 12 + PAGE_HASH_BYTES).toString("hex");
    const payloadStart = offset + 12 + PAGE_HASH_BYTES;
    if (payloadStart + payloadLen > buffer.length) {
      complete = false;
      break;
    }
    const payload = buffer.slice(payloadStart, payloadStart + payloadLen);
    const entries: Array<[string, string]> = [];
    let p = 0;
    let payloadOk = true;
    for (let k = 0; k < keyCount; k++) {
      if (p + 8 > payload.length) {
        payloadOk = false;
        break;
      }
      const keyLen = payload.readUInt32LE(p);
      const valLen = payload.readUInt32LE(p + 4);
      if (p + 8 + keyLen + valLen > payload.length) {
        payloadOk = false;
        break;
      }
      const key = payload.toString("utf8", p + 8, p + 8 + keyLen);
      const value = payload.toString("utf8", p + 8 + keyLen, p + 8 + keyLen + valLen);
      entries.push([key, value]);
      p += 8 + keyLen + valLen;
    }
    if (!payloadOk) {
      complete = false;
      break;
    }
    pages.push({ pageId, keyCount, hash: hashPage(pageId, payload).toString("hex"), storedHash, entries });
    offset = payloadStart + payloadLen;
  }
  if (offset !== buffer.length) {
    complete = false;
  }
  return { chunkIndex, pages, complete };
}
