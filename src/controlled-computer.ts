/**
 * 可控计算器：把昂贵的 compute 包成「调用先入队、完成靠手动」的形式，
 * 让验证脚本可以精确编排并发时序（旧任务晚到、取消后迟到完成等）。
 *
 * 每次 compute 调用产生一个 PendingCall，只有调用 completeNext/completeAll
 * 才会用 impl 对（调用时捕获的）输入求值并兑现。impl 在兑现那一刻才读输入，
 * 因此能真实模拟「计算期间输入被覆盖」的危险场景。
 */
export interface PendingCall<I, P, R> {
  readonly seq: number;
  readonly input: I;
  readonly params: P;
  readonly signal: AbortSignal;
  resolve(value: R): void;
  reject(err: unknown): void;
}

export class ControlledComputer<I, P, R> {
  /** 实际 compute 被调用的总次数（用于核对合并/重算次数）。 */
  invocations = 0;

  private readonly pending: PendingCall<I, P, R>[] = [];
  private seq = 0;

  constructor(private readonly impl: (input: I, params: P) => R) {}

  readonly compute = (input: I, params: P, signal: AbortSignal): Promise<R> => {
    this.invocations += 1;
    this.seq += 1;
    const seq = this.seq;
    return new Promise<R>((resolve, reject) => {
      this.pending.push({ seq, input, params, signal, resolve, reject });
    });
  };

  get pendingCount(): number {
    return this.pending.length;
  }

  /** 兑现最早一个未完成的调用（impl 在此时才读输入）。 */
  completeNext(): PendingCall<I, P, R> {
    const call = this.pending.shift();
    if (call === undefined) {
      throw new Error('no pending compute call');
    }
    call.resolve(this.impl(call.input, call.params));
    return call;
  }

  completeAll(): void {
    while (this.pendingCount > 0) {
      this.completeNext();
    }
  }
}
