/**
 * B+ 树与范围扫描（Range Scan）迭代器。
 *
 * - 内部节点只存路由键，记录全部存于叶子节点。
 * - 叶子节点通过 `next` 指针形成单向链表，范围扫描沿链表顺序前进。
 * - 迭代器任意时刻最多 Pin 住 1 个叶子页；跨越物理页边界时
 *   先 fetchPage（Pin）新页，再 unpinPage 旧页，不发生页面泄漏。
 * - 扫描区间为半开区间 [startKey, endKey)。
 */

import { BufferPool } from './buffer_pool.ts';

export const LEAF_CAPACITY = 64; // 叶子节点最大记录数
export const INTERNAL_CAPACITY = 64; // 内部节点最大键数

export interface LeafNode {
  isLeaf: true;
  keys: number[];
  values: number[];
  next: number; // 右侧兄弟叶子页 pageId，0 表示链表末尾
}

export interface InternalNode {
  isLeaf: false;
  keys: number[]; // n 个路由键
  children: number[]; // n + 1 个子页 pageId
}

export type BTreeNode = LeafNode | InternalNode;

export interface ScanEntry {
  key: number;
  value: number;
}

/** 第一个 >= key 的下标。 */
function lowerBound(keys: number[], key: number): number {
  let lo = 0;
  let hi = keys.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (keys[mid] < key) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

/** 第一个 > key 的下标。 */
function upperBound(keys: number[], key: number): number {
  let lo = 0;
  let hi = keys.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (keys[mid] <= key) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

export class RangeScanIterator implements IterableIterator<ScanEntry> {
  private readonly pool: BufferPool;
  private readonly endKey: number;
  private leafId = 0;
  private leaf: LeafNode | null = null;
  private slot = 0;
  private exhausted = false;

  constructor(
    pool: BufferPool,
    rootPageId: number,
    startKey: number,
    endKey: number,
  ) {
    this.pool = pool;
    this.endKey = endKey;
    if (startKey >= endKey) {
      this.exhausted = true;
      return;
    }
    // 自根向下定位起始叶子页；途经的内部页即用即放（unpin），
    // 最终只有目标叶子页保持 Pinned。
    let pageId = rootPageId;
    for (;;) {
      const node = this.pool.fetchPage<BTreeNode>(pageId);
      if (node.isLeaf) {
        this.leafId = pageId;
        this.leaf = node;
        this.slot = lowerBound(node.keys, startKey);
        break;
      }
      const childId = node.children[upperBound(node.keys, startKey)];
      this.pool.unpinPage(pageId);
      pageId = childId;
    }
    this.skipToValid();
  }

  /**
   * 让 (leaf, slot) 指向区间内下一条记录：
   * - slot 越过当前叶末尾时，沿 next 链表穿越页边界（Pin 新页 / Unpin 旧页）；
   * - 遇到 >= endKey 的键或链表末尾时结束迭代并释放 Pin。
   */
  private skipToValid(): void {
    while (this.leaf !== null && this.slot >= this.leaf.keys.length) {
      const nextId = this.leaf.next;
      this.pool.unpinPage(this.leafId);
      this.leaf = null;
      if (nextId === 0) {
        this.exhausted = true;
        return;
      }
      this.leafId = nextId;
      this.leaf = this.pool.fetchPage<LeafNode>(nextId);
      this.slot = 0;
    }
    if (this.leaf !== null && this.leaf.keys[this.slot] >= this.endKey) {
      this.release();
    }
  }

  next(): IteratorResult<ScanEntry, undefined> {
    if (this.exhausted || this.leaf === null) {
      return { value: undefined, done: true };
    }
    const entry: ScanEntry = {
      key: this.leaf.keys[this.slot],
      value: this.leaf.values[this.slot],
    };
    this.slot++;
    this.skipToValid();
    return { value: entry, done: false };
  }

  [Symbol.iterator](): IterableIterator<ScanEntry> {
    return this;
  }

  /** 提前终止（如 break）时调用，释放持有的 Pin；幂等。 */
  close(): void {
    this.release();
  }

  [Symbol.dispose](): void {
    this.release();
  }

  private release(): void {
    if (this.leaf !== null) {
      this.pool.unpinPage(this.leafId);
      this.leaf = null;
    }
    this.exhausted = true;
  }
}

export class BPlusTree {
  private readonly pool: BufferPool;
  private rootPageId: number;

  constructor(pool: BufferPool) {
    this.pool = pool;
    const { pageId } = pool.newPage<LeafNode>({
      isLeaf: true,
      keys: [],
      values: [],
      next: 0,
    });
    this.rootPageId = pageId;
    pool.unpinPage(pageId, true);
  }

  get rootId(): number {
    return this.rootPageId;
  }

  /** 开启范围扫描，返回 [startKey, endKey) 的有序迭代器。 */
  scan(startKey: number, endKey: number): RangeScanIterator {
    return new RangeScanIterator(this.pool, this.rootPageId, startKey, endKey);
  }

  /** 点查：沿树下行，途经页面即用即放。 */
  get(key: number): number | undefined {
    let pageId = this.rootPageId;
    for (;;) {
      const node = this.pool.fetchPage<BTreeNode>(pageId);
      if (node.isLeaf) {
        const i = lowerBound(node.keys, key);
        const hit = i < node.keys.length && node.keys[i] === key;
        const value = hit ? node.values[i] : undefined;
        this.pool.unpinPage(pageId);
        return value;
      }
      const childId = node.children[upperBound(node.keys, key)];
      this.pool.unpinPage(pageId);
      pageId = childId;
    }
  }

  insert(key: number, value: number): void {
    const path: number[] = []; // 下行路径上的内部页 pageId（不持有 Pin）
    let pageId = this.rootPageId;
    for (;;) {
      const node = this.pool.fetchPage<BTreeNode>(pageId);
      if (node.isLeaf) {
        const i = lowerBound(node.keys, key);
        node.keys.splice(i, 0, key);
        node.values.splice(i, 0, value);
        if (node.keys.length <= LEAF_CAPACITY) {
          this.pool.unpinPage(pageId, true);
        } else {
          this.splitLeaf(pageId, node, path); // 内部负责 unpin
        }
        return;
      }
      path.push(pageId);
      const childId = node.children[upperBound(node.keys, key)];
      this.pool.unpinPage(pageId);
      pageId = childId;
    }
  }

  /** 分裂已满叶子，维护叶子链表，然后把分隔键插入父节点。 */
  private splitLeaf(leafId: number, leaf: LeafNode, path: number[]): void {
    const mid = leaf.keys.length >>> 1;
    const rightKeys = leaf.keys.splice(mid);
    const rightValues = leaf.values.splice(mid);
    const { pageId: rightId, data: right } = this.pool.newPage<LeafNode>({
      isLeaf: true,
      keys: rightKeys,
      values: rightValues,
      next: leaf.next,
    });
    leaf.next = rightId;
    const separator = right.keys[0];
    this.pool.unpinPage(rightId, true);
    this.pool.unpinPage(leafId, true);
    this.insertIntoParent(leafId, separator, rightId, path);
  }

  private insertIntoParent(
    leftId: number,
    key: number,
    rightId: number,
    path: number[],
  ): void {
    if (path.length === 0) {
      const { pageId: newRootId, data: newRoot } =
        this.pool.newPage<InternalNode>({
          isLeaf: false,
          keys: [key],
          children: [leftId, rightId],
        });
      this.rootPageId = newRootId;
      this.pool.unpinPage(newRootId, true);
      return;
    }
    const parentId = path.pop()!;
    const parent = this.pool.fetchPage<InternalNode>(parentId);
    const i = upperBound(parent.keys, key);
    parent.keys.splice(i, 0, key);
    parent.children.splice(i + 1, 0, rightId);
    if (parent.keys.length <= INTERNAL_CAPACITY) {
      this.pool.unpinPage(parentId, true);
      return;
    }
    // 内部节点分裂：中间键上移，不保留在任何子节点中。
    const mid = parent.keys.length >>> 1;
    const upKey = parent.keys[mid];
    const rightKeys = parent.keys.splice(mid + 1);
    parent.keys.pop();
    const rightChildren = parent.children.splice(mid + 1);
    const { pageId: newRightId, data: newRight } =
      this.pool.newPage<InternalNode>({
        isLeaf: false,
        keys: rightKeys,
        children: rightChildren,
      });
    this.pool.unpinPage(newRightId, true);
    this.pool.unpinPage(parentId, true);
    this.insertIntoParent(parentId, upKey, newRightId, path);
  }
}
