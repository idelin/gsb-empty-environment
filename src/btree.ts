import { OverflowChain } from './overflow.ts';
import { decodeField, encodeField } from './field.ts';
import { INVALID_PAGE_ID, PAGE_SIZE, PageType, Pager } from './pager.ts';

/**
 * B+ tree whose nodes are fixed-size pages. Variable-length keys and values
 * larger than the inline threshold are truncated into overflow page chains;
 * the tree page keeps only a prefix and the chain head pointer.
 *
 * Leaf page:     [type:u8][nextLeaf:u32le][entryCount:u16le] (keyField valueField)*
 * Internal page: [type:u8][keyCount:u16le] child0:u32le (keyField child:u32le)*
 *
 * Encoded field bytes are cached on the in-memory entries so rewriting a page
 * never re-encodes (and never re-allocates) an existing overflow chain.
 */

const LEAF_HEADER_SIZE = 7;
const INTERNAL_HEADER_SIZE = 3;

interface LeafEntry {
  key: Buffer;
  value: Buffer;
  keyBytes: Buffer;
  valueBytes: Buffer;
}

interface LeafNode {
  next: number;
  entries: LeafEntry[];
}

interface Separator {
  key: Buffer;
  bytes: Buffer;
}

interface InternalNode {
  keys: Separator[];
  children: number[];
}

export interface EntryDescription {
  found: boolean;
  overflow: boolean;
  totalLength: number;
  prefix: Buffer | null;
  firstOverflowPageId: number | null;
  overflowChainPageIds: number[];
}

export class BPlusTree {
  readonly pager = new Pager();
  readonly chain = new OverflowChain(this.pager);
  private rootId: number;

  constructor() {
    this.rootId = this.pager.allocate();
    this.writeLeaf(this.rootId, { next: INVALID_PAGE_ID, entries: [] });
  }

  insert(key: Buffer, value: Buffer): void {
    const path = this.findPath(key);
    const leafId = path[path.length - 1].pageId;
    const leaf = this.readLeaf(leafId);
    const dup = leaf.entries.findIndex((e) => e.key.equals(key));
    if (dup >= 0) leaf.entries.splice(dup, 1);
    const entry: LeafEntry = {
      key,
      value,
      keyBytes: encodeField(this.chain, key).bytes,
      valueBytes: encodeField(this.chain, value).bytes,
    };
    const pos = leaf.entries.findIndex((e) => Buffer.compare(e.key, key) > 0);
    leaf.entries.splice(pos === -1 ? leaf.entries.length : pos, 0, entry);
    if (this.leafSize(leaf) <= PAGE_SIZE) {
      this.writeLeaf(leafId, leaf);
      return;
    }
    const mid = Math.ceil(leaf.entries.length / 2);
    const right: LeafNode = { next: leaf.next, entries: leaf.entries.splice(mid) };
    const rightId = this.pager.allocate();
    this.writeLeaf(rightId, right);
    leaf.next = rightId;
    this.writeLeaf(leafId, leaf);
    const firstRight = right.entries[0];
    this.insertIntoParent(path, { key: firstRight.key, bytes: firstRight.keyBytes }, rightId);
  }

  get(key: Buffer): Buffer | null {
    const leaf = this.readLeaf(this.findLeafId(key));
    const entry = leaf.entries.find((e) => e.key.equals(key));
    return entry ? entry.value : null;
  }

  /** Inspect how a key's value is physically stored (inline vs overflow chain). */
  describeEntry(key: Buffer): EntryDescription {
    const leafId = this.findLeafId(key);
    const raw = this.pager.read(leafId);
    const count = raw.readUInt16LE(5);
    let offset = LEAF_HEADER_SIZE;
    for (let i = 0; i < count; i++) {
      const keyField = decodeField(this.chain, raw, offset);
      offset += keyField.bytesRead;
      const valueField = decodeField(this.chain, raw, offset);
      if (keyField.data.equals(key)) {
        return {
          found: true,
          overflow: valueField.overflow,
          totalLength: valueField.totalLength,
          prefix: valueField.prefix,
          firstOverflowPageId: valueField.firstOverflowPageId,
          overflowChainPageIds:
            valueField.firstOverflowPageId === null
              ? []
              : this.chain.pageIdsOf(valueField.firstOverflowPageId),
        };
      }
      offset += valueField.bytesRead;
    }
    return {
      found: false,
      overflow: false,
      totalLength: 0,
      prefix: null,
      firstOverflowPageId: null,
      overflowChainPageIds: [],
    };
  }

  stats(): { pageCount: number; overflowPageCount: number } {
    let overflowPageCount = 0;
    for (let i = 0; i < this.pager.pageCount; i++) {
      if (this.pager.read(i).readUInt8(0) === PageType.Overflow) overflowPageCount++;
    }
    return { pageCount: this.pager.pageCount, overflowPageCount };
  }

  private findLeafId(key: Buffer): number {
    const path = this.findPath(key);
    return path[path.length - 1].pageId;
  }

