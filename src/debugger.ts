import { Fiber } from './fiber.ts';
import { Scheduler, type Transition } from './scheduler.ts';

export interface FiberSnapshot {
  id: string;
  pc: number;
  state: Fiber['state'];
  /** 挂起原因；就绪/运行/结束时为 null。 */
  suspendReason: string | null;
  waitingOn: string | null;
}

export interface BreakpointHit {
  fiberId: string;
  pc: number;
  tick: number;
}

/**
 * 控制反转调试器：调度器的每一步推进都由调试器显式触发。
 * - setBreakpoint：按 协程 + 指令序号 打断点；
 * - pauseClock：暂停全局时钟（睡眠者不再到期）；
 * - tick：手动推动一个时隙；
 * - stepFiber：单步执行某个协程的一条指令；
 * - snapshot：查看所有协程的挂起原因；
 * - transitions：状态迁移字典，用于回放定位假死时隙。
 */
export class Debugger {
  readonly scheduler: Scheduler;
  stepMode = true;
  private readonly breakpoints = new Map<string, Set<number>>();
  private hitQueue: BreakpointHit[] = [];

  constructor(scheduler: Scheduler) {
    this.scheduler = scheduler;
  }

  // ---- 断点 ----

  setBreakpoint(fiberId: string, pc: number): void {
    let set = this.breakpoints.get(fiberId);
    if (!set) {
      set = new Set();
      this.breakpoints.set(fiberId, set);
    }
    set.add(pc);
  }

  clearBreakpoint(fiberId: string, pc: number): void {
    this.breakpoints.get(fiberId)?.delete(pc);
  }

  /** 上一次 tick/step 命中的断点。 */
  drainBreakpointHits(): BreakpointHit[] {
    const hits = this.hitQueue;
    this.hitQueue = [];
    return hits;
  }

  // ---- 时钟 ----

  pauseClock(): void {
    this.scheduler.clockPaused = true;
  }

  resumeClock(): void {
    this.scheduler.clockPaused = false;
  }

  // ---- 推进 ----

  /** 手动推动一个时隙：时钟前进一步，随后每个就绪协程执行一条指令（遇断点暂停）。 */
  tick(): void {
    this.scheduler.tick();
    for (const fiber of this.scheduler.fibers) {
      if (fiber.state === 'ready') this.stepFiber(fiber.id);
    }
  }

  /** 单步：仅让指定协程执行一条指令，不推进时钟、不调度其他协程。 */
  stepFiber(fiberId: string): boolean {
    const fiber = this.requireFiber(fiberId);
    if (fiber.state !== 'ready') return false;
    const bp = this.breakpoints.get(fiberId);
    if (bp?.has(fiber.pc)) {
      this.hitQueue.push({ fiberId, pc: fiber.pc, tick: this.scheduler.clock });
      return false; // 命中断点：该指令暂不执行
    }
    return this.scheduler.stepFiber(fiber);
  }

  // ---- 观察 ----

  /** 所有协程当前状态与挂起原因（等待锁 / 等待 Channel / 睡眠 / 等待资源）。 */
  snapshot(): FiberSnapshot[] {
    return this.scheduler.fibers.map((fiber) => ({
      id: fiber.id,
      pc: fiber.pc,
      state: fiber.state,
      suspendReason: this.describeSuspend(fiber),
      waitingOn: fiber.waitingOn,
    }));
  }

  /** 状态迁移字典：fiberId -> 按时隙排列的迁移记录。 */
  transitions(): Map<string, Transition[]> {
    return this.scheduler.transitions;
  }

  /**
   * 假死定位：返回在 [fromTick, toTick] 区间内状态从未变化、且未结束的协程，
   * 附其卡住的挂起原因与起始时隙。
   */
  findStuckFibers(fromTick = 0, toTick = this.scheduler.clock): Array<{
    fiberId: string;
    stuckSince: number;
    suspendReason: string;
  }> {
    const stuck: Array<{ fiberId: string; stuckSince: number; suspendReason: string }> = [];
    for (const fiber of this.scheduler.fibers) {
      if (fiber.state === 'done') continue;
      const history = this.scheduler.transitions.get(fiber.id) ?? [];
      const lastChange = history.length === 0 ? 0 : history[history.length - 1].tick;
      const movedInWindow = history.some((t) => t.tick > fromTick && t.tick <= toTick);
      if (!movedInWindow && lastChange <= fromTick) {
        stuck.push({
          fiberId: fiber.id,
          stuckSince: lastChange,
          suspendReason: this.describeSuspend(fiber) ?? 'unknown',
        });
      }
    }
    return stuck;
  }

  private describeSuspend(fiber: Fiber): string | null {
    switch (fiber.state) {
      case 'sleeping':
        return `sleeping until tick ${fiber.wakeAtTick}`;
      case 'waiting-lock':
        return `waiting lock "${fiber.waitingOn}"`;
      case 'waiting-channel':
        return `waiting channel "${fiber.waitingOn}"`;
      case 'waiting-resource':
        return `waiting resource "${fiber.waitingOn}" (not initialized)`;
      default:
        return null;
    }
  }

  private requireFiber(fiberId: string): Fiber {
    const fiber = this.scheduler.fibers.find((f) => f.id === fiberId);
    if (!fiber) throw new Error(`unknown fiber: ${fiberId}`);
    return fiber;
  }
}
