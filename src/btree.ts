import { Pager, PAGE_SIZE } from './pager.ts';
import { OverflowChain } from './overflow.ts';
import {
  type DataRef,
  encodeRef,
  readRef,
  refSerializedSize,
  writeRef,
  parseRef,
} from './refs.ts';

const LEAF_TAG = 0x4c; // 'L'
const INTERNAL_TAG = 0x49; // 'I'
/** tag(1) + entryCount(2) + nextLeafPageId(4) */
const LEAF_HEADER_SIZE = 7;
/** tag(1) + keyCount(2) */
const INTERNAL_HEADER_SIZE = 3;

interface LeafEntry {
  keyRef: DataRef;
  valRef: DataRef;
}

interface Split {
  sep: Buffer;
  rightId: number;
}

export interface LeafEntryInfo {
  key: Buffer;
  keyInline: boolean;
  keyPrefix: Buffer;
  keyTotalLen: number;
  keyOverflowChain: number[];
  valueInline: boolean;
  valuePrefix: Buffer;
  valueTotalLen: number;
  valueOverflowChain: number[];
}

export interface BTreeOptions {
  pager?: Pager;
  /** Payloads larger than this (bytes) are moved to overflow pages. */
  maxInline?: number;
  /** Bytes of an overflowed payload kept in the leaf as a local prefix. */
  prefixLen?: number;
}

function toBuffer(data: Buffer | string): Buffer {
  return typeof data === 'string' ? Buffer.from(data, 'utf8') : data;
}

/**
 * B+ tree whose leaf/internal pages live in a Pager. Any key or value larger
 * than `maxInline` is truncated to a short prefix inside the node page; the
 * full payload is stored in a chained list of overflow pages and reassembled
 * transparently on read.
 */
export class BTree {
  private pager: Pager;
  private chain: OverflowChain;
  private maxInline: number;
  private prefixLen: number;
  private rootId: number;

  constructor(options: BTreeOptions = {}) {
    this.pager = options.pager ?? new Pager();
    this.chain = new OverflowChain(this.pager);
    this.maxInline = options.maxInline ?? Math.floor(PAGE_SIZE / 4);
    this.prefixLen = options.prefixLen ?? 128;
    this.rootId = this.pager.allocate();
    this.writeLeaf(this.rootId, [], 0);
  }

  // ---- public API -------------------------------------------------------

  insert(key: Buffer | string, value: Buffer | string): void {
    const split = this.insertRec(this.rootId, toBuffer(key), toBuffer(value));
    if (split) {
      const sepRef = encodeRef(this.chain, split.sep, this.maxInline, this.prefixLen);
      const newRoot = this.pager.allocate();
      this.writeInternal(newRoot, [sepRef], [this.rootId, split.rightId]);
      this.rootId = newRoot;
    }
  }

  get(key: Buffer | string): Buffer | null {
    const keyBuf = toBuffer(key);
    let pageId = this.rootId;
    for (;;) {
      const page = this.pager.read(pageId);
      const tag = page.readUInt8(0);
      if (tag === LEAF_TAG) {
        for (const entry of this.parseLeaf(page).entries) {
          if (readRef(this.chain, entry.keyRef).equals(keyBuf)) {
            return readRef(this.chain, entry.valRef);
          }
        }
        return null;
      }
      const { keys, children } = this.parseInternal(page);
      let idx = 0;
      while (idx < keys.length && readRef(this.chain, keys[idx]).compare(keyBuf) <= 0) idx++;
      pageId = children[idx];
    }
  }

  /** Debug/inspection view of how a key is physically stored in its leaf. */
  inspectEntry(key: Buffer | string): LeafEntryInfo | null {
    const keyBuf = toBuffer(key);
    let pageId = this.rootId;
    for (;;) {
      const page = this.pager.read(pageId);
      const tag = page.readUInt8(0);
      if (tag === LEAF_TAG) {
        for (const entry of this.parseLeaf(page).entries) {
          if (readRef(this.chain, entry.keyRef).equals(keyBuf)) {
            return {
              key: keyBuf,
              keyInline: entry.keyRef.inline,
              keyPrefix: Buffer.from(entry.keyRef.prefix),
              keyTotalLen: entry.keyRef.totalLen,
              keyOverflowChain: entry.keyRef.inline ? [] : this.chain.chainIds(entry.keyRef.overflowHead),
              valueInline: entry.valRef.inline,
              valuePrefix: Buffer.from(entry.valRef.prefix),
              valueTotalLen: entry.valRef.totalLen,
              valueOverflowChain: entry.valRef.inline ? [] : this.chain.chainIds(entry.valRef.overflowHead),
            };
          }
        }
        return null;
      }
      const { keys, children } = this.parseInternal(page);
      let idx = 0;
      while (idx < keys.length && readRef(this.chain, keys[idx]).compare(keyBuf) <= 0) idx++;
      pageId = children[idx];
    }
  }

  stats(): { pageCount: number; rootPageId: number; maxInline: number; prefixLen: number } {
    return {
      pageCount: this.pager.pageCount,
      rootPageId: this.rootId,
      maxInline: this.maxInline,
      prefixLen: this.prefixLen,
    };
  }

  // ---- insert internals -------------------------------------------------

  private insertRec(pageId: number, key: Buffer, value: Buffer): Split | null {
    const tag = this.pager.read(pageId).readUInt8(0);
    return tag === LEAF_TAG
      ? this.insertIntoLeaf(pageId, key, value)
      : this.insertIntoInternal(pageId, key, value);
  }

