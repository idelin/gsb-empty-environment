/**
 * 多优先级协程池：
 *  - 优先级抢占：高优先级协程在低优先级协程主动 Yield 时插队；
 *  - 时间片轮转：同优先级协程按时间配额（执行步数）轮转；
 *  - 配额监控：低优先级协程占满配额时强制协同切换，保护系统活性。
 *
 * 优先级数值越大越紧急。协程体为生成器，每次 yield 记为一个执行步，
 * 并产生一条进入执行轨迹（trace）的标签。
 */

export type CoroutineBody = () => Generator<string, void, void>;

export interface CoroutineStat {
  id: string;
  priority: number;
  /** 累计执行步数 */
  steps: number;
  /** 被高优先级抢占次数 */
  preemptions: number;
  /** 时间配额耗尽被强制轮转次数 */
  rotations: number;
  done: boolean;
}

interface Task {
  id: string;
  priority: number;
  gen: Generator<string, void, void>;
  done: boolean;
  steps: number;
  stepsInSlice: number;
  preemptions: number;
  rotations: number;
}

export interface PoolOptions {
  /** 时间片配额：单个协程允许连续执行的最大步数，默认 3 */
  timeSlice?: number;
}

export class CoroutinePool {
  private readonly timeSlice: number;
  /** 就绪队列：按优先级降序，同优先级先进先出 */
  private readonly readyQueue: Task[] = [];
  private readonly tasks = new Map<string, Task>();
  /** 协程 yield 出的执行轨迹 */
  readonly trace: string[] = [];
  /** 调度器决策事件（抢占 / 轮转 / 完成） */
  readonly events: string[] = [];

  constructor(options: PoolOptions = {}) {
    this.timeSlice = options.timeSlice ?? 3;
    if (this.timeSlice < 1) throw new RangeError("timeSlice must be >= 1");
  }

  /** 注册协程；运行中调用即可让新协程进入就绪态 */
  spawn(id: string, priority: number, body: CoroutineBody): void {
    if (this.tasks.has(id)) throw new Error(`duplicate coroutine id: ${id}`);
    const task: Task = {
      id,
      priority,
      gen: body(),
      done: false,
      steps: 0,
      stepsInSlice: 0,
      preemptions: 0,
      rotations: 0,
    };
    this.tasks.set(id, task);
    this.enqueue(task, false);
  }

  /** 插入就绪队列：toFront 置于同优先级组头部（被抢占者），否则置于组尾 */
  private enqueue(task: Task, toFront: boolean): void {
    const index = this.readyQueue.findIndex((t) =>
      toFront ? t.priority <= task.priority : t.priority < task.priority,
    );
    if (index === -1) this.readyQueue.push(task);
    else this.readyQueue.splice(index, 0, task);
  }

  /** 运行直至所有协程结束，返回执行轨迹 */
  run(): string[] {
    while (this.readyQueue.length > 0) {
      const task = this.readyQueue.shift()!;
      task.stepsInSlice = 0;

      while (!task.done) {
        const result = task.gen.next();
        if (result.done) {
          task.done = true;
          this.events.push(`done:${task.id}`);
          break;
        }

        task.steps += 1;
        task.stepsInSlice += 1;
        this.trace.push(result.value);

        // 1) 优先级抢占：存在更高优先级就绪协程时，当前协程在本步出让后立即让位
        if (this.readyQueue.some((t) => t.priority > task.priority)) {
          task.preemptions += 1;
          this.events.push(`preempt:${task.id}`);
          this.enqueue(task, true);
          break;
        }

        // 2) 时间配额监控：占满配额且存在同优先级就绪协程时，强制协同切换
        if (
          task.stepsInSlice >= this.timeSlice &&
          this.readyQueue.some((t) => t.priority === task.priority)
        ) {
          task.rotations += 1;
          this.events.push(`rotate:${task.id}`);
          this.enqueue(task, false);
          break;
        }
        // 3) 否则继续执行当前协程（高优先级协程可连续运行至完成，杜绝优先级反转）
      }
    }
    return this.trace;
  }

  /** 配额与调度统计，用于监控 */
  stats(): CoroutineStat[] {
    return [...this.tasks.values()].map((t) => ({
      id: t.id,
      priority: t.priority,
      steps: t.steps,
      preemptions: t.preemptions,
      rotations: t.rotations,
      done: t.done,
    }));
  }
}
