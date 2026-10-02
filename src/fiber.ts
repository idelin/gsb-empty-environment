import type { Instruction, FiberBody } from './instructions.ts';

/** 协程状态机 */
export type FiberState =
  | 'ready'                // 就绪，等待调度
  | 'running'              // 正在执行指令
  | 'sleeping'             // 睡眠，等待时钟到期
  | 'waiting-lock'         // 等待互斥锁
  | 'waiting-channel-recv' // 等待 Channel 可读
  | 'waiting-channel-send' // 等待 Channel 可写
  | 'waiting-resource'     // 等待资源被初始化
  | 'paused-breakpoint'    // 被调试器断点暂停
  | 'done';                // 执行完毕

/** 挂起原因（供调试器展示） */
export type SuspendReason =
  | { kind: 'sleep'; wakeAtTick: number }
  | { kind: 'lock'; name: string }
  | { kind: 'channel-recv'; name: string }
  | { kind: 'channel-send'; name: string }
  | { kind: 'resource'; name: string }
  | { kind: 'breakpoint'; pc: number };

export function describeReason(reason: SuspendReason): string {
  switch (reason.kind) {
    case 'sleep': return `睡眠至 tick=${reason.wakeAtTick}`;
    case 'lock': return `等待锁 "${reason.name}"`;
    case 'channel-recv': return `等待 Channel "${reason.name}" 可读`;
    case 'channel-send': return `等待 Channel "${reason.name}" 可写`;
    case 'resource': return `等待资源 "${reason.name}" 初始化`;
    case 'breakpoint': return `断点暂停 @pc=${reason.pc}`;
  }
}

let nextFiberId = 1;

export function resetFiberIds(): void {
  nextFiberId = 1;
}

export class Fiber {
  readonly id: number;
  readonly name: string;
  /** 下一条待执行指令的序号（已执行指令数） */
  pc = 0;
  state: FiberState = 'ready';
  suspendReason: SuspendReason | null = null;
  /** 最近一次 yield 的指令（调试用） */
  lastInstruction: Instruction | null = null;
  private readonly gen: Generator<Instruction, void, unknown>;

  constructor(name: string, body: FiberBody) {
    this.id = nextFiberId++;
    this.name = name;
    this.gen = body();
  }

  get isDone(): boolean {
    return this.state === 'done';
  }

  /**
   * 推进一条指令。sendValue 是上一条指令的返回结果（如 recv 到的值）。
   * 返回 yield 出的指令；若协程结束返回 null。
   */
  advance(sendValue?: unknown): Instruction | null {
    this.state = 'running';
    const result = this.gen.next(sendValue);
    if (result.done) {
      this.state = 'done';
      this.suspendReason = null;
      return null;
    }
    this.lastInstruction = result.value;
    this.pc++;
    return result.value;
  }

  suspend(state: FiberState, reason: SuspendReason): void {
    this.state = state;
    this.suspendReason = reason;
  }

  resume(): void {
    this.state = 'ready';
    this.suspendReason = null;
  }
}
