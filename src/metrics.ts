/**
 * 指标：单节点每秒出/入包数、百分位统计。
 */

import type { Network } from './sim.ts';

export interface PacketRate {
  outPerSec: number;
  inPerSec: number;
  totalPerSec: number;
}

/** 测量窗口内，平均每个节点每秒的网络出入包数。 */
export function perNodePacketRate(
  net: Network,
  nodeCount: number,
  windowMs: number,
): PacketRate {
  const seconds = windowMs / 1000;
  return {
    outPerSec: net.totalOut / nodeCount / seconds,
    inPerSec: net.totalIn / nodeCount / seconds,
    totalPerSec: (net.totalOut + net.totalIn) / nodeCount / seconds,
  };
}

export function percentile(sortedAsc: number[], p: number): number {
  if (sortedAsc.length === 0) return NaN;
  const idx = Math.min(
    sortedAsc.length - 1,
    Math.ceil((p / 100) * sortedAsc.length) - 1,
  );
  return sortedAsc[Math.max(0, idx)];
}

export function summarize(values: number[]): {
  min: number;
  p50: number;
  p95: number;
  max: number;
} {
  const sorted = [...values].sort((a, b) => a - b);
  return {
    min: sorted[0],
    p50: percentile(sorted, 50),
    p95: percentile(sorted, 95),
    max: sorted[sorted.length - 1],
  };
}
