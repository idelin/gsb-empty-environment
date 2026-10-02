import { Scheduler } from './scheduler.ts';
import type { Transition } from './scheduler.ts';
import { Fiber, describeReason } from './fiber.ts';
import type { FiberState } from './fiber.ts';

export interface DebugTransition extends Transition {
  source: 'scheduler' | 'debugger';
}

export interface StallReport {
  fiberId: number;
  fiberName: string;
  state: FiberState;
  reason: string;
  /** 进入该状态的 tick（"特定时隙"） */
  sinceTick: number;
  /** 已停滞的 tick 数 */
  stalledFor: number;
}

const SUSPENDED_STATES: ReadonlySet<FiberState> = new Set([
  'sleeping',
  'waiting-lock',
  'waiting-channel-recv',
  'waiting-channel-send',
  'waiting-resource',
]);

/**
 * 控制反转调试器：调度器的每一步都由调试器显式推动。
 * - enterStepMode：暂停全局时钟，进入单步调试模式
 * - tick：手动推动一个 tick
 * - stepInstruction：单步执行某个协程的一条指令
 * - 断点、挂起原因查看、状态转换字典、假死（stall）检测
 */
export class Debugger {
  readonly scheduler: Scheduler;
  stepMode = false;

  private readonly breakpoints = new Set<string>();
  private readonly log: DebugTransition[] = [];

  constructor(scheduler: Scheduler) {
    this.scheduler = scheduler;
    scheduler.onTransition((t) => {
      this.log.push({ ...t, source: 'scheduler' });
    });
    scheduler.setBeforeInstructionHook((fiber) => this.beforeInstruction(fiber));
  }

  // ---- 模式与全局时钟 ----------------------------------------------------

  /** 进入单步调试模式：暂停全局时钟，之后只能由调试器手动推动 */
  enterStepMode(): void {
    this.stepMode = true;
    this.scheduler.pauseClock();
  }

  exitStepMode(): void {
    this.stepMode = false;
    this.scheduler.resumeClock();
  }

  get currentTick(): number {
    return this.scheduler.tick;
  }

  /** 手动推动调度器 n 个 tick（默认 1 个） */
  tick(n = 1): void {
    for (let i = 0; i < n; i++) this.scheduler.tickOnce();
  }

  // ---- 断点 ----------------------------------------------------------------

  setBreakpoint(fiber: number | string, pc: number): void {
    const f = this.mustGetFiber(fiber);
    this.breakpoints.add(`${f.id}:${pc}`);
  }

  removeBreakpoint(fiber: number | string, pc: number): void {
    const f = this.mustGetFiber(fiber);
    this.breakpoints.delete(`${f.id}:${pc}`);
  }

  clearBreakpoints(): void {
    this.breakpoints.clear();
  }

  private beforeInstruction(fiber: Fiber): boolean {
    if (!this.breakpoints.has(`${fiber.id}:${fiber.pc}`)) return true;
    const from = fiber.state;
    fiber.suspend('paused-breakpoint', { kind: 'breakpoint', pc: fiber.pc });
    this.record(fiber, from, 'paused-breakpoint', `断点命中 @pc=${fiber.pc}`);
    return false;
  }

  /** 继续：恢复所有被断点暂停的协程为就绪态 */
  continue(): void {
    for (const fiber of this.scheduler.allFibers()) {
      if (fiber.state === 'paused-breakpoint') {
        fiber.resume();
        this.record(fiber, 'paused-breakpoint', 'ready', '断点继续');
      }
    }
  }

  // ---- 单步 ------------------------------------------------------------------

  /**
   * 单步执行指定协程的一条指令（不推进全局时钟）。
   * 若协程被断点暂停，则强制执行该指令并越过断点。
   */
  stepInstruction(fiber: number | string): boolean {
    const f = this.mustGetFiber(fiber);
    if (f.state === 'paused-breakpoint') {
      this.record(f, 'paused-breakpoint', 'running', `单步越过断点 @pc=${f.pc}`);
    }
    return this.scheduler.stepFiberOnce(f, true);
  }

  // ---- 观察 ------------------------------------------------------------------

  /** 所有未完成协程的当前挂起原因字典 */
  suspendReasons(): Record<string, string> {
    const result: Record<string, string> = {};
    for (const fiber of this.scheduler.allFibers()) {
      if (fiber.isDone) continue;
      result[fiber.name] = fiber.suspendReason
        ? describeReason(fiber.suspendReason)
        : `就绪 (state=${fiber.state})`;
    }
    return result;
  }

  /** 完整状态转换日志 */
  transitions(): DebugTransition[] {
    return [...this.log];
  }

  /** 某协程的状态转换字典 */
  transitionsOf(fiber: number | string): DebugTransition[] {
    const f = this.mustGetFiber(fiber);
    return this.log.filter((t) => t.fiberId === f.id);
  }

  /** 某 tick 内发生的全部状态转换 */
  transitionsAt(tick: number): DebugTransition[] {
    return this.log.filter((t) => t.tick === tick);
  }

  /**
   * 重放日志，重建指定 tick 时刻每个协程的状态。
   * 返回 { 协程名: { state, pc } }。
   */
  stateAt(tick: number): Record<string, { state: FiberState; pc: number }> {
    const result: Record<string, { state: FiberState; pc: number }> = {};
    for (const fiber of this.scheduler.allFibers()) {
      result[fiber.name] = { state: 'ready', pc: 0 };
    }
    for (const t of this.log) {
      if (t.tick > tick) break;
      result[t.fiberName] = { state: t.to, pc: t.pc };
    }
    return result;
  }

  /**
   * 假死检测：找出处于挂起状态且超过 minStallTicks 未发生状态转换的协程，
   * 报告其进入该状态的时隙与挂起原因（如等待未初始化资源）。
   */
  detectStall(minStallTicks = 1): StallReport[] {
    const reports: StallReport[] = [];
    const now = this.scheduler.tick;
    for (const fiber of this.scheduler.allFibers()) {
      if (!SUSPENDED_STATES.has(fiber.state)) continue;
      const last = [...this.log].reverse().find((t) => t.fiberId === fiber.id);
      const sinceTick = last ? last.tick : 0;
      const stalledFor = now - sinceTick;
      if (stalledFor >= minStallTicks) {
        reports.push({
          fiberId: fiber.id,
          fiberName: fiber.name,
          state: fiber.state,
          reason: fiber.suspendReason ? describeReason(fiber.suspendReason) : '未知',
          sinceTick,
          stalledFor,
        });
      }
    }
    return reports;
  }

  /** 打印当前所有协程快照（便于人工排错） */
  snapshot(): string {
    const lines = [`tick=${this.scheduler.tick} stepMode=${this.stepMode}`];
    for (const fiber of this.scheduler.allFibers()) {
      const reason = fiber.suspendReason ? ` | ${describeReason(fiber.suspendReason)}` : '';
      lines.push(`  #${fiber.id} ${fiber.name}: ${fiber.state} pc=${fiber.pc}${reason}`);
    }
    return lines.join('\n');
  }

  // ---- 内部 ------------------------------------------------------------------

  private mustGetFiber(idOrName: number | string): Fiber {
    const fiber = this.scheduler.getFiber(idOrName);
    if (!fiber) throw new Error(`未找到协程: ${String(idOrName)}`);
    return fiber;
  }

  private record(fiber: Fiber, from: FiberState, to: FiberState, note: string): void {
    this.log.push({
      tick: this.scheduler.tick,
      fiberId: fiber.id,
      fiberName: fiber.name,
      from,
      to,
      pc: fiber.pc,
      note,
      source: 'debugger',
    });
  }
}
