export interface HeapEntry {
  level: number;
  dist: number;
  tag: number;
  idx: number;
}

function less(a: HeapEntry, b: HeapEntry): boolean {
  if (a.level !== b.level) return a.level < b.level;
  if (a.dist !== b.dist) return a.dist < b.dist;
  if (a.tag !== b.tag) return a.tag < b.tag;
  return a.idx < b.idx;
}

/** Binary min-heap ordered lexicographically by (level, dist, tag, idx). */
export class MinHeap {
  private a: HeapEntry[] = [];

  get size(): number {
    return this.a.length;
  }

  push(e: HeapEntry): void {
    const arr = this.a;
    arr.push(e);
    let i = arr.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (less(arr[i], arr[parent])) {
        const tmp = arr[i];
        arr[i] = arr[parent];
        arr[parent] = tmp;
        i = parent;
      } else {
        break;
      }
    }
  }

  pop(): HeapEntry | undefined {
    const arr = this.a;
    const n = arr.length;
    if (n === 0) return undefined;
    const top = arr[0];
    const last = arr.pop() as HeapEntry;
    if (n > 1) {
      arr[0] = last;
      let i = 0;
      for (;;) {
        const l = i * 2 + 1;
        const r = l + 1;
        let smallest = i;
        if (l < arr.length && less(arr[l], arr[smallest])) smallest = l;
        if (r < arr.length && less(arr[r], arr[smallest])) smallest = r;
        if (smallest === i) break;
        const tmp = arr[i];
        arr[i] = arr[smallest];
        arr[smallest] = tmp;
        i = smallest;
      }
    }
    return top;
  }
}
