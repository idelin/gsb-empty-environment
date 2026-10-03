import { BottleneckLink } from './link.ts';
import { DSCP, type Packet } from './packet.ts';

export interface FlowSpec {
  flowId: string;
  dscp: number;
  sizeBytes: number;
  /** One packet every `intervalMs` milliseconds. */
  intervalMs: number;
}

export interface ScenarioResult {
  link: BottleneckLink;
  stats: ReturnType<BottleneckLink['statsByFlow']>;
}

/**
 * Run a mixed-traffic scenario over a congested bottleneck link.
 * Every flow is persistently backlogged per its interval, so total
 * offered load exceeds link capacity.
 */
export function runScenario(
  capacityBytesPerMs: number,
  flows: FlowSpec[],
  durationMs: number,
): ScenarioResult {
  const link = new BottleneckLink(capacityBytesPerMs);
  let nextId = 1;
  const nextArrival = new Map<FlowSpec, number>(flows.map((f) => [f, 0]));

  for (let t = 0; t < durationMs; t++) {
    for (const flow of flows) {
      if (t >= (nextArrival.get(flow) ?? 0)) {
        const packet: Packet = {
          id: nextId++,
          dscp: flow.dscp,
          sizeBytes: flow.sizeBytes,
          arrivalMs: t,
          flowId: flow.flowId,
        };
        link.arrive(packet);
        nextArrival.set(flow, t + flow.intervalMs);
      }
    }
    link.tick();
  }
  // Drain phase: let queued packets finish so latency stats are complete.
  while (link.scheduler.backlog > 0) link.tick();

  return { link, stats: link.statsByFlow(durationMs) };
}

/** Standard acceptance scenario: EF voice + AF video + BE bulk download. */
export function acceptanceScenario(durationMs = 10_000): ScenarioResult {
  return runScenario(
    187.5, // 1.5 Mbit/s bottleneck
    [
      // Voice: G.711, 160 B every 20 ms -> 64 kbit/s, marked EF.
      { flowId: 'voice-ef', dscp: DSCP.EF, sizeBytes: 160, intervalMs: 20 },
      // Video: 600 B every 10 ms -> 480 kbit/s, marked AF41.
      { flowId: 'video-af4', dscp: DSCP.AF41, sizeBytes: 600, intervalMs: 10 },
      // Bulk download: 1500 B every 2 ms -> 6 Mbit/s offered, marked BE.
      { flowId: 'bulk-be', dscp: DSCP.BE, sizeBytes: 1500, intervalMs: 2 },
    ],
    durationMs,
  );
}
