/**
 * 可控调度器：
 * - 同键请求合并为一个底层任务（共享字体/字形簇依赖）；
 * - 订阅者引用计数：取消一个订阅者不取消其他等待者；
 * - 手动 pump/flush/complete，测试可精确控制回包时机与顺序；
 * - 回包时校验代际，旧代际结果一律丢弃并向订阅者发 stale。
 */

export type TaskState = 'queued' | 'running' | 'done' | 'stale' | 'cancelled';

export type TaskEvent<T> =
  | { readonly type: 'resolved'; readonly value: T }
  | { readonly type: 'stale' }
  | { readonly type: 'cancelled' };

export type Subscriber<T> = (event: TaskEvent<T>) => void;

export interface Subscription {
  cancel(): void;
}

export class Task<T = unknown> {
  readonly key: string;
  readonly payload: unknown;
  readonly generation: number;
  state: TaskState = 'queued';
  readonly deps: Task[] = [];
  readonly dependents = new Set<Task>();
  readonly subscribers = new Set<Subscriber<T>>();
  /** 交付后的结果值，供依赖方执行器读取。 */
  value: unknown;
  pendingValue: unknown;
  hasPending = false;

  constructor(key: string, payload: unknown, generation: number) {
    this.key = key;
    this.payload = payload;
    this.generation = generation;
  }
}

export interface TaskHandle<T = unknown> {
  readonly task: Task<T>;
  subscribe(subscriber: Subscriber<T>): Subscription;
}

export type Executor = (task: Task) => unknown;

export class Scheduler {
  private readonly executor: Executor;
  private readonly generationOf: () => number;
  private readonly auto: boolean;
  private readonly active = new Map<string, Task>();
  private readonly readyQueue: Task[] = [];
  private readonly running: Task[] = [];
  private autoScheduled = false;
  /** 执行器调用次数，测试用来验证同键合并。 */
  executorCalls = 0;

  constructor(executor: Executor, generationOf: () => number, opts: { auto?: boolean } = {}) {
    this.executor = executor;
    this.generationOf = generationOf;
    this.auto = opts.auto ?? false;
  }

  /** 请求任务；同键且仍活跃的任务直接复用（同键合并）。 */
  request<T>(key: string, deps: TaskHandle[] = [], payload?: unknown): TaskHandle<T> {
    let task = this.active.get(key) as Task<T> | undefined;
    if (!task) {
      task = new Task<T>(key, payload, this.generationOf());
      task.deps.push(...deps.map((d) => d.task));
      for (const dep of task.deps) dep.dependents.add(task);
      this.active.set(key, task as Task);
      this.readyQueue.push(task as Task);
      if (this.auto) this.scheduleAuto();
    }
    const captured = task;
    return {
      task: captured,
      subscribe: (subscriber: Subscriber<T>): Subscription => {
        captured.subscribers.add(subscriber);
        let cancelled = false;
        return {
          cancel: () => {
            if (cancelled) return;
            cancelled = true;
            captured.subscribers.delete(subscriber);
            this.releaseIfOrphan(captured as Task);
          },
        };
      },
    };
  }

  /** 启动所有依赖已就绪的排队任务，返回启动数量。 */
  pump(): number {
    let started = 0;
    for (let i = 0; i < this.readyQueue.length; i++) {
      const task = this.readyQueue[i];
      if (task.state !== 'queued') continue;
      if (!task.deps.every((d) => d.state === 'done')) continue;
      task.state = 'running';
      this.readyQueue.splice(i, 1);
      i--;
      this.running.push(task);
      this.executorCalls++;
      const value = this.executor(task);
      task.pendingValue = value;
      task.hasPending = true;
      started++;
    }
    return started;
  }

  /** 交付所有待回包（按启动顺序），回包时校验代际。 */
  flush(): number {
    let delivered = 0;
    while (this.running.length > 0) {
      const task = this.running.shift()!;
      this.deliver(task, task.pendingValue);
      delivered++;
    }
    return delivered;
  }

  /** 交付指定任务的回包，可覆盖回包值（测试注入旧回包/乱序回包）。 */
  complete(key: string, value?: unknown): boolean {
    const index = this.running.findIndex((t) => t.key === key);
    if (index < 0) return false;
    const [task] = this.running.splice(index, 1);
    this.deliver(task, arguments.length >= 2 ? value : task.pendingValue);
    return true;
  }

  pendingKeys(): string[] {
    return this.running.map((t) => t.key);
  }

  queuedCount(): number {
    return this.readyQueue.filter((t) => t.state === 'queued').length;
  }

  runningCount(): number {
    return this.running.length;
  }

  private deliver(task: Task, value: unknown): void {
    if (task.state !== 'running') return;
    if (task.generation < this.generationOf()) {
      // 旧代际回包：丢弃，级联使依赖它的任务一并失效。
      this.markStale(task);
      return;
    }
    task.state = 'done';
    this.active.delete(task.key);
    task.value = value;
    for (const subscriber of [...task.subscribers]) {
      (subscriber as Subscriber<unknown>)({ type: 'resolved', value });
    }
    task.subscribers.clear();
    this.releaseDeps(task);
  }

  private markStale(task: Task): void {
    if (task.state === 'stale' || task.state === 'done') return;
    task.state = 'stale';
    this.active.delete(task.key);
    for (const subscriber of [...task.subscribers]) {
      (subscriber as Subscriber<unknown>)({ type: 'stale' });
    }
    task.subscribers.clear();
    this.releaseDeps(task);
    for (const dependent of [...task.dependents]) this.markStale(dependent);
    task.dependents.clear();
  }

  private releaseDeps(task: Task): void {
    for (const dep of task.deps) {
      dep.dependents.delete(task);
      this.releaseIfOrphan(dep);
    }
    task.deps.length = 0;
  }

  /** 无订阅者且无依赖者的任务可以取消；取消级联释放其依赖。 */
  private releaseIfOrphan(task: Task): void {
    if (task.subscribers.size > 0 || task.dependents.size > 0) return;
    if (task.state === 'done' || task.state === 'stale' || task.state === 'cancelled') return;
    task.state = 'cancelled';
    this.active.delete(task.key);
    const rq = this.readyQueue.indexOf(task);
    if (rq >= 0) this.readyQueue.splice(rq, 1);
    const rn = this.running.indexOf(task);
    if (rn >= 0) this.running.splice(rn, 1);
    for (const subscriber of [...task.subscribers]) {
      (subscriber as Subscriber<unknown>)({ type: 'cancelled' });
    }
    this.releaseDeps(task);
  }

  private scheduleAuto(): void {
    if (this.autoScheduled) return;
    this.autoScheduled = true;
    queueMicrotask(() => {
      this.autoScheduled = false;
      // 依赖链逐层就绪，循环驱动直到静默。
      for (;;) {
        const started = this.pump();
        const delivered = this.flush();
        if (started === 0 && delivered === 0) break;
      }
    });
  }
}
