/**
 * 确定性模拟网络：用种子随机数生成心跳到达时间，
 * 支持“常态抖动”“偶发抖动尖峰”“网络分区（断网）”三种行为。
 */

/** mulberry32 种子随机数发生器 */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Box-Muller 生成高斯噪声 */
export function gaussian(rand: () => number, mean: number, stdDev: number): number {
  const u1 = Math.max(rand(), 1e-12);
  const u2 = rand();
  const z = Math.sqrt(-2 * Math.log(u1)) * Math.cos(2 * Math.PI * u2);
  return mean + stdDev * z;
}

export interface NetworkScenario {
  /** 基础心跳间隔（ms） */
  baseIntervalMs: number;
  /** 常态抖动标准差（ms） */
  jitterStdDevMs: number;
  /** 偶发抖动尖峰：每隔 spikeEvery 个心跳出现一次 */
  spikeEvery?: number;
  /** 尖峰额外延迟（ms）——处于历史方差范围内的“短时抖动” */
  spikeDelayMs?: number;
  /** 从第几个心跳开始断网（之后不再有心跳到达） */
  partitionAfterBeats?: number;
  /** 随机种子 */
  seed?: number;
}

/** 生成一串心跳到达时间（相对 t=0，单位 ms） */
export function generateArrivals(scenario: NetworkScenario): number[] {
  const rand = mulberry32(scenario.seed ?? 42);
  const arrivals: number[] = [];
  let t = 0;
  const total = (scenario.partitionAfterBeats ?? 200) + 5;
  for (let i = 0; i < total; i++) {
    if (scenario.partitionAfterBeats !== undefined && i >= scenario.partitionAfterBeats) {
      break; // 网络分区：不再有心跳
    }
    let interval = gaussian(rand, scenario.baseIntervalMs, scenario.jitterStdDevMs);
    if (
      scenario.spikeEvery !== undefined &&
      scenario.spikeDelayMs !== undefined &&
      i > 0 &&
      i % scenario.spikeEvery === 0
    ) {
      interval += scenario.spikeDelayMs;
    }
    t += Math.max(1, interval);
    arrivals.push(t);
  }
  return arrivals;
}

/** 传统固定超时探测器（对照组）：超过 timeoutMs 未收到心跳即判死 */
export class FixedTimeoutDetector {
  private lastAt: number | null = null;
  private readonly timeoutMs: number;
  constructor(timeoutMs: number) {
    this.timeoutMs = timeoutMs;
  }
  heartbeat(at: number): void {
    this.lastAt = at;
  }
  isAlive(at: number): boolean {
    if (this.lastAt === null) return true;
    return at - this.lastAt <= this.timeoutMs;
  }
}
