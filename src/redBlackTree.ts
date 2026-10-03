/**
 * 红黑树（Red-Black Tree）—— 一致性哈希环的有序索引结构。
 * 键为环上的哈希位置（uint32），值为该位置对应的物理节点 ID。
 * 提供 insert / delete / ceiling / min，路由查找复杂度 O(log n)。
 */

type Color = "RED" | "BLACK";

class RBNode<K, V> {
  key: K;
  value: V;
  color: Color = "RED";
  left: RBNode<K, V> | null = null;
  right: RBNode<K, V> | null = null;
  parent: RBNode<K, V> | null = null;

  constructor(key: K, value: V) {
    this.key = key;
    this.value = value;
  }
}

export class RedBlackTree<K, V> {
  private root: RBNode<K, V> | null = null;
  private readonly compare: (a: K, b: K) => number;
  private size_ = 0;

  constructor(compare: (a: K, b: K) => number) {
    this.compare = compare;
  }

  get size(): number {
    return this.size_;
  }

  has(key: K): boolean {
    return this.findNode(key) !== null;
  }

  get(key: K): V | undefined {
    const node = this.findNode(key);
    return node === null ? undefined : node.value;
  }

  private findNode(key: K): RBNode<K, V> | null {
    let cur = this.root;
    while (cur !== null) {
      const cmp = this.compare(key, cur.key);
      if (cmp === 0) return cur;
      cur = cmp < 0 ? cur.left : cur.right;
    }
    return null;
  }

  /** 环上顺时针查找：返回 >= key 的最小条目；不存在时返回 null（由调用方回绕到 min）。 */
  ceiling(key: K): { key: K; value: V } | null {
    let cur = this.root;
    let best: RBNode<K, V> | null = null;
    while (cur !== null) {
      const cmp = this.compare(key, cur.key);
      if (cmp === 0) return { key: cur.key, value: cur.value };
      if (cmp < 0) {
        best = cur;
        cur = cur.left;
      } else {
        cur = cur.right;
      }
    }
    return best === null ? null : { key: best.key, value: best.value };
  }

  min(): { key: K; value: V } | null {
    let cur = this.root;
    if (cur === null) return null;
    while (cur.left !== null) cur = cur.left;
    return { key: cur.key, value: cur.value };
  }

  insert(key: K, value: V): void {
    let parent: RBNode<K, V> | null = null;
    let cur = this.root;
    while (cur !== null) {
      const cmp = this.compare(key, cur.key);
      if (cmp === 0) {
        cur.value = value;
        return;
      }
      parent = cur;
      cur = cmp < 0 ? cur.left : cur.right;
    }
    const node = new RBNode<K, V>(key, value);
    node.parent = parent;
    if (parent === null) {
      this.root = node;
    } else if (this.compare(key, parent.key) < 0) {
      parent.left = node;
    } else {
      parent.right = node;
    }
    this.size_++;
    this.fixInsert(node);
  }

  delete(key: K): boolean {
    const node = this.findNode(key);
    if (node === null) return false;
    this.deleteNode(node);
    this.size_--;
    return true;
  }

  /** 中序遍历（升序），用于校验与调试。 */
  inOrder(): Array<{ key: K; value: V }> {
    const out: Array<{ key: K; value: V }> = [];
    const walk = (n: RBNode<K, V> | null): void => {
      if (n === null) return;
      walk(n.left);
      out.push({ key: n.key, value: n.value });
      walk(n.right);
    };
    walk(this.root);
    return out;
  }

  private rotateLeft(x: RBNode<K, V>): void {
    const y = x.right!;
    x.right = y.left;
    if (y.left !== null) y.left.parent = x;
    y.parent = x.parent;
    if (x.parent === null) this.root = y;
    else if (x === x.parent.left) x.parent.left = y;
    else x.parent.right = y;
    y.left = x;
    x.parent = y;
  }

  private rotateRight(x: RBNode<K, V>): void {
    const y = x.left!;
    x.left = y.right;
    if (y.right !== null) y.right.parent = x;
    y.parent = x.parent;
    if (x.parent === null) this.root = y;
    else if (x === x.parent.right) x.parent.right = y;
    else x.parent.left = y;
    y.right = x;
    x.parent = y;
  }

