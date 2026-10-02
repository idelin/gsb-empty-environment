export interface TreeEntry<V> {
  key: number;
  value: V;
}

class RBNode<V> {
  key: number;
  value: V;
  red = true;
  left: RBNode<V> | null = null;
  right: RBNode<V> | null = null;
  parent: RBNode<V> | null = null;

  constructor(key: number, value: V) {
    this.key = key;
    this.value = value;
  }
}

export class RedBlackTree<V> {
  private root: RBNode<V> | null = null;
  private count = 0;

  get size(): number {
    return this.count;
  }

  insert(key: number, value: V): void {
    let parent: RBNode<V> | null = null;
    let current = this.root;
    while (current !== null) {
      parent = current;
      if (key === current.key) {
        current.value = value;
        return;
      }
      current = key < current.key ? current.left : current.right;
    }
    const node = new RBNode<V>(key, value);
    node.parent = parent;
    if (parent === null) {
      this.root = node;
    } else if (key < parent.key) {
      parent.left = node;
    } else {
      parent.right = node;
    }
    this.count += 1;
    this.fixInsert(node);
  }

  ceiling(key: number): TreeEntry<V> | null {
    let current = this.root;
    let best: RBNode<V> | null = null;
    while (current !== null) {
      if (current.key === key) {
        return { key: current.key, value: current.value };
      }
      if (current.key < key) {
        current = current.right;
      } else {
        best = current;
        current = current.left;
      }
    }
    return best === null ? null : { key: best.key, value: best.value };
  }

  minimum(): TreeEntry<V> | null {
    let current = this.root;
    if (current === null) return null;
    while (current.left !== null) current = current.left;
    return { key: current.key, value: current.value };
  }

  private fixInsert(start: RBNode<V>): void {
    let current = start;
    while (current.parent !== null && current.parent.red) {
      const parent = current.parent;
      const grand = parent.parent as RBNode<V>;
      if (parent === grand.left) {
        const uncle = grand.right;
        if (uncle !== null && uncle.red) {
          parent.red = false;
          uncle.red = false;
          grand.red = true;
          current = grand;
        } else {
          if (current === parent.right) {
            current = parent;
            this.rotateLeft(current);
          }
          (current.parent as RBNode<V>).red = false;
          ((current.parent as RBNode<V>).parent as RBNode<V>).red = true;
          this.rotateRight((current.parent as RBNode<V>).parent as RBNode<V>);
        }
      } else {
        const uncle = grand.left;
        if (uncle !== null && uncle.red) {
          parent.red = false;
          uncle.red = false;
          grand.red = true;
          current = grand;
        } else {
          if (current === parent.left) {
            current = parent;
            this.rotateRight(current);
          }
          (current.parent as RBNode<V>).red = false;
          ((current.parent as RBNode<V>).parent as RBNode<V>).red = true;
          this.rotateLeft((current.parent as RBNode<V>).parent as RBNode<V>);
        }
      }
    }
    (this.root as RBNode<V>).red = false;
  }

  private rotateLeft(node: RBNode<V>): void {
    const pivot = node.right as RBNode<V>;
    node.right = pivot.left;
    if (pivot.left !== null) pivot.left.parent = node;
    pivot.parent = node.parent;
    if (node.parent === null) {
      this.root = pivot;
    } else if (node === node.parent.left) {
      node.parent.left = pivot;
    } else {
      node.parent.right = pivot;
    }
    pivot.left = node;
    node.parent = pivot;
  }

  private rotateRight(node: RBNode<V>): void {
    const pivot = node.left as RBNode<V>;
    node.left = pivot.right;
    if (pivot.right !== null) pivot.right.parent = node;
    pivot.parent = node.parent;
    if (node.parent === null) {
      this.root = pivot;
    } else if (node === node.parent.right) {
      node.parent.right = pivot;
    } else {
      node.parent.left = pivot;
    }
    pivot.right = node;
    node.parent = pivot;
  }
}
