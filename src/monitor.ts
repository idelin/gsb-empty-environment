/**
 * LSM 性能诊断监控器。
 * 滑动窗口统计每次 Get 的读放大（实际读取的 SSTable 数）与 Bloom Filter 误判率，
 * 越限时给出动态建议：提高 Compaction 并发/权重，或重建 Bloom Filter。
 */

export interface GetSample {
  /** 本次 Get 实际读取（打开并检索）的 SSTable 数量 —— 读放大 */
  sstablesRead: number;
  /** Bloom Filter 判定次数 */
  bloomChecks: number;
  /** Bloom Filter 判定为不存在而跳过 SSTable 的次数 */
  bloomNegatives: number;
  /** Bloom Filter 误判（判定可能存在但表中实际没有）的次数 */
  falsePositives: number;
  /** 是否命中 */
  found: boolean;
}

export type AlertCode = 'HIGH_READ_AMPLIFICATION' | 'HIGH_BLOOM_FP_RATE';

export interface Alert {
  code: AlertCode;
  level: 'warn' | 'critical';
  metric: string;
  value: number;
  threshold: number;
  message: string;
  suggestion: string;
}

export interface MonitorOptions {
  /** 滑动窗口大小（样本数），默认 200 */
  windowSize?: number;
  /** 触发评估所需的最小样本数，默认 50 */
  minSamples?: number;
  /** 读放大告警阈值，默认 2.0 */
  readAmpWarn?: number;
  /** Bloom Filter 误判率告警阈值，默认 0.05 */
  fpRateWarn?: number;
}

export interface MetricsSnapshot {
  samples: number;
  avgReadAmplification: number;
  falsePositiveRate: number;
  totalSstablesRead: number;
  totalBloomChecks: number;
  totalBloomNegatives: number;
  totalFalsePositives: number;
}

const round2 = (n: number): number => Math.round(n * 100) / 100;

export class LsmDiagnosticsMonitor {
  private window: GetSample[] = [];
  private activeCodes = new Set<AlertCode>();
  private readonly windowSize: number;
  private readonly minSamples: number;
  private readonly readAmpWarn: number;
  private readonly fpRateWarn: number;

  constructor(options: MonitorOptions = {}) {
    this.windowSize = options.windowSize ?? 200;
    this.minSamples = options.minSamples ?? 50;
    this.readAmpWarn = options.readAmpWarn ?? 2.0;
    this.fpRateWarn = options.fpRateWarn ?? 0.05;
  }

  /** 记录一次 Get 样本；返回本次新触发的告警（边沿触发，不重复刷屏）。 */
  record(sample: GetSample): Alert[] {
    this.window.push(sample);
    if (this.window.length > this.windowSize) this.window.shift();
    if (this.window.length < this.minSamples) return [];

    const current = this.evaluate();
    const fresh = current.filter((a) => !this.activeCodes.has(a.code));
    const next = new Set(current.map((a) => a.code));
    // 迟滞：指标未回落到阈值的 80% 以下时保持告警态，避免边界抖动导致重复告警
    for (const code of this.activeCodes) {
      if (next.has(code)) continue;
      const s = this.snapshot();
      const borderline =
        code === 'HIGH_READ_AMPLIFICATION'
          ? s.avgReadAmplification > this.readAmpWarn * 0.8
          : s.falsePositiveRate > this.fpRateWarn * 0.8;
      if (borderline) next.add(code);
    }
    this.activeCodes = next;
    return fresh;
  }

  snapshot(): MetricsSnapshot {
    let read = 0;
    let checks = 0;
    let negatives = 0;
    let fps = 0;
    for (const s of this.window) {
      read += s.sstablesRead;
      checks += s.bloomChecks;
      negatives += s.bloomNegatives;
      fps += s.falsePositives;
    }
    // 误判率 = 误判次数 / 键实际不存在的判定次数（标准 Bloom Filter FPR 定义）
    const absentChecks = negatives + fps;
    return {
      samples: this.window.length,
      avgReadAmplification: this.window.length === 0 ? 0 : read / this.window.length,
      falsePositiveRate: absentChecks === 0 ? 0 : fps / absentChecks,
      totalSstablesRead: read,
      totalBloomChecks: checks,
      totalBloomNegatives: negatives,
      totalFalsePositives: fps,
    };
  }

  /** 对当前窗口进行评估，返回所有处于越限状态的告警。 */
  evaluate(): Alert[] {
    const s = this.snapshot();
    const alerts: Alert[] = [];

    if (s.avgReadAmplification > this.readAmpWarn) {
      const critical = s.avgReadAmplification > this.readAmpWarn * 2;
      alerts.push({
        code: 'HIGH_READ_AMPLIFICATION',
        level: critical ? 'critical' : 'warn',
        metric: 'readAmplification',
        value: round2(s.avgReadAmplification),
        threshold: this.readAmpWarn,
        message:
          `读放大 ${s.avgReadAmplification.toFixed(2)} 超过阈值 ${this.readAmpWarn.toFixed(2)}` +
          `（每次 Get 平均实际读取 ${s.avgReadAmplification.toFixed(2)} 个 SSTable）`,
        suggestion:
          '建议提高 Compaction 并发/权重（如 compactionConcurrency+1）以加速 SSTable 合并、' +
          '压制读延迟；同时检查是否存在被关闭的 Bloom Filter 并重建',
      });
    }

    const absentChecks = s.totalBloomNegatives + s.totalFalsePositives;
    if (absentChecks >= 50 && s.falsePositiveRate > this.fpRateWarn) {
      alerts.push({
        code: 'HIGH_BLOOM_FP_RATE',
        level: s.falsePositiveRate > this.fpRateWarn * 2 ? 'critical' : 'warn',
        metric: 'bloomFalsePositiveRate',
        value: round2(s.falsePositiveRate),
        threshold: this.fpRateWarn,
        message:
          `Bloom Filter 误判率 ${(s.falsePositiveRate * 100).toFixed(2)}% 超过阈值 ` +
          `${(this.fpRateWarn * 100).toFixed(2)}%`,
        suggestion:
          '建议重建 Bloom Filter（提高 bitsPerKey 或按真实键数重新分配位图）以降低误判、减少无效 SSTable 读取',
      });
    }

    return alerts;
  }

  reset(): void {
    this.window = [];
    this.activeCodes = new Set();
  }
}
