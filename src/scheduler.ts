/**
 * 确定性事件调度器：最小堆，按 (tick, 插入序号) 全序调度。
 * 相同插入序列必然产生相同的弹出序列 —— 虚拟时间本身也是确定性的。
 */
export interface Occurrence {
  tick: number;
  order: number;
  source: 'arrival' | 'timer';
  /** timer 细分：ack（内部确认，不入日志）/ timeout（时钟中断，入日志） */
  kind: 'packet-arrival' | 'ack' | 'timeout';
  seq: number;
  size: number;
  attempt: number;
}

export class Scheduler {
  private heap: Occurrence[] = [];
  private counter = 0;

  get size(): number {
    return this.heap.length;
  }

  get insertions(): number {
    return this.counter;
  }

  insert(occ: Omit<Occurrence, 'order'>): void {
    const full: Occurrence = { ...occ, order: this.counter++ };
    this.heap.push(full);
    this.bubbleUp(this.heap.length - 1);
  }

  pop(): Occurrence | undefined {
    const top = this.heap[0];
    const last = this.heap.pop();
    if (this.heap.length > 0 && last !== undefined) {
      this.heap[0] = last;
      this.bubbleDown(0);
    }
    return top;
  }

  /** 队列内容纳入快照，保证“全部组件内存不变量”可对比 */
  dumpState(): unknown {
    return this.heap.map((o) => ({ ...o })).sort(compareOcc);
  }

  private bubbleUp(i: number): void {
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (compareOcc(this.heap[i], this.heap[parent]) < 0) {
        [this.heap[i], this.heap[parent]] = [this.heap[parent], this.heap[i]];
        i = parent;
      } else {
        return;
      }
    }
  }

  private bubbleDown(i: number): void {
    const n = this.heap.length;
    for (;;) {
      const l = 2 * i + 1;
      const r = l + 1;
      let smallest = i;
      if (l < n && compareOcc(this.heap[l], this.heap[smallest]) < 0) smallest = l;
      if (r < n && compareOcc(this.heap[r], this.heap[smallest]) < 0) smallest = r;
      if (smallest === i) return;
      [this.heap[i], this.heap[smallest]] = [this.heap[smallest], this.heap[i]];
      i = smallest;
    }
  }
}

function compareOcc(a: Occurrence, b: Occurrence): number {
  if (a.tick !== b.tick) return a.tick - b.tick;
  return a.order - b.order;
}
