import { judgeMetric, type MetricConfig, type MetricResult } from "./judge.js";

export interface MetricGroup {
  name: string;
  /** Relative weight of this group; normalized across effective groups. */
  weight: number;
  metrics: MetricConfig[];
}

export interface AggregateConfig {
  groups: MetricGroup[];
  /** Score threshold in [0, 1] at or above which the release passes. */
  passScore: number;
}

export type AggregateVerdict = "PASS" | "FAIL" | "INCONCLUSIVE";

export interface AggregateResult {
  verdict: AggregateVerdict;
  /** Weighted score in [0, 1] over effective (non-skipped) metrics. */
  score: number;
  metricResults: MetricResult[];
  /** Name of the metric that vetoed the release, if any. */
  vetoedBy?: string;
  reason: string;
}

export interface MetricSamples {
  baseline: number[];
  canary: number[];
}

/**
 * Aggregate per-metric verdicts into a release-level verdict.
 *
 * - Metrics judged SKIPPED (NODATA_PASS with no data) are ignored and the
 *   remaining weights are re-normalized.
 * - Group weights are normalized across groups that still have effective
 *   metrics; inside a group, metric weights (default 1) are normalized.
 * - Any failed `critical` metric, or any metric failed by the NODATA_FAIL
 *   policy, vetoes the release regardless of the total score.
 * - Any INCONCLUSIVE metric (without a failure) yields an INCONCLUSIVE
 *   release verdict so the pipeline retries instead of rolling back.
 */
export function aggregateMetrics(
  config: AggregateConfig,
  samples: Map<string, MetricSamples>,
): AggregateResult {
  const metricResults: MetricResult[] = [];
  const byName = new Map<string, MetricResult>();

  for (const group of config.groups) {
    for (const metric of group.metrics) {
      const s = samples.get(metric.name) ?? { baseline: [], canary: [] };
      const result = judgeMetric(metric, s.baseline, s.canary);
      metricResults.push(result);
      byName.set(metric.name, result);
    }
  }

  // Critical / NODATA_FAIL veto: immediate FAIL regardless of score.
  for (const result of metricResults) {
    if (result.verdict !== "FAIL") continue;
    const noDataFail = result.reason.includes("NODATA_FAIL");
    if (result.critical || noDataFail) {
      return {
        verdict: "FAIL",
        score: 0,
        metricResults,
        vetoedBy: result.name,
        reason: `metric "${result.name}" vetoed the release (${result.reason})`,
      };
    }
  }

  // Weighted score over effective metrics with re-normalization.
  let totalScore = 0;
  let totalGroupWeight = 0;
  for (const group of config.groups) {
    let groupScore = 0;
    let groupMetricWeight = 0;
    for (const metric of group.metrics) {
      const result = byName.get(metric.name)!;
      if (result.verdict === "SKIPPED" || result.verdict === "INCONCLUSIVE") {
        continue;
      }
      const w = result.weight;
      groupScore += w * (result.verdict === "PASS" ? 1 : 0);
      groupMetricWeight += w;
    }
    if (groupMetricWeight === 0) continue; // whole group skipped/inconclusive
    totalScore += group.weight * (groupScore / groupMetricWeight);
    totalGroupWeight += group.weight;
  }
  const score = totalGroupWeight === 0 ? 1 : totalScore / totalGroupWeight;

  const anyInconclusive = metricResults.some(
    (r) => r.verdict === "INCONCLUSIVE",
  );

  if (score < config.passScore) {
    return {
      verdict: "FAIL",
      score,
      metricResults,
      reason: `score ${score} below passScore ${config.passScore}`,
    };
  }
  if (anyInconclusive) {
    return {
      verdict: "INCONCLUSIVE",
      score,
      metricResults,
      reason: "one or more metrics inconclusive; retry before deciding",
    };
  }
  return {
    verdict: "PASS",
    score,
    metricResults,
    reason: `score ${score} >= passScore ${config.passScore}`,
  };
}
