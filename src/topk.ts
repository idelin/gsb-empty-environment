/** Fixed-size min-heap selecting the K largest weights in O(n log k). */

export interface ScoredCell {
  cell: number;
  weight: number;
}

export function topK(sums: Float64Array, k: number): ScoredCell[] {
  if (k <= 0) return [];
  const heapCell = new Int32Array(k);
  const heapWeight = new Float64Array(k);
  let size = 0;

  const swap = (a: number, b: number): void => {
    const tc = heapCell[a];
    heapCell[a] = heapCell[b];
    heapCell[b] = tc;
    const tw = heapWeight[a];
    heapWeight[a] = heapWeight[b];
    heapWeight[b] = tw;
  };

  const siftUp = (i: number): void => {
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (heapWeight[parent] <= heapWeight[i]) break;
      swap(parent, i);
      i = parent;
    }
  };

  const siftDown = (i: number): void => {
    for (;;) {
      const left = 2 * i + 1;
      const right = left + 1;
      let smallest = i;
      if (left < size && heapWeight[left] < heapWeight[smallest]) smallest = left;
      if (right < size && heapWeight[right] < heapWeight[smallest]) smallest = right;
      if (smallest === i) break;
      swap(i, smallest);
      i = smallest;
    }
  };

  for (let cell = 0; cell < sums.length; cell++) {
    const w = sums[cell];
    if (w <= 0) continue;
    if (size < k) {
      heapCell[size] = cell;
      heapWeight[size] = w;
      siftUp(size);
      size++;
    } else if (w > heapWeight[0]) {
      heapCell[0] = cell;
      heapWeight[0] = w;
      siftDown(0);
    }
  }

  const result: ScoredCell[] = [];
  for (let i = 0; i < size; i++) {
    result.push({ cell: heapCell[i], weight: heapWeight[i] });
  }
  result.sort((a, b) => b.weight - a.weight || a.cell - b.cell);
  return result;
}
