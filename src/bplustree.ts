import { BufferPool } from "./buffer_pool.ts";
import {
  Page,
  type LeafNode,
  type InternalNode,
  INVALID_PAGE_ID,
} from "./page.ts";

// 单个叶子页 / 内部页的最大条目数（阶数）
export const LEAF_MAX = 64;
export const INTERNAL_MAX = 64;

interface SplitResult {
  promotedKey: number;
  rightPageId: number;
}

// 在已排序数组中找第一个 >= key 的下标
function lowerBound(arr: number[], key: number): number {
  let lo = 0;
  let hi = arr.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (arr[mid] < key) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  return lo;
}

export class BPlusTree {
  private pool: BufferPool;
  private rootPageId: number;

  constructor(pool: BufferPool) {
    this.pool = pool;
    const root = this.pool.newPage({
      isLeaf: true,
      keys: [],
      values: [],
      next: INVALID_PAGE_ID,
    });
    this.pool.unpinPage(root.pageId);
    this.rootPageId = root.pageId;
  }

  get rootId(): number {
    return this.rootPageId;
  }

  // 自根向下定位 key 所在的叶子页，返回已 Pin 的叶子 Page。
  // 调用方负责 unpinPage。
  findLeaf(key: number): Page {
    let page = this.pool.fetchPage(this.rootPageId);
    while (!page.node.isLeaf) {
      const node = page.node as InternalNode;
      // 第一个 > key 的键位即子指针下标
      let idx = lowerBound(node.keys, key);
      if (idx < node.keys.length && node.keys[idx] === key) {
        idx++;
      }
      const childId = node.children[idx];
      this.pool.unpinPage(page.pageId);
      page = this.pool.fetchPage(childId);
    }
    return page;
  }

  search(key: number): number | undefined {
    const leaf = this.findLeaf(key);
    try {
      const node = leaf.node as LeafNode;
      const idx = lowerBound(node.keys, key);
      if (idx < node.keys.length && node.keys[idx] === key) {
        return node.values[idx];
      }
      return undefined;
    } finally {
      this.pool.unpinPage(leaf.pageId);
    }
  }

  insert(key: number, value: number): void {
    const split = this.insertInto(this.rootPageId, key, value);
    if (split !== null) {
      const newRoot = this.pool.newPage({
        isLeaf: false,
        keys: [split.promotedKey],
        children: [this.rootPageId, split.rightPageId],
      });
      this.pool.unpinPage(newRoot.pageId);
      this.rootPageId = newRoot.pageId;
    }
  }

  private insertInto(
    pageId: number,
    key: number,
    value: number,
  ): SplitResult | null {
    const page = this.pool.fetchPage(pageId);
    try {
      if (page.node.isLeaf) {
        return this.insertIntoLeaf(page, key, value);
      }
      return this.insertIntoInternal(page, key, value);
    } finally {
      this.pool.unpinPage(pageId);
    }
  }

  private insertIntoLeaf(
    page: Page,
    key: number,
    value: number,
  ): SplitResult | null {
    const node = page.node as LeafNode;
    const idx = lowerBound(node.keys, key);
    if (idx < node.keys.length && node.keys[idx] === key) {
      throw new Error(`duplicate key ${key}`);
    }
    node.keys.splice(idx, 0, key);
    node.values.splice(idx, 0, value);
    page.dirty = true;
    if (node.keys.length <= LEAF_MAX) {
      return null;
    }
    // 叶子分裂：右半部分迁入新页，并维护叶子链表
    const mid = Math.ceil(node.keys.length / 2);
    const rightKeys = node.keys.splice(mid);
    const rightValues = node.values.splice(mid);
    const right = this.pool.newPage({
      isLeaf: true,
      keys: rightKeys,
      values: rightValues,
      next: node.next,
    });
    node.next = right.pageId;
    this.pool.unpinPage(right.pageId);
    return { promotedKey: rightKeys[0], rightPageId: right.pageId };
  }

  private insertIntoInternal(
    page: Page,
    key: number,
    value: number,
  ): SplitResult | null {
    const node = page.node as InternalNode;
    let idx = lowerBound(node.keys, key);
    if (idx < node.keys.length && node.keys[idx] === key) {
      idx++;
    }
    const childSplit = this.insertInto(node.children[idx], key, value);
    if (childSplit === null) {
      return null;
    }
    node.keys.splice(idx, 0, childSplit.promotedKey);
    node.children.splice(idx + 1, 0, childSplit.rightPageId);
    page.dirty = true;
    if (node.keys.length <= INTERNAL_MAX) {
      return null;
    }
    // 内部节点分裂：中间键上移，不保留在任一子节点中
    const mid = Math.floor(node.keys.length / 2);
    const promotedKey = node.keys[mid];
    const rightKeys = node.keys.splice(mid + 1);
    node.keys.pop(); // 移除上移键
    const rightChildren = node.children.splice(mid + 1);
    const right = this.pool.newPage({
      isLeaf: false,
      keys: rightKeys,
      children: rightChildren,
    });
    this.pool.unpinPage(right.pageId);
    return { promotedKey, rightPageId: right.pageId };
  }

  // 范围扫描：输出 [startKey, endKey) 区间内的全部 (key, value)，
  // 严格按键升序。返回的迭代器在叶子页边界处自动换页。
  scan(startKey: number, endKey: number): RangeScanIterator {
    return new RangeScanIterator(this.pool, this, startKey, endKey);
  }
}

export class RangeScanIterator implements IterableIterator<[number, number]> {
  private pool: BufferPool;
  private endKey: number;
  private leaf: Page | null = null;
  private slot = 0;
  private finished = false;

  constructor(
    pool: BufferPool,
    tree: BPlusTree,
    startKey: number,
    endKey: number,
  ) {
    this.pool = pool;
    this.endKey = endKey;
    if (startKey >= endKey) {
      this.finished = true;
      return;
    }
    // 定位起始 Key 所在叶子页及槽位（叶子页保持 Pin 状态）
    this.leaf = tree.findLeaf(startKey);
    const node = this.leaf.node as LeafNode;
    this.slot = lowerBound(node.keys, startKey);
    this.skipToValidSlot();
  }

  // 槽位越界时沿叶子链表前进：Pin 新页、Unpin 旧页
  private skipToValidSlot(): void {
    while (this.leaf !== null) {
      const node = this.leaf.node as LeafNode;
      if (this.slot < node.keys.length) {
        if (node.keys[this.slot] >= this.endKey) {
          this.close();
        }
        return;
      }
      // 跨越物理页边界
      const nextId = node.next;
      this.pool.unpinPage(this.leaf.pageId);
      if (nextId === INVALID_PAGE_ID) {
        this.leaf = null;
        this.finished = true;
        return;
      }
      this.leaf = this.pool.fetchPage(nextId);
      this.slot = 0;
    }
  }

  next(): IteratorResult<[number, number]> {
    if (this.finished || this.leaf === null) {
      return { value: undefined, done: true };
    }
    const node = this.leaf.node as LeafNode;
    const pair: [number, number] = [
      node.keys[this.slot],
      node.values[this.slot],
    ];
    this.slot++;
    this.skipToValidSlot();
    return { value: pair, done: false };
  }

  // 提前终止（如 for...of 中 break）时释放持有的 Pin
  return(): IteratorResult<[number, number]> {
    this.close();
    return { value: undefined, done: true };
  }

  close(): void {
    if (this.leaf !== null) {
      this.pool.unpinPage(this.leaf.pageId);
      this.leaf = null;
    }
    this.finished = true;
  }

  [Symbol.iterator](): IterableIterator<[number, number]> {
    return this;
  }
}
