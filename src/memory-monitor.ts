/**
 * 堆内存采样监控器：周期性记录 heapUsed 峰值，用于验证内存保护效果。
 */
export interface MemoryMonitor {
  /** 停止采样并返回观测到的峰值 heapUsed（字节） */
  stop(): number;
  readonly peak: number;
}

export function startMemoryMonitor(intervalMs = 10): MemoryMonitor {
  let peak = process.memoryUsage().heapUsed;
  const timer = setInterval(() => {
    const used = process.memoryUsage().heapUsed;
    if (used > peak) {
      peak = used;
    }
  }, intervalMs);
  timer.unref();
  return {
    get peak(): number {
      return peak;
    },
    stop(): number {
      clearInterval(timer);
      const used = process.memoryUsage().heapUsed;
      if (used > peak) {
        peak = used;
      }
      return peak;
    },
  };
}

/** 触发 GC 并返回稳定后的 heapUsed 基线（需 --expose-gc，否则退化为当前值） */
export async function heapBaseline(): Promise<number> {
  const gc = (globalThis as { gc?: () => void }).gc;
  if (gc !== undefined) {
    gc();
    await new Promise((resolve) => setTimeout(resolve, 20));
    gc();
  }
  return process.memoryUsage().heapUsed;
}