  private fixInsert(z: RBNode<K, V>): void {
    while (z.parent !== null && z.parent.color === "RED") {
      const parent = z.parent;
      const grand = parent.parent!;
      if (parent === grand.left) {
        const uncle = grand.right;
        if (uncle !== null && uncle.color === "RED") {
          parent.color = "BLACK";
          uncle.color = "BLACK";
          grand.color = "RED";
          z = grand;
        } else {
          if (z === parent.right) {
            z = parent;
            this.rotateLeft(z);
          }
          z.parent!.color = "BLACK";
          z.parent!.parent!.color = "RED";
          this.rotateRight(z.parent!.parent!);
        }
      } else {
        const uncle = grand.left;
        if (uncle !== null && uncle.color === "RED") {
          parent.color = "BLACK";
          uncle.color = "BLACK";
          grand.color = "RED";
          z = grand;
        } else {
          if (z === parent.left) {
            z = parent;
            this.rotateRight(z);
          }
          z.parent!.color = "BLACK";
          z.parent!.parent!.color = "RED";
          this.rotateLeft(z.parent!.parent!);
        }
      }
    }
    this.root!.color = "BLACK";
  }

  private transplant(u: RBNode<K, V>, v: RBNode<K, V> | null): void {
    if (u.parent === null) this.root = v;
    else if (u === u.parent.left) u.parent.left = v;
    else u.parent.right = v;
    if (v !== null) v.parent = u.parent;
  }

  private minimum(n: RBNode<K, V>): RBNode<K, V> {
    while (n.left !== null) n = n.left;
    return n;
  }

  private deleteNode(z: RBNode<K, V>): void {
    let y = z;
    let yOriginalColor: Color = y.color;
    let x: RBNode<K, V> | null;
    let xParent: RBNode<K, V> | null;
    if (z.left === null) {
      x = z.right;
      xParent = z.parent;
      this.transplant(z, z.right);
    } else if (z.right === null) {
      x = z.left;
      xParent = z.parent;
      this.transplant(z, z.left);
    } else {
      y = this.minimum(z.right);
      yOriginalColor = y.color;
      x = y.right;
      if (y.parent === z) {
        xParent = y;
        if (x !== null) x.parent = y;
      } else {
        xParent = y.parent;
        this.transplant(y, y.right);
        y.right = z.right;
        y.right!.parent = y;
      }
      this.transplant(z, y);
      y.left = z.left;
      y.left!.parent = y;
      y.color = z.color;
    }
    if (yOriginalColor === "BLACK") this.fixDelete(x, xParent);
  }

  private fixDelete(x: RBNode<K, V> | null, parent: RBNode<K, V> | null): void {
    while (x !== this.root && (x === null || x.color === "BLACK")) {
      if (parent === null) break;
      if (x === parent.left) {
        let w = parent.right;
        if (w !== null && w.color === "RED") {
          w.color = "BLACK";
          parent.color = "RED";
          this.rotateLeft(parent);
          w = parent.right;
        }
        if (
          w === null ||
          ((w.left === null || w.left.color === "BLACK") &&
            (w.right === null || w.right.color === "BLACK"))
        ) {
          if (w !== null) w.color = "RED";
          x = parent;
          parent = x.parent;
        } else {
          if (w.right === null || w.right.color === "BLACK") {
            if (w.left !== null) w.left.color = "BLACK";
            w.color = "RED";
            this.rotateRight(w);
            w = parent.right!;
          }
          w.color = parent.color;
          parent.color = "BLACK";
          if (w.right !== null) w.right.color = "BLACK";
          this.rotateLeft(parent);
          x = this.root;
          parent = null;
        }
      } else {
        let w = parent.left;
        if (w !== null && w.color === "RED") {
          w.color = "BLACK";
          parent.color = "RED";
          this.rotateRight(parent);
          w = parent.left;
        }
        if (
          w === null ||
          ((w.right === null || w.right.color === "BLACK") &&
            (w.left === null || w.left.color === "BLACK"))
        ) {
          if (w !== null) w.color = "RED";
          x = parent;
          parent = x.parent;
        } else {
          if (w.left === null || w.left.color === "BLACK") {
            if (w.right !== null) w.right.color = "BLACK";
            w.color = "RED";
            this.rotateLeft(w);
            w = parent.left!;
          }
          w.color = parent.color;
          parent.color = "BLACK";
          if (w.left !== null) w.left.color = "BLACK";
          this.rotateRight(parent);
          x = this.root;
          parent = null;
        }
      }
    }
    if (x !== null) x.color = "BLACK";
  }
}
