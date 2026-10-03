/**
 * 受界漂移时钟:本地时间 = 全局 tick + 偏移。
 * 偏移被限制在 [-maxPairwiseSkew/2, +maxPairwiseSkew/2],
 * 从而保证任意两节点时钟差不超过 maxPairwiseSkew。
 */
export class BoundedClock {
  readonly nodeId: string;
  readonly maxPairwiseSkew: number;
  private offset = 0;

  constructor(nodeId: string, maxPairwiseSkew: number) {
    this.nodeId = nodeId;
    this.maxPairwiseSkew = maxPairwiseSkew;
  }

  get offsetTicks(): number {
    return this.offset;
  }

  setOffset(offset: number): void {
    if (Math.abs(offset) > this.maxPairwiseSkew / 2) {
      throw new RangeError(
        `clock offset ${offset} out of bound |offset| <= ${this.maxPairwiseSkew / 2}`,
      );
    }
    this.offset = offset;
  }

  localTime(globalTick: number): number {
    return globalTick + this.offset;
  }
}
