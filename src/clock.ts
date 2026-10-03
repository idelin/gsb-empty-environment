/**
 * 单节点确定性时钟：ARIES 中 LSN 的唯一来源。
 * 严格单调递增、无回拨、无并发分叉；崩溃重启后从 WAL 最大 LSN 恢复。
 */
export class DeterministicClock {
  #current: number;

  constructor(startAt = 0) {
    if (!Number.isInteger(startAt) || startAt < 0) {
      throw new Error(`invalid clock seed: ${startAt}`);
    }
    this.#current = startAt;
  }

  /** 分配下一个逻辑时间戳（LSN）。 */
  tick(): number {
    this.#current += 1;
    return this.#current;
  }

  now(): number {
    return this.#current;
  }

  /** 重启恢复：将时钟推进到至少 `observed`，保证不重发已用 LSN。 */
  observe(observed: number): void {
    if (observed > this.#current) {
      this.#current = observed;
    }
  }
}
