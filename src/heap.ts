// 二叉最小堆，元素为整数 id，比较器决定优先级
export interface CompareFn {
  (a: number, b: number): number;
}

export interface Heap {
  size(): number;
  push(id: number): void;
  pop(): number | undefined;
}

export function createHeap(cmp: CompareFn): Heap {
  const items: number[] = [];

  function swap(i: number, j: number): void {
    const tmp = items[i];
    items[i] = items[j];
    items[j] = tmp;
  }

  function push(id: number): void {
    items.push(id);
    let idx = items.length - 1;
    while (idx > 0) {
      const parent = (idx - 1) >> 1;
      if (cmp(items[parent], items[idx]) <= 0) break;
      swap(parent, idx);
      idx = parent;
    }
  }

  function pop(): number | undefined {
    if (items.length === 0) return undefined;
    const top = items[0];
    const last = items.pop();
    if (items.length > 0 && last !== undefined) {
      items[0] = last;
      let idx = 0;
      for (;;) {
        const left = idx * 2 + 1;
        const right = left + 1;
        let smallest = idx;
        if (left < items.length && cmp(items[left], items[smallest]) < 0) smallest = left;
        if (right < items.length && cmp(items[right], items[smallest]) < 0) smallest = right;
        if (smallest === idx) break;
        swap(smallest, idx);
        idx = smallest;
      }
    }
    return top;
  }

  function size(): number {
    return items.length;
  }

  return { size, push, pop };
}