  private insertIntoLeaf(pageId: number, key: Buffer, value: Buffer): Split | null {
    const { entries, next } = this.parseLeaf(this.pager.read(pageId));
    let pos = 0;
    let found = false;
    while (pos < entries.length) {
      const cmp = readRef(this.chain, entries[pos].keyRef).compare(key);
      if (cmp === 0) { found = true; break; }
      if (cmp > 0) break;
      pos++;
    }
    const valRef = encodeRef(this.chain, value, this.maxInline, this.prefixLen);
    if (found) {
      const oldVal = entries[pos].valRef;
      if (!oldVal.inline) this.chain.free(oldVal.overflowHead);
      entries[pos] = { keyRef: entries[pos].keyRef, valRef };
    } else {
      const keyRef = encodeRef(this.chain, key, this.maxInline, this.prefixLen);
      entries.splice(pos, 0, { keyRef, valRef });
    }
    if (this.leafSize(entries) <= PAGE_SIZE) {
      this.writeLeaf(pageId, entries, next);
      return null;
    }
    const mid = entries.length >> 1;
    const left = entries.slice(0, mid);
    const right = entries.slice(mid);
    const rightId = this.pager.allocate();
    this.writeLeaf(rightId, right, next);
    this.writeLeaf(pageId, left, rightId);
    return { sep: readRef(this.chain, right[0].keyRef), rightId };
  }

  private insertIntoInternal(pageId: number, key: Buffer, value: Buffer): Split | null {
    const { keys, children } = this.parseInternal(this.pager.read(pageId));
    let idx = 0;
    while (idx < keys.length && readRef(this.chain, keys[idx]).compare(key) <= 0) idx++;
    const childSplit = this.insertRec(children[idx], key, value);
    if (!childSplit) return null;
    const sepRef = encodeRef(this.chain, childSplit.sep, this.maxInline, this.prefixLen);
    keys.splice(idx, 0, sepRef);
    children.splice(idx + 1, 0, childSplit.rightId);
    if (this.internalSize(keys, children) <= PAGE_SIZE) {
      this.writeInternal(pageId, keys, children);
      return null;
    }
    const mid = keys.length >> 1;
    const upKey = readRef(this.chain, keys[mid]);
    if (!keys[mid].inline) this.chain.free(keys[mid].overflowHead);
    const rightId = this.pager.allocate();
    this.writeInternal(rightId, keys.slice(mid + 1), children.slice(mid + 1));
    this.writeInternal(pageId, keys.slice(0, mid), children.slice(0, mid + 1));
    return { sep: upKey, rightId };
  }

  // ---- page (de)serialization -------------------------------------------

  private leafSize(entries: LeafEntry[]): number {
    let size = LEAF_HEADER_SIZE;
    for (const e of entries) size += refSerializedSize(e.keyRef) + refSerializedSize(e.valRef);
    return size;
  }

  private internalSize(keys: DataRef[], children: number[]): number {
    let size = INTERNAL_HEADER_SIZE + children.length * 4;
    for (const k of keys) size += refSerializedSize(k);
    return size;
  }

  private writeLeaf(id: number, entries: LeafEntry[], next: number): void {
    const buf = Buffer.alloc(PAGE_SIZE);
    buf.writeUInt8(LEAF_TAG, 0);
    buf.writeUInt16LE(entries.length, 1);
    buf.writeUInt32LE(next, 3);
    let offset = LEAF_HEADER_SIZE;
    for (const e of entries) {
      offset = writeRef(buf, offset, e.keyRef);
      offset = writeRef(buf, offset, e.valRef);
    }
    this.pager.write(id, buf);
  }

  private parseLeaf(page: Buffer): { entries: LeafEntry[]; next: number } {
    if (page.readUInt8(0) !== LEAF_TAG) throw new Error('btree: not a leaf page');
    const count = page.readUInt16LE(1);
    const next = page.readUInt32LE(3);
    const entries: LeafEntry[] = [];
    let offset = LEAF_HEADER_SIZE;
    for (let i = 0; i < count; i++) {
      const k = parseRef(page, offset);
      const v = parseRef(page, k.next);
      offset = v.next;
      entries.push({ keyRef: k.ref, valRef: v.ref });
    }
    return { entries, next };
  }

  private writeInternal(id: number, keys: DataRef[], children: number[]): void {
    const buf = Buffer.alloc(PAGE_SIZE);
    buf.writeUInt8(INTERNAL_TAG, 0);
    buf.writeUInt16LE(keys.length, 1);
    let offset = INTERNAL_HEADER_SIZE;
    for (const child of children) {
      buf.writeUInt32LE(child, offset);
      offset += 4;
    }
    for (const k of keys) offset = writeRef(buf, offset, k);
    this.pager.write(id, buf);
  }

  private parseInternal(page: Buffer): { keys: DataRef[]; children: number[] } {
    if (page.readUInt8(0) !== INTERNAL_TAG) throw new Error('btree: not an internal page');
    const count = page.readUInt16LE(1);
    const children: number[] = [];
    let offset = INTERNAL_HEADER_SIZE;
    for (let i = 0; i <= count; i++) {
      children.push(page.readUInt32LE(offset));
      offset += 4;
    }
    const keys: DataRef[] = [];
    for (let i = 0; i < count; i++) {
      const k = parseRef(page, offset);
      offset = k.next;
      keys.push(k.ref);
    }
    return { keys, children };
  }
}
