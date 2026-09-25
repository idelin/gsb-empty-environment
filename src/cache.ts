import { compileGraph, versionOfConfig } from './graph.ts';
import type { ConversionPlan, GraphConfig } from './graph.ts';

/**
 * Plans are cached by config version (content hash). A hot config swap
 * produces a new version, so old and new plans stay isolated; entries
 * for stale versions are never reused for a different config.
 */
export class PlanCache {
  private plans = new Map<string, ConversionPlan>();

  get size(): number {
    return this.plans.size;
  }

  get(version: string): ConversionPlan | undefined {
    return this.plans.get(version);
  }

  has(version: string): boolean {
    return this.plans.has(version);
  }

  compileOrGet(config: GraphConfig): ConversionPlan {
    const version = versionOfConfig(config);
    const hit = this.plans.get(version);
    if (hit) return hit;
    const plan = compileGraph(config);
    this.plans.set(plan.version, plan);
    return plan;
  }
}
