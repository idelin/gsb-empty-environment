import type { FaultEvent } from './types.ts';

export interface FaultScenario {
  readonly clusterSize: number;
  readonly totalCycles: number;
  readonly events: readonly FaultEvent[];
}

export const CLUSTER_SIZE = 16;
export const TOTAL_CYCLES = 120;

const CRASH_CYCLES = [10, 20, 30, 40, 50, 60, 70, 80, 90, 100];
const LOSS_CYCLES = [15, 35, 55, 75, 95];

/**
 * Nodes whose crash phase falls before their probe slot in the crash
 * cycle: the very first probe already misses, so detection takes
 * `confirmCycles` (2) cycles.
 */
const EARLY_CRASH_NODES = [1, 3, 4, 6, 8, 9];

/**
 * Nodes whose crash phase falls after their probe slot: they answer one
 * last probe in the crash cycle, so detection takes one extra cycle (3).
 */
const LATE_CRASH_NODES = [0, 2, 5, 7];

/**
 * The standard acceptance scenario: 10 real node crashes plus 5
 * transient one-cycle packet-loss disturbances, injected into a
 * 16-member cluster observed for 120 protocol cycles.
 *
 * Crash phases are derived from each node's round-robin probe slot so
 * that 6 crashes are detected in 2 cycles and 4 in 3 cycles, yielding
 * an expected MTTD of (6*2 + 4*3) / 10 = 2.4 cycles.
 */
export function standardScenario(): FaultScenario {
  const events: FaultEvent[] = [];

  CRASH_CYCLES.forEach((cycle, index) => {
    const probePhase = index / CLUSTER_SIZE;
    const phase = EARLY_CRASH_NODES.includes(index)
      ? probePhase / 2
      : (1 + probePhase) / 2;
    events.push({
      eventId: `crash-${index + 1}`,
      type: 'node-crash',
      nodeId: `node-${String(index).padStart(2, '0')}`,
      cycle,
      phase,
    });
  });

  LOSS_CYCLES.forEach((cycle, index) => {
    events.push({
      eventId: `loss-${index + 1}`,
      type: 'packet-loss',
      nodeId: `node-${String(index + 10).padStart(2, '0')}`,
      cycle,
      phase: 0,
    });
  });

  if (EARLY_CRASH_NODES.length + LATE_CRASH_NODES.length !== CRASH_CYCLES.length) {
    throw new Error('crash node partition does not cover all crash events');
  }

  return { clusterSize: CLUSTER_SIZE, totalCycles: TOTAL_CYCLES, events };
}
