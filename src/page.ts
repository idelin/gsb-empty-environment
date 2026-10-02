// B+ 树节点在页（Page）中的数据布局。
// 每个 Page 对应 BufferPool 中的一个物理页框，pageId 即页号。

export interface LeafNode {
  isLeaf: true;
  keys: number[];
  values: number[];
  // 叶子链表：指向右侧兄弟叶子的 pageId，-1 表示链表末尾
  next: number;
}

export interface InternalNode {
  isLeaf: false;
  keys: number[];
  // children.length === keys.length + 1
  children: number[];
}

export type NodeData = LeafNode | InternalNode;

export const INVALID_PAGE_ID = -1;

export class Page {
  readonly pageId: number;
  node: NodeData;
  dirty: boolean;

  constructor(pageId: number, node: NodeData) {
    this.pageId = pageId;
    this.node = node;
    this.dirty = false;
  }
}
