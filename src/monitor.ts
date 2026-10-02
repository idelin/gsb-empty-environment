import type { GetTrace, LsmEngine } from './lsmEngine.ts';

export type AlertKind = 'READ_AMPLIFICATION' | 'BLOOM_FALSE_POSITIVE';

export interface Alert {
  kind: AlertKind;
  severity: 'warn' | 'critical';
  message: string;
  suggestion: string;
  metrics: { readAmplification: number; bloomFpRate: number };
  at: number;
}

export interface WindowStats {
  samples: number;
  /** 滑动窗口内平均每次 Get 实际读取的 SSTable 数 */
  readAmplification: number;
  /** 滑动窗口内 Bloom Filter 误判率 */
  bloomFpRate: number;
  totalTablesRead: number;
  totalBloomChecks: number;
  totalBloomFalsePositives: number;
}

export interface MonitorOptions {
  /** 滑动窗口大小（Get 样本数） */
  windowSize?: number;
  /** 读放大告警阈值 */
  readAmpThreshold?: number;
  /** Bloom 误判率告警阈值 */
  bloomFpThreshold?: number;
  /** 触发告警的最小样本数，避免冷启动抖动 */
  minSamples?: number;
}

/**
 * LSM 性能度量诊断器：
 *  - 统计每次 Get 实际读取的 SSTable 数量（读放大）
 *  - 统计 Bloom Filter 误判率
 *  - 越限时输出告警与动态调优建议（提高 Compaction 权重 / 重建 Filter）
 */
export class LsmDiagnosticsMonitor {
  private readonly windowSize: number;
  private readonly readAmpThreshold: number;
  private readonly bloomFpThreshold: number;
  private readonly minSamples: number;
  private window: GetTrace[] = [];
  private readonly alerts: Alert[] = [];
  private readonly alertListeners: ((alert: Alert) => void)[] = [];

  constructor(options: MonitorOptions = {}) {
    this.windowSize = options.windowSize ?? 100;
    this.readAmpThreshold = options.readAmpThreshold ?? 2.0;
    this.bloomFpThreshold = options.bloomFpThreshold ?? 0.05;
    this.minSamples = options.minSamples ?? 10;
  }

  /** 挂接到引擎，实时消费每次 Get 的 trace。 */
  attach(engine: LsmEngine): void {
    engine.onGet((trace) => this.record(trace));
  }

  record(trace: GetTrace): void {
    this.window.push(trace);
    if (this.window.length > this.windowSize) this.window.shift();
    this.evaluate();
  }

  stats(): WindowStats {
    let tablesRead = 0;
    let bloomChecks = 0;
    let bloomFp = 0;
    for (const t of this.window) {
      tablesRead += t.tablesRead;
      bloomChecks += t.bloomChecks;
      bloomFp += t.bloomFalsePositives;
    }
    const samples = this.window.length;
    return {
      samples,
      readAmplification: samples === 0 ? 0 : tablesRead / samples,
      bloomFpRate: bloomChecks === 0 ? 0 : bloomFp / bloomChecks,
      totalTablesRead: tablesRead,
      totalBloomChecks: bloomChecks,
      totalBloomFalsePositives: bloomFp,
    };
  }

  private evaluate(): void {
    if (this.window.length < this.minSamples) return;
    const s = this.stats();
    if (s.readAmplification > this.readAmpThreshold) {
      const severity = s.readAmplification > this.readAmpThreshold * 2 ? 'critical' : 'warn';
      this.raise({
        kind: 'READ_AMPLIFICATION',
        severity,
        message: `读放大越限：平均每次 Get 读取 ${s.readAmplification.toFixed(2)} 张 SSTable（阈值 ${this.readAmpThreshold}）`,
        suggestion:
          '建议提高 Compaction 并发/权重（增大 compactionFanIn、降低 compactionTrigger），加速合并 SSTable 以压制读延迟；' +
          '若大量表关闭了 Bloom Filter，请同时重建 Filter。',
        metrics: { readAmplification: s.readAmplification, bloomFpRate: s.bloomFpRate },
        at: Date.now(),
      });
    }
    if (s.totalBloomChecks > 0 && s.bloomFpRate > this.bloomFpThreshold) {
      this.raise({
        kind: 'BLOOM_FALSE_POSITIVE',
        severity: s.bloomFpRate > this.bloomFpThreshold * 2 ? 'critical' : 'warn',
        message: `Bloom Filter 误判率越限：${(s.bloomFpRate * 100).toFixed(2)}%（阈值 ${(this.bloomFpThreshold * 100).toFixed(2)}%）`,
        suggestion: '建议重建 Bloom Filter（rebuildAllFilters），并检查预期条目数与目标误判率参数。',
        metrics: { readAmplification: s.readAmplification, bloomFpRate: s.bloomFpRate },
        at: Date.now(),
      });
    }
  }

  private raise(alert: Alert): void {
    const last = this.alerts[this.alerts.length - 1];
    // 同类告警去抖：避免窗口滑动期间刷屏
    if (last && last.kind === alert.kind && alert.at - last.at < 50) return;
    this.alerts.push(alert);
    for (const l of this.alertListeners) l(alert);
  }

  onAlert(listener: (alert: Alert) => void): void {
    this.alertListeners.push(listener);
  }

  get recentAlerts(): readonly Alert[] {
    return this.alerts;
  }

  reset(): void {
    this.window = [];
    this.alerts.length = 0;
  }
}
