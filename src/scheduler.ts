export type Priority = number;

export interface SpawnOptions {
  name: string;
  priority: Priority;
}

export type CoroutineBody = (ctx: CoroutineContext) => Generator<unknown, void, void>;

export interface CoroutineContext {
  readonly name: string;
  readonly priority: Priority;
  spawn(options: SpawnOptions, body: CoroutineBody): void;
}

export type PreemptReason = "higher-priority-ready" | "timeslice-exhausted";

export type SchedulerEvent =
  | { type: "dispatch"; name: string; priority: Priority }
  | { type: "step"; name: string; priority: Priority; step: number }
  | { type: "yield"; name: string; priority: Priority }
  | { type: "preempt"; name: string; priority: Priority; reason: PreemptReason }
  | { type: "complete"; name: string; priority: Priority };

export interface PoolOptions {
  /** 时间片配额：单个协程在一次调度回合内允许连续执行的最大步数 */
  timeSlice?: number;
  onEvent?: (event: SchedulerEvent) => void;
}

export interface CoroutineStats {
  name: string;
  priority: Priority;
  totalSteps: number;
  done: boolean;
}

interface Coroutine {
  id: number;
  name: string;
  priority: Priority;
  gen: Generator<unknown, void, void>;
  done: boolean;
  totalSteps: number;
}

const DEFAULT_TIME_SLICE = 4;

/**
 * 多优先级协程池：
 * - 严格优先级调度：每次协同出让点之后，CPU 交给就绪队列中优先级最高的协程；
 * - 时间片轮转：同优先级协程按 FIFO 轮转，单个协程连续执行超过 timeSlice 步后强制换出；
 * - 配额监控：低优先级协程每执行一步都会检查是否有更高优先级协程就绪，
 *   一旦发现立即强制协同切换，防止优先级反转、保证系统活性。
 */
export class CoroutinePool {
  private readonly timeSlice: number;
  private readonly onEvent?: (event: SchedulerEvent) => void;
  private readonly ready: Coroutine[] = [];
  private readonly all: Coroutine[] = [];
  private nextId = 1;

  constructor(options: PoolOptions = {}) {
    if (options.timeSlice !== undefined && (!Number.isInteger(options.timeSlice) || options.timeSlice < 1)) {
      throw new RangeError("timeSlice must be a positive integer");
    }
    this.timeSlice = options.timeSlice ?? DEFAULT_TIME_SLICE;
    this.onEvent = options.onEvent;
  }

  spawn(options: SpawnOptions, body: CoroutineBody): void {
    const ctx: CoroutineContext = {
      name: options.name,
      priority: options.priority,
      spawn: (spawnOptions, spawnBody) => this.spawn(spawnOptions, spawnBody),
    };
    const coroutine: Coroutine = {
      id: this.nextId++,
      name: options.name,
      priority: options.priority,
      gen: body(ctx),
      done: false,
      totalSteps: 0,
    };
    this.ready.push(coroutine);
    this.all.push(coroutine);
  }

  get pending(): number {
    return this.ready.length;
  }

  stats(): CoroutineStats[] {
    return this.all.map((co) => ({
      name: co.name,
      priority: co.priority,
      totalSteps: co.totalSteps,
      done: co.done,
    }));
  }

  /** 运行调度器直到所有协程执行完毕（协同式，单线程）。 */
  run(): void {
    while (this.ready.length > 0) {
      const coroutine = this.pickNext();
      this.emit({ type: "dispatch", name: coroutine.name, priority: coroutine.priority });

      let sliceSteps = 0;
      while (sliceSteps < this.timeSlice && !coroutine.done) {
        const result = coroutine.gen.next();
        coroutine.totalSteps += 1;
        sliceSteps += 1;
        this.emit({
          type: "step",
          name: coroutine.name,
          priority: coroutine.priority,
          step: coroutine.totalSteps,
        });

        if (result.done) {
          coroutine.done = true;
          this.emit({ type: "complete", name: coroutine.name, priority: coroutine.priority });
          break;
        }

        this.emit({ type: "yield", name: coroutine.name, priority: coroutine.priority });

        // 配额监控：低优先级协程每占用一步都重新检查就绪队列，
        // 一旦出现更高优先级协程，立即强制协同切换。
        if (this.highestReadyPriority() > coroutine.priority) {
          this.emit({
            type: "preempt",
            name: coroutine.name,
            priority: coroutine.priority,
            reason: "higher-priority-ready",
          });
          break;
        }
      }

      if (!coroutine.done && sliceSteps >= this.timeSlice) {
        this.emit({
          type: "preempt",
          name: coroutine.name,
          priority: coroutine.priority,
          reason: "timeslice-exhausted",
        });
      }

      if (!coroutine.done) {
        // 时间片轮转：换出的协程回到就绪队列尾部。
        this.ready.push(coroutine);
      }
    }
  }

  /** 取出就绪队列中优先级最高的协程；同优先级按 FIFO（轮转）顺序。 */
  private pickNext(): Coroutine {
    let bestIndex = 0;
    for (let i = 1; i < this.ready.length; i += 1) {
      if (this.ready[i].priority > this.ready[bestIndex].priority) {
        bestIndex = i;
      }
    }
    const [coroutine] = this.ready.splice(bestIndex, 1);
    return coroutine;
  }

  private highestReadyPriority(): Priority {
    let highest = Number.NEGATIVE_INFINITY;
    for (const coroutine of this.ready) {
      if (coroutine.priority > highest) {
        highest = coroutine.priority;
      }
    }
    return highest;
  }

  private emit(event: SchedulerEvent): void {
    this.onEvent?.(event);
  }
}
