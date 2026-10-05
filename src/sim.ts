interface SimEvent {
  time: number;
  order: number;
  fn: () => void;
}

function less(x: SimEvent, y: SimEvent): boolean {
  return x.time < y.time || (x.time === y.time && x.order < y.order);
}

// 虚拟时间离散事件模拟器（二叉堆事件队列）。
export class Sim {
  now = 0;
  private heap: SimEvent[] = [];
  private counter = 0;

  schedule(delay: number, fn: () => void): void {
    const ev: SimEvent = { time: this.now + delay, order: this.counter++, fn };
    const h = this.heap;
    h.push(ev);
    let i = h.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (less(h[i], h[p])) {
        [h[p], h[i]] = [h[i], h[p]];
        i = p;
      } else break;
    }
  }

  // 运行直到 done() 为真（返回 true）或超过 tMax / 事件耗尽（返回 false）。
  runUntil(done: () => boolean, tMax: number): boolean {
    while (this.heap.length > 0) {
      if (done()) return true;
      const ev = this.pop();
      if (ev.time > tMax) return false;
      this.now = ev.time;
      ev.fn();
    }
    return done();
  }

  private pop(): SimEvent {
    const h = this.heap;
    const top = h[0];
    const last = h.pop()!;
    if (h.length > 0) {
      h[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let m = i;
        if (l < h.length && less(h[l], h[m])) m = l;
        if (r < h.length && less(h[r], h[m])) m = r;
        if (m === i) break;
        [h[m], h[i]] = [h[i], h[m]];
        i = m;
      }
    }
    return top;
  }
}
