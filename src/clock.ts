/**
 * 可注入时钟：测试用 ManualClock 虚拟时间，生产用 SystemClock 真实时间。
 * 所有租约 TTL 逻辑都基于 Clock，保证测试确定性与速度。
 */

export type TimerHandle = unknown;

export interface Clock {
  now(): number;
  setTimeout(callback: () => void, delayMs: number): TimerHandle;
  clearTimeout(handle: TimerHandle): void;
}

export class SystemClock implements Clock {
  now(): number {
    return Date.now();
  }
  setTimeout(callback: () => void, delayMs: number): TimerHandle {
    return setTimeout(callback, delayMs);
  }
  clearTimeout(handle: TimerHandle): void {
    clearTimeout(handle as Parameters<typeof clearTimeout>[0]);
  }
}

interface ScheduledTask {
  id: number;
  runAt: number;
  callback: () => void;
  cancelled: boolean;
}

/** 虚拟时钟：最小堆调度，advance() 按 (runAt, id) 顺序触发到期回调。 */
export class ManualClock implements Clock {
  private currentTime = 0;
  private nextId = 1;
  private heap: ScheduledTask[] = [];
  private byId = new Map<number, ScheduledTask>();

  now(): number {
    return this.currentTime;
  }

  setTimeout(callback: () => void, delayMs: number): TimerHandle {
    const task: ScheduledTask = {
      id: this.nextId++,
      runAt: this.currentTime + Math.max(0, delayMs),
      callback,
      cancelled: false,
    };
    this.heap.push(task);
    this.bubbleUp(this.heap.length - 1);
    this.byId.set(task.id, task);
    return task.id;
  }

  clearTimeout(handle: TimerHandle): void {
    // 惰性删除：仅打标记，弹到堆顶时跳过
    const task = this.byId.get(handle as number);
    if (task) task.cancelled = true;
  }

  /** 推进虚拟时间，按时间顺序执行所有到期任务。 */
  advance(ms: number): void {
    const target = this.currentTime + ms;
    while (this.heap.length > 0) {
      const top = this.heap[0];
      if (top.cancelled) {
        this.popTop();
        continue;
      }
      if (top.runAt > target) break;
      this.popTop();
      this.byId.delete(top.id);
      this.currentTime = top.runAt;
      top.callback();
    }
    this.currentTime = target;
  }

  get pendingCount(): number {
    let count = 0;
    for (const task of this.heap) {
      if (!task.cancelled) count++;
    }
    return count;
  }

  private static comesBefore(a: ScheduledTask, b: ScheduledTask): boolean {
    return a.runAt < b.runAt || (a.runAt === b.runAt && a.id < b.id);
  }

  private bubbleUp(index: number): void {
    while (index > 0) {
      const parent = (index - 1) >> 1;
      if (!ManualClock.comesBefore(this.heap[index], this.heap[parent])) break;
      const tmp = this.heap[index];
      this.heap[index] = this.heap[parent];
      this.heap[parent] = tmp;
      index = parent;
    }
  }

  private popTop(): void {
    const last = this.heap.pop()!;
    if (this.heap.length === 0) return;
    this.heap[0] = last;
    let index = 0;
    for (;;) {
      const left = index * 2 + 1;
      const right = left + 1;
      let smallest = index;
      if (left < this.heap.length && ManualClock.comesBefore(this.heap[left], this.heap[smallest])) {
        smallest = left;
      }
      if (right < this.heap.length && ManualClock.comesBefore(this.heap[right], this.heap[smallest])) {
        smallest = right;
      }
      if (smallest === index) break;
      const tmp = this.heap[index];
      this.heap[index] = this.heap[smallest];
      this.heap[smallest] = tmp;
      index = smallest;
    }
  }
}