  private findPath(key: Buffer): { pageId: number; childIndex: number }[] {
    const path: { pageId: number; childIndex: number }[] = [];
    let pageId = this.rootId;
    for (;;) {
      const page = this.pager.read(pageId);
      if (page.readUInt8(0) === PageType.Leaf) {
        path.push({ pageId, childIndex: -1 });
        return path;
      }
      const node = this.readInternal(pageId);
      let childIndex = 0;
      while (childIndex < node.keys.length && Buffer.compare(node.keys[childIndex].key, key) <= 0) {
        childIndex++;
      }
      path.push({ pageId, childIndex });
      pageId = node.children[childIndex];
    }
  }

  private insertIntoParent(
    path: { pageId: number; childIndex: number }[],
    separator: Separator,
    rightId: number,
  ): void {
    if (path.length === 1) {
      const rootId = this.pager.allocate();
      this.writeInternal(rootId, { keys: [separator], children: [path[0].pageId, rightId] });
      this.rootId = rootId;
      return;
    }
    path.pop();
    const parentId = path[path.length - 1].pageId;
    const parent = this.readInternal(parentId);
    const insertAt = path[path.length - 1].childIndex + 1;
    parent.keys.splice(insertAt - 1, 0, separator);
    parent.children.splice(insertAt, 0, rightId);
    if (this.internalSize(parent) <= PAGE_SIZE) {
      this.writeInternal(parentId, parent);
      return;
    }
    const mid = Math.floor(parent.keys.length / 2);
    const promoted = parent.keys[mid];
    const right: InternalNode = {
      keys: parent.keys.splice(mid + 1),
      children: parent.children.splice(mid + 1),
    };
    parent.keys.pop();
    const rightInternalId = this.pager.allocate();
    this.writeInternal(rightInternalId, right);
    this.writeInternal(parentId, parent);
    this.insertIntoParent(path, promoted, rightInternalId);
  }

  private leafSize(leaf: LeafNode): number {
    let size = LEAF_HEADER_SIZE;
    for (const entry of leaf.entries) size += entry.keyBytes.length + entry.valueBytes.length;
    return size;
  }

  private internalSize(node: InternalNode): number {
    let size = INTERNAL_HEADER_SIZE + 4 * node.children.length;
    for (const key of node.keys) size += key.bytes.length;
    return size;
  }

  private readLeaf(pageId: number): LeafNode {
    const raw = this.pager.read(pageId);
    if (raw.readUInt8(0) !== PageType.Leaf) throw new Error(`page ${pageId} is not a leaf`);
    const count = raw.readUInt16LE(5);
    const entries: LeafEntry[] = [];
    let offset = LEAF_HEADER_SIZE;
    for (let i = 0; i < count; i++) {
      const key = decodeField(this.chain, raw, offset);
      const keyBytes = Buffer.from(raw.subarray(offset, offset + key.bytesRead));
      offset += key.bytesRead;
      const value = decodeField(this.chain, raw, offset);
      const valueBytes = Buffer.from(raw.subarray(offset, offset + value.bytesRead));
      offset += value.bytesRead;
      entries.push({ key: key.data, value: value.data, keyBytes, valueBytes });
    }
    return { next: raw.readUInt32LE(1), entries };
  }

  private writeLeaf(pageId: number, leaf: LeafNode): void {
    const raw = Buffer.alloc(PAGE_SIZE);
    raw.writeUInt8(PageType.Leaf, 0);
    raw.writeUInt32LE(leaf.next, 1);
    raw.writeUInt16LE(leaf.entries.length, 5);
    let offset = LEAF_HEADER_SIZE;
    for (const entry of leaf.entries) {
      entry.keyBytes.copy(raw, offset);
      offset += entry.keyBytes.length;
      entry.valueBytes.copy(raw, offset);
      offset += entry.valueBytes.length;
    }
    if (offset > PAGE_SIZE) throw new Error(`leaf page ${pageId} overflowed during write`);
    this.pager.write(pageId, raw);
  }

  private readInternal(pageId: number): InternalNode {
    const raw = this.pager.read(pageId);
    if (raw.readUInt8(0) !== PageType.Internal) throw new Error(`page ${pageId} is not internal`);
    const keyCount = raw.readUInt16LE(1);
    const keys: Separator[] = [];
    const children: number[] = [];
    let offset = INTERNAL_HEADER_SIZE;
    children.push(raw.readUInt32LE(offset));
    offset += 4;
    for (let i = 0; i < keyCount; i++) {
      const key = decodeField(this.chain, raw, offset);
      keys.push({ key: key.data, bytes: Buffer.from(raw.subarray(offset, offset + key.bytesRead)) });
      offset += key.bytesRead;
      children.push(raw.readUInt32LE(offset));
      offset += 4;
    }
    return { keys, children };
  }

  private writeInternal(pageId: number, node: InternalNode): void {
    const raw = Buffer.alloc(PAGE_SIZE);
    raw.writeUInt8(PageType.Internal, 0);
    raw.writeUInt16LE(node.keys.length, 1);
    let offset = INTERNAL_HEADER_SIZE;
    raw.writeUInt32LE(node.children[0], offset);
    offset += 4;
    for (let i = 0; i < node.keys.length; i++) {
      node.keys[i].bytes.copy(raw, offset);
      offset += node.keys[i].bytes.length;
      raw.writeUInt32LE(node.children[i + 1], offset);
      offset += 4;
    }
    if (offset > PAGE_SIZE) throw new Error(`internal page ${pageId} overflowed during write`);
    this.pager.write(pageId, raw);
  }
}
