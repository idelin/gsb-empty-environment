/**
 * 离散事件模拟器与虚拟网络。
 *
 * - Simulator：虚拟时钟（毫秒）+ 二叉堆事件队列，发送/定时器都走事件队列。
 * - Network：为每条消息安排一次"投递"事件，并按节点统计出/入包数。
 *
 * 使用虚拟时间而非真实定时器，1000 节点 × 数秒模拟在毫秒内跑完且结果确定。
 */

export interface Endpoint {
  readonly id: number;
  readonly alive: boolean;
  receive(msg: unknown): void;
}

interface SimEvent {
  time: number;
  seq: number;
  fn: () => void;
}

export class Simulator {
  now = 0;
  private heap: SimEvent[] = [];
  private seq = 0;

  get pending(): number {
    return this.heap.length;
  }

  schedule(delayMs: number, fn: () => void): void {
    const event: SimEvent = { time: this.now + delayMs, seq: this.seq++, fn };
    const heap = this.heap;
    heap.push(event);
    let i = heap.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      const p = heap[parent];
      if (p.time < event.time || (p.time === event.time && p.seq < event.seq)) break;
      heap[i] = p;
      i = parent;
    }
    heap[i] = event;
  }

  private pop(): SimEvent {
    const heap = this.heap;
    const top = heap[0];
    const last = heap.pop() as SimEvent;
    if (heap.length > 0) {
      let i = 0;
      for (;;) {
        const left = i * 2 + 1;
        const right = left + 1;
        let smallest = i;
        if (
          left < heap.length &&
          (heap[left].time < last.time ||
            (heap[left].time === last.time && heap[left].seq < last.seq))
        ) {
          smallest = left;
        }
        if (
          right < heap.length &&
          (heap[right].time < heap[smallest].time ||
            (heap[right].time === heap[smallest].time &&
              heap[right].seq < heap[smallest].seq))
        ) {
          smallest = right;
        }
        if (smallest === i) break;
        heap[i] = heap[smallest];
        i = smallest;
      }
      heap[i] = last;
    }
    return top;
  }

  runUntil(endMs: number): void {
    while (this.heap.length > 0 && this.heap[0].time <= endMs) {
      const event = this.pop();
      this.now = event.time;
      event.fn();
    }
    this.now = endMs;
  }
}

export class Network {
  totalOut = 0;
  totalIn = 0;
  readonly outByNode: number[];
  readonly inByNode: number[];
  /** false 时只统计包量（收=发，立即入账），不调度投递事件——用于大规模基线吞吐测量。 */
  readonly deliver: boolean;
  private sim: Simulator;
  private latencyMs: () => number;
  private bucketMs: number;
  private buckets = new Map<number, { tos: Endpoint[]; msgs: unknown[] }>();
  private lastSlot = -1;
  private lastBucket: { tos: Endpoint[]; msgs: unknown[] } | undefined;

  constructor(
    sim: Simulator,
    nodeCount: number,
    latencyMs: () => number,
    bucketMs = 5,
    deliver = true,
  ) {
    this.sim = sim;
    this.latencyMs = latencyMs;
    this.bucketMs = bucketMs;
    this.deliver = deliver;
    this.outByNode = new Array(nodeCount).fill(0);
    this.inByNode = new Array(nodeCount).fill(0);
  }

  resetCounters(): void {
    this.totalOut = 0;
    this.totalIn = 0;
    this.outByNode.fill(0);
    this.inByNode.fill(0);
  }

  send(from: Endpoint, to: Endpoint, msg: unknown): void {
    if (!from.alive) return;
    this.totalOut++;
    this.outByNode[from.id]++;
    if (!this.deliver) {
      if (to.alive) {
        this.totalIn++;
        this.inByNode[to.id]++;
      }
      return;
    }
    // 投递按 bucketMs 时间窗聚合：同一窗口内的所有投递共享一个调度事件，
    // 既保留了网络延迟语义，又把事件队列规模降低约两个数量级。
    const slot = Math.floor((this.sim.now + this.latencyMs()) / this.bucketMs);
    let bucket = slot === this.lastSlot ? this.lastBucket : this.buckets.get(slot);
    if (!bucket) {
      const fresh = { tos: [] as Endpoint[], msgs: [] as unknown[] };
      bucket = fresh;
      this.buckets.set(slot, fresh);
      const delay = Math.max(0, (slot + 1) * this.bucketMs - this.sim.now);
      this.sim.schedule(delay, () => {
        this.buckets.delete(slot);
        if (this.lastSlot === slot) {
          this.lastSlot = -1;
          this.lastBucket = undefined;
        }
        for (let i = 0; i < fresh.tos.length; i++) {
          const target = fresh.tos[i];
          if (!target.alive) continue; // 对端已崩溃：包发出去了，但不会被接收
          this.totalIn++;
          this.inByNode[target.id]++;
          target.receive(fresh.msgs[i]);
        }
      });
    }
    bucket.tos.push(to);
    bucket.msgs.push(msg);
    this.lastSlot = slot;
    this.lastBucket = bucket;
  }
}
