/**
 * 响应式环路死锁保护器（核心守护模块）。
 *
 * 职责：
 * 1. 记录一次更新级联（cascade）中的全部因果事件（读 / 写 / 调度 / 执行）。
 * 2. 检测 Effect / Computed 直接或间接写入自身依赖的恶意逻辑，
 *    并记录首个自依赖写入作为“触发源”。
 * 3. 在检测到调用栈环路（节点重入）或迭代次数突破阈值时，
 *    抛出 CircularSignalUpdate 强制阻断。
 */

import { CircularSignalUpdate, type CircularReport, type CycleKind, type SourceInfo } from './errors.ts';

export const DEFAULT_MAX_ITERATIONS = 50;

export interface GuardOptions {
  /** 单次更新级联允许的最大迭代次数，默认 50 */
  maxIterations?: number;
}

class ReactiveGuard {
  private maxIterations: number = DEFAULT_MAX_ITERATIONS;
  private iterations = 0;
  private events: string[] = [];
  private runStack: string[] = [];
  private selfWriteSource: SourceInfo | null = null;
  private cascadeDepth = 0;

  configure(options: GuardOptions): void {
    if (options.maxIterations !== undefined) {
      if (!Number.isInteger(options.maxIterations) || options.maxIterations < 1) {
        throw new RangeError('maxIterations 必须是 >= 1 的整数');
      }
      this.maxIterations = options.maxIterations;
    }
  }

  getMaxIterations(): number {
    return this.maxIterations;
  }

  getIterationCount(): number {
    return this.iterations;
  }

  /**
   * 进入一个更新级联。级联可以嵌套（Signal.set -> flush -> Effect.run -> Signal.set），
   * 只有最外层的进入会重置计数器与因果链。
   */
  enterCascade(): void {
    if (this.cascadeDepth === 0) {
      this.iterations = 0;
      this.events = [];
      this.selfWriteSource = null;
    }
    this.cascadeDepth += 1;
  }

  exitCascade(): void {
    this.cascadeDepth = Math.max(0, this.cascadeDepth - 1);
  }

  /**
   * 每次调度执行一个 Effect 前调用。
   * 迭代计数达到阈值时抛出 CircularSignalUpdate（iteration-overflow）。
   */
  beginIteration(observerLabel: string): void {
    this.iterations += 1;
    if (this.iterations >= this.maxIterations) {
      throw this.buildError('iteration-overflow', observerLabel);
    }
  }

  /**
   * 节点开始执行（Effect 运行 / Computed 重算）前调用。
   * 若同一节点已在执行栈上，说明出现调用栈环路，立即抛出。
   */
  enterRun(nodeLabel: string): void {
    if (this.runStack.includes(nodeLabel)) {
      throw this.buildError('call-stack-cycle', nodeLabel);
    }
    this.runStack.push(nodeLabel);
  }

  exitRun(): void {
    this.runStack.pop();
  }

  /** 追加一条因果链事件 */
  log(event: string): void {
    this.events.push(event);
  }

  /** 记录一次自依赖写入（直接 / 间接），首个记录即触发源 */
  recordSelfWrite(source: SourceInfo): void {
    if (this.selfWriteSource === null) {
      this.selfWriteSource = source;
    }
  }

  private buildError(kind: CycleKind, trigger: string): CircularSignalUpdate {
    const report: CircularReport = {
      kind,
      iterations: this.iterations,
      maxIterations: this.maxIterations,
      trigger,
      source: this.selfWriteSource,
      chain: [...this.events],
    };
    return new CircularSignalUpdate(report);
  }
}

/** 全局唯一守护器实例 */
export const guard = new ReactiveGuard();

/** 调整守护器配置（如迭代阈值） */
export function configureGuard(options: GuardOptions): void {
  guard.configure(options);
}
