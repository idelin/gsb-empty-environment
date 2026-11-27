import { mannWhitneyU, quantile, type MannWhitneyResult } from "./stats.js";

/** Direction in which a metric is considered "worse". */
export type Direction = "failOnHigh" | "failOnLow";

/** Policy applied when one or both samples are empty. */
export type NoDataPolicy = "NODATA_FAIL" | "NODATA_PASS";

export type MetricVerdict = "PASS" | "FAIL" | "INCONCLUSIVE" | "SKIPPED";

export interface MetricConfig {
  name: string;
  direction: Direction;
  /** Significance level for the one-sided test in the fail direction. */
  alpha?: number;
  /**
   * Effect-size gate: the metric only fails when the relative degradation
   * ratio (bad direction) strictly exceeds this value. Use e.g. 1.01 to
   * tolerate up to 1% degradation. Defaults to 1 (any significant
   * degradation fails).
   */
  minEffectRatio?: number;
  /** Minimum per-side sample size; below this the verdict is INCONCLUSIVE. */
  minSampleSize?: number;
  /** Policy when baseline or canary sample is empty. Default NODATA_FAIL. */
  noDataPolicy?: NoDataPolicy;
  /** Critical metrics veto the whole release when they fail. */
  critical?: boolean;
  /** Weight of this metric inside its group. Default 1. */
  weight?: number;
  /** Quantile used for the effect-size ratio. Default 0.5 (median). */
  quantile?: number;
}

export interface MetricResult {
  name: string;
  verdict: MetricVerdict;
  reason: string;
  critical: boolean;
  weight: number;
  stats?: MannWhitneyResult;
  /** Relative degradation ratio in the fail direction (>= 0, may be Infinity). */
  effectRatio?: number;
}

function degradationRatio(
  direction: Direction,
  baselineQ: number,
  canaryQ: number,
): number {
  const numerator = direction === "failOnHigh" ? canaryQ : baselineQ;
  const denominator = direction === "failOnHigh" ? baselineQ : canaryQ;
  if (denominator === 0) {
    if (numerator === 0) return 1;
    return numerator > 0 ? Infinity : 0;
  }
  if (denominator < 0) {
    throw new Error(
      "effect-size ratio requires non-negative metric values; got negative quantile",
    );
  }
  return numerator / denominator;
}

/**
 * Judge a single metric: tie-corrected Mann-Whitney U significance test in
 * the configured fail direction, gated by a relative effect-size threshold.
 * A metric fails only when it is BOTH statistically significant AND degrades
 * by more than `minEffectRatio`.
 */
export function judgeMetric(
  config: MetricConfig,
  baseline: number[],
  canary: number[],
): MetricResult {
  const alpha = config.alpha ?? 0.05;
  const minEffectRatio = config.minEffectRatio ?? 1;
  const minSampleSize = config.minSampleSize ?? 1;
  const noDataPolicy = config.noDataPolicy ?? "NODATA_FAIL";
  const critical = config.critical ?? false;
  const weight = config.weight ?? 1;
  const q = config.quantile ?? 0.5;

  const base: Omit<MetricResult, "verdict" | "reason"> = {
    name: config.name,
    critical,
    weight,
  };

  if (baseline.length === 0 || canary.length === 0) {
    if (noDataPolicy === "NODATA_PASS") {
      return {
        ...base,
        verdict: "SKIPPED",
        reason: "no data; NODATA_PASS policy -> excluded from scoring",
      };
    }
    return {
      ...base,
      verdict: "FAIL",
      reason: "no data; NODATA_FAIL policy -> metric fails",
    };
  }

  if (baseline.length < minSampleSize || canary.length < minSampleSize) {
    return {
      ...base,
      verdict: "INCONCLUSIVE",
      reason: `sample size below minSampleSize=${minSampleSize}; retry instead of rolling back`,
    };
  }

  const stats = mannWhitneyU(baseline, canary);
  const effectRatio = degradationRatio(
    config.direction,
    quantile(baseline, q),
    quantile(canary, q),
  );

  if (stats.sigmaU === 0) {
    return {
      ...base,
      verdict: "PASS",
      reason: "all observations identical; zero difference",
      stats,
      effectRatio,
    };
  }

  const p =
    config.direction === "failOnHigh" ? stats.pUpper : stats.pLower;
  const significant = p < alpha;
  const effectExceeded = effectRatio > minEffectRatio;

  if (significant && effectExceeded) {
    return {
      ...base,
      verdict: "FAIL",
      reason: `significant degradation (p=${p.toExponential(3)} < ${alpha}) with effect ratio ${effectRatio} > ${minEffectRatio}`,
      stats,
      effectRatio,
    };
  }
  return {
    ...base,
    verdict: "PASS",
    reason: significant
      ? `statistically significant but effect ratio ${effectRatio} <= ${minEffectRatio}; waived by effect-size gate`
      : `not significant (p=${p.toExponential(3)} >= ${alpha})`,
    stats,
    effectRatio,
  };
}
