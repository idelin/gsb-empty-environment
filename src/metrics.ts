export interface MetricsSummary {
  falsePositives: number;
  trueRemovals: number;
  suspicions: number;
  falseSuspicions: number;
  staleHeartbeatsIgnored: number;
  postRemovalHeartbeats: number;
  detectionLatencyCount: number;
  detectionLatencyAvgMs: number;
  detectionLatencyMaxMs: number;
  detectionLatencyP95Ms: number;
}

export class Metrics {
  falsePositives = 0;
  trueRemovals = 0;
  suspicions = 0;
  falseSuspicions = 0;
  staleHeartbeatsIgnored = 0;
  postRemovalHeartbeats = 0;
  readonly detectionLatenciesMs: number[] = [];

  recordDetectionLatency(ms: number): void {
    this.detectionLatenciesMs.push(ms);
  }

  summary(): MetricsSummary {
    const latencies = [...this.detectionLatenciesMs].sort((a, b) => a - b);
    const n = latencies.length;
    const avg = n === 0 ? 0 : latencies.reduce((a, b) => a + b, 0) / n;
    const p95 = n === 0 ? 0 : latencies[Math.min(n - 1, Math.ceil(0.95 * n) - 1)];
    return {
      falsePositives: this.falsePositives,
      trueRemovals: this.trueRemovals,
      suspicions: this.suspicions,
      falseSuspicions: this.falseSuspicions,
      staleHeartbeatsIgnored: this.staleHeartbeatsIgnored,
      postRemovalHeartbeats: this.postRemovalHeartbeats,
      detectionLatencyCount: n,
      detectionLatencyAvgMs: avg,
      detectionLatencyMaxMs: n === 0 ? 0 : latencies[n - 1],
      detectionLatencyP95Ms: p95,
    };
  }
}
