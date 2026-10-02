/**
 * B+ 树 + 螃蟹脚并发加锁协议（Latch Crabbing / Coupling）。
 *
 * 协议要点：
 * - 遍历自上而下加锁，父节点锁在子节点锁到手之后才释放（coupling）；
 * - 读操作：对路径逐节点加读锁，子锁到手立即释放父锁 —— 读者在高层
 *   节点几乎不停留，多个读者可共享同一节点的读锁，无阻塞高速穿越；
 * - 写操作（插入）：对路径加写锁，若子节点"安全"（keys 未满，插入不会
 *   引发分裂/借调），则提前释放所有祖先锁，只保留不安全区段的锁；
 * - 根节点特殊处理：加锁后复核 root 身份（防止并发根分裂导致用到过期
 *   根），根满则在写锁下分裂使树长高，然后重启遍历。
 *
 * 死锁自由论证：锁只按"根 -> 叶"的偏序获取，任何时刻一个操作持有的锁
 * 构成树中的一条向下路径，不存在循环等待，因此无死锁。
 */

import { RwLatch } from "./latch.ts";

class LeafNode<V> {
  readonly isLeaf = true as const;
  readonly latch = new RwLatch();
  keys: number[] = [];
  values: V[] = [];
}

class InternalNode<V> {
  readonly isLeaf = false as const;
  readonly latch = new RwLatch();
  keys: number[] = [];
  children: BNode<V>[] = [];
}

type BNode<V> = LeafNode<V> | InternalNode<V>;

/** 第一个大于 key 的位置（B+ 树内部节点路由：keys[i] 是 children[i+1] 的最小键） */
function childIndex<V>(node: InternalNode<V>, key: number): number {
  let i = 0;
  while (i < node.keys.length && node.keys[i] <= key) i++;
  return i;
}

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

function splitLeaf<V>(leaf: LeafNode<V>): { sep: number; right: LeafNode<V> } {
  const mid = Math.ceil(leaf.keys.length / 2);
  const right = new LeafNode<V>();
  right.keys = leaf.keys.slice(mid);
  right.values = leaf.values.slice(mid);
  leaf.keys.length = mid;
  leaf.values.length = mid;
  return { sep: right.keys[0], right }; // B+ 树：sep 复制上移
}

function splitInternal<V>(node: InternalNode<V>): { sep: number; right: InternalNode<V> } {
  const mid = Math.floor(node.keys.length / 2);
  const sep = node.keys[mid]; // 内部节点：sep 上移且不保留
  const right = new InternalNode<V>();
  right.keys = node.keys.slice(mid + 1);
  right.children = node.children.slice(mid + 1);
  node.keys.length = mid;
  node.children.length = mid + 1;
  return { sep, right };
}

function splitNode<V>(node: BNode<V>): { sep: number; right: BNode<V> } {
  return node.isLeaf ? splitLeaf(node) : splitInternal(node);
}

function insertChild<V>(parent: InternalNode<V>, sep: number, right: BNode<V>): void {
  const i = lowerBound(parent.keys, sep);
  parent.keys.splice(i, 0, sep);
  parent.children.splice(i + 1, 0, right);
}

export class BPlusTree<V> {
  private root: BNode<V> = new LeafNode<V>();
  /** 每个节点允许的最大键数 = order - 1 */
  readonly maxKeys: number;

  constructor(order = 8) {
    if (order < 3) throw new Error("order must be >= 3");
    this.maxKeys = order - 1;
  }

  /**
   * 读操作（螃蟹脚）：逐节点 read-latch，子锁到手立即释放父锁。
   * 任一时刻最多持有路径上相邻两把读锁，高层节点几乎零占用。
   */
  async get(key: number): Promise<V | undefined> {
    for (;;) {
      const root = this.root;
      await root.latch.acquireRead();
      if (root !== this.root) {
        // 加锁期间根被并发分裂替换，重启以新根为入口
        root.latch.releaseRead();
        continue;
      }
      let current: BNode<V> = root;
      try {
        while (!current.isLeaf) {
          const child = current.children[childIndex(current, key)];
          await child.latch.acquireRead(); // 先拿子锁
          current.latch.releaseRead();     // 再放父锁（crabbing）
          current = child;
        }
        const i = lowerBound(current.keys, key);
        if (i < current.keys.length && current.keys[i] === key) {
          return current.values[i];
        }
        return undefined;
      } finally {
        current.latch.releaseRead();
      }
    }
  }

  async has(key: number): Promise<boolean> {
    return (await this.get(key)) !== undefined;
  }

  /**
   * 写操作（螃蟹脚）：路径加写锁，遇到"安全"子节点（keys 未满，插入
   * 不会分裂）即提前释放全部祖先锁；分裂只会在仍持有锁的不安全区段
   * 内向上传播，且区段顶端必为安全节点，传播必然有界。
   */
  async insert(key: number, value: V): Promise<void> {
    for (;;) {
      const root = this.root;
      await root.latch.acquireWrite();
      if (root !== this.root) {
        root.latch.releaseWrite();
        continue; // 过期根，重启
      }
      if (root.keys.length >= this.maxKeys) {
        // 根满：在写锁独占下分裂根，树长高一层，然后重启
        const { sep, right } = splitNode(root);
        const newRoot = new InternalNode<V>();
        newRoot.keys = [sep];
        newRoot.children = [root, right];
        this.root = newRoot;
        root.latch.releaseWrite();
        continue;
      }
      // held：当前持有的写锁链（自顶向下）。held[0] 必为安全节点。
      let held: BNode<V>[] = [root];
      try {
        let current: BNode<V> = root;
        while (!current.isLeaf) {
          const child = current.children[childIndex(current, key)];
          await child.latch.acquireWrite();
          if (child.keys.length < this.maxKeys) {
            // 子节点安全：提前释放所有祖先锁，提高并发吞吐
            for (const n of held) n.latch.releaseWrite();
            held = [];
          }
          held.push(child);
          current = child;
        }
        // 叶子内 upsert
        const i = lowerBound(current.keys, key);
        if (i < current.keys.length && current.keys[i] === key) {
          current.values[i] = value;
        } else {
          current.keys.splice(i, 0, key);
          current.values.splice(i, 0, value);
        }
        // 溢出则分裂，沿仍持有锁的链向上传播（顶不到 held[0] 之外）
        let level = held.length - 1;
        while (held[level].keys.length > this.maxKeys) {
          const { sep, right } = splitNode(held[level]);
          insertChild(held[level - 1] as InternalNode<V>, sep, right);
          level--;
        }
        return;
      } finally {
        for (const n of held) n.latch.releaseWrite();
      }
    }
  }

  /** 测试/演示用：静止状态下中序收集全部键值对（不加锁）。 */
  collect(): Array<[number, V]> {
    const out: Array<[number, V]> = [];
    const walk = (n: BNode<V>): void => {
      if (n.isLeaf) {
        for (let i = 0; i < n.keys.length; i++) out.push([n.keys[i], n.values[i]]);
      } else {
        for (const c of n.children) walk(c);
      }
    };
    walk(this.root);
    return out;
  }

  /** 当前树高（演示用，静止状态下调用）。 */
  height(): number {
    let h = 1;
    let n: BNode<V> = this.root;
    while (!n.isLeaf) {
      n = n.children[0];
      h++;
    }
    return h;
  }
}
