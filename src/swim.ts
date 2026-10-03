import type { DetectorEvent, FaultEvent, NodeId } from './types.ts';

export interface SwimOptions {
  /** Consecutive missed probes before a member becomes suspected. */
  readonly suspicionThreshold: number;
  /** Cycles a member may remain suspected without an ack before it is marked dead. */
  readonly confirmCycles: number;
}

export const DEFAULT_SWIM_OPTIONS: SwimOptions = {
  suspicionThreshold: 1,
  confirmCycles: 2,
};

type MemberState = 'alive' | 'suspected' | 'dead';

interface MemberView {
  state: MemberState;
  missedProbes: number;
  suspectAge: number;
}

/**
 * A simulated cluster whose members answer probes according to the
 * injected ground-truth faults. Each member is probed once per cycle at
 * a fixed phase (its index divided by the cluster size), modelling the
 * round-robin probe schedule of SWIM.
 */
export class SwimCluster {
  readonly nodeIds: readonly NodeId[];
  private readonly crashes = new Map<NodeId, { cycle: number; phase: number }>();
  private readonly lossCycles = new Map<NodeId, Set<number>>();

  constructor(size: number) {
    this.nodeIds = Array.from(
      { length: size },
      (_, index) => `node-${String(index).padStart(2, '0')}`,
    );
  }

  applyFault(event: FaultEvent): void {
    if (event.type === 'node-crash') {
      this.crashes.set(event.nodeId, { cycle: event.cycle, phase: event.phase });
      return;
    }
    const cycles = this.lossCycles.get(event.nodeId) ?? new Set<number>();
    cycles.add(event.cycle);
    this.lossCycles.set(event.nodeId, cycles);
  }

  probePhase(nodeId: NodeId): number {
    return this.nodeIds.indexOf(nodeId) / this.nodeIds.length;
  }

  acksProbe(nodeId: NodeId, cycle: number): boolean {
    const crash = this.crashes.get(nodeId);
    if (
      crash !== undefined &&
      (cycle > crash.cycle || (cycle === crash.cycle && this.probePhase(nodeId) >= crash.phase))
    ) {
      return false;
    }
    return !this.lossCycles.get(nodeId)?.has(cycle);
  }
}

/**
 * The SWIM-style failure detector: probes every member it still believes
 * to be reachable once per cycle, suspects members whose missed probes
 * reach the suspicion threshold, refutes suspicions when an ack arrives
 * again (SWIM incarnation-style refutation), and marks members dead
 * after `confirmCycles` unrefuted suspected cycles.
 */
export class SwimDetector {
  private readonly views = new Map<NodeId, MemberView>();
  private readonly cluster: SwimCluster;
  private readonly options: SwimOptions;

  constructor(cluster: SwimCluster, options: SwimOptions = DEFAULT_SWIM_OPTIONS) {
    this.cluster = cluster;
    this.options = options;
    for (const nodeId of cluster.nodeIds) {
      this.views.set(nodeId, { state: 'alive', missedProbes: 0, suspectAge: 0 });
    }
  }

  stateOf(nodeId: NodeId): MemberState {
    const view = this.views.get(nodeId);
    if (view === undefined) throw new Error(`unknown node: ${nodeId}`);
    return view.state;
  }

  runCycle(cycle: number): DetectorEvent[] {
    const events: DetectorEvent[] = [];
    for (const nodeId of this.cluster.nodeIds) {
      const view = this.views.get(nodeId);
      if (view === undefined || view.state === 'dead') continue;

      if (this.cluster.acksProbe(nodeId, cycle)) {
        view.missedProbes = 0;
        if (view.state === 'suspected') {
          view.state = 'alive';
          view.suspectAge = 0;
          events.push({ cycle, nodeId, kind: 'refuted' });
        }
        continue;
      }

      view.missedProbes += 1;
      if (view.state === 'alive' && view.missedProbes >= this.options.suspicionThreshold) {
        view.state = 'suspected';
        view.suspectAge = 0;
        events.push({ cycle, nodeId, kind: 'suspected' });
        continue;
      }
      if (view.state === 'suspected') {
        view.suspectAge += 1;
        if (view.suspectAge >= this.options.confirmCycles) {
          view.state = 'dead';
          events.push({ cycle, nodeId, kind: 'marked-dead' });
        }
      }
    }
    return events;
  }
}

export function simulate(
  cluster: SwimCluster,
  detector: SwimDetector,
  totalCycles: number,
): DetectorEvent[] {
  const log: DetectorEvent[] = [];
  for (let cycle = 0; cycle < totalCycles; cycle += 1) {
    log.push(...detector.runCycle(cycle));
  }
  return log;
}
