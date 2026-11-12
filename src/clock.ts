export interface Clock {
  now(): number;
}

export class SystemClock implements Clock {
  now(): number {
    return Date.now();
  }
}

/** 可手动推进的时钟，供测试确定性地制造过期激活。 */
export class ManualClock implements Clock {
  private current: number;

  constructor(start = 0) {
    this.current = start;
  }

  now(): number {
    return this.current;
  }

  advance(ms: number): number {
    if (ms < 0) {
      throw new RangeError('clock can only advance forward');
    }
    this.current += ms;
    return this.current;
  }
}
