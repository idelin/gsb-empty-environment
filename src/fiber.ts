import type { Effect } from './effects.ts';

/** 协程（Fiber）状态机。waiting-* 即“挂起原因”。 */
export type FiberState =
  | 'ready'
  | 'running'
  | 'sleeping'
  | 'waiting-lock'
  | 'waiting-channel'
  | 'waiting-resource'
  | 'done';

export type FiberBody = () => Generator<Effect, void, unknown>;

let nextFiberSeq = 0;

export class Fiber {
  readonly id: string;
  readonly name: string;
  private readonly gen: Generator<Effect, void, unknown>;

  /** 程序计数器：已执行的指令数，下一条待执行指令的序号。 */
  pc = 0;
  state: FiberState = 'ready';
  /** 挂起对象明细，如锁名 / 通道名 / 资源名。 */
  waitingOn: string | null = null;
  /** 睡眠唤醒时刻（调度器全局时钟的 tick）。 */
  wakeAtTick: number | null = null;
  /** 被唤醒后要注入回生成器的值（如 chan.recv 的结果）。 */
  inbox: unknown = undefined;

  constructor(body: FiberBody, name?: string) {
    this.name = name ?? `fiber-${nextFiberSeq}`;
    this.id = this.name;
    this.gen = body();
    nextFiberSeq += 1;
  }

  /** 推进一条指令；返回该指令（Effect），若协程已结束返回 null。 */
  next(): Effect | null {
    const { value, done } = this.gen.next(this.inbox);
    this.inbox = undefined;
    if (done) return null;
    this.pc += 1;
    return value;
  }
}
