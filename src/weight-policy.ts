import type { NodeMetrics } from "./types.ts";

export interface WeightPolicyOptions {
  /** Virtual-node count for a fully healthy node. Default 100. */
  baseWeight: number;
  /**
   * Load at or below this threshold keeps full weight; above it the weight
   * decays linearly toward `minFactor` as load approaches 1. Default 0.5.
   */
  softThreshold: number;
  /** Lower bound of the health factor, so a hot node still keeps a floor. Default 0.2. */
  minFactor: number;
}

export const DEFAULT_POLICY: WeightPolicyOptions = {
  baseWeight: 100,
  softThreshold: 0.5,
  minFactor: 0.2,
};

/**
 * Maps real-time metrics to a health factor in [minFactor, 1].
 * With the defaults: cpu 30% -> 1.0 (weight 100), cpu 90% -> 0.2 (weight 20).
 */
export function healthFactor(
  metrics: NodeMetrics,
  policy: WeightPolicyOptions = DEFAULT_POLICY,
): number {
  const load = Math.max(metrics.cpu, metrics.memory);
  if (load <= policy.softThreshold) return 1;
  const factor = (1 - load) / (1 - policy.softThreshold);
  return Math.max(policy.minFactor, Math.min(1, factor));
}

/** Converts a health factor into a concrete virtual-node weight (>= 1). */
export function weightFor(
  metrics: NodeMetrics,
  policy: WeightPolicyOptions = DEFAULT_POLICY,
): number {
  return Math.max(1, Math.round(policy.baseWeight * healthFactor(metrics, policy)));
}
