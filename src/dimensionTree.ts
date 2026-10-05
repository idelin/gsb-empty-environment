export interface DimNode {
  label: string;
  level: number;
  parent: DimNode | null;
  children: Map<string, DimNode>;
  recordCount: number;
  insertionOrder: number;
}

function createNode(label: string, level: number, parent: DimNode | null, insertionOrder: number): DimNode {
  return {
    label,
    level,
    parent,
    children: new Map<string, DimNode>(),
    recordCount: 0,
    insertionOrder,
  };
}

export class DimensionTree {
  readonly root: DimNode;
  private counter = 0;

  constructor() {
    this.root = createNode('', -1, null, 0);
  }

  addPath(labels: string[]): DimNode[] {
    const nodes: DimNode[] = [this.root];
    let current = this.root;
    this.root.recordCount += 1;
    for (const label of labels) {
      let child = current.children.get(label);
      if (!child) {
        this.counter += 1;
        child = createNode(label, current.level + 1, current, this.counter);
        current.children.set(label, child);
      }
      child.recordCount += 1;
      nodes.push(child);
      current = child;
    }
    return nodes;
  }

  lookupPath(labels: string[]): DimNode[] {
    const nodes: DimNode[] = [this.root];
    let current = this.root;
    for (const label of labels) {
      const child = current.children.get(label);
      if (!child) {
        return nodes;
      }
      nodes.push(child);
      current = child;
    }
    return nodes;
  }

  removePath(labels: string[]): void {
    const chain: DimNode[] = [this.root];
    let current = this.root;
    this.root.recordCount -= 1;
    for (const label of labels) {
      const child = current.children.get(label);
      if (!child) {
        break;
      }
      child.recordCount -= 1;
      chain.push(child);
      current = child;
    }
    for (let i = chain.length - 1; i >= 1; i -= 1) {
      const node = chain[i];
      if (node.recordCount <= 0 && node.parent) {
        node.parent.children.delete(node.label);
      }
    }
  }

  clear(): void {
    this.root.children.clear();
    this.root.recordCount = 0;
    this.counter = 0;
  }

  size(): number {
    let total = 0;
    const walk = (node: DimNode): void => {
      total += 1;
      node.children.forEach(walk);
    };
    walk(this.root);
    return total;
  }
}
