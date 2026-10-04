import type { NodeId } from "./types.ts";
import type { Transition } from "./monitor.ts";

export interface LatencyStats {
  count: number;
  avgMs: number;
  maxMs: number;
}

export class Metrics {
  falseSuspects = 0;
  falseRemovals = 0;
  recoveries = 0;
  rejoins = 0;
  missedDetections = 0;
  readonly detectionLatenciesMs: number[] = [];
  private pendingCrashes = new Map<NodeId, number[]>();

  noteCrash(id: NodeId, at: number): void {
    const list = this.pendingCrashes.get(id) ?? [];
    list.push(at);
    this.pendingCrashes.set(id, list);
  }

  noteRestart(id: NodeId): void {
    this.pendingCrashes.delete(id);
  }

  noteTransition(t: Transition, nodeAlive: boolean): void {
    if (t.to === "SUSPECTED") {
      if (nodeAlive) this.falseSuspects++;
      return;
    }
    if (t.to === "REMOVED") {
      if (nodeAlive) {
        this.falseRemovals++;
        return;
      }
      const list = this.pendingCrashes.get(t.nodeId);
      const crashAt = list?.shift();
      if (crashAt !== undefined) this.detectionLatenciesMs.push(t.at - crashAt);
      return;
    }
    if (t.to === "ALIVE" && t.from === "SUSPECTED") this.recoveries++;
    if (t.to === "ALIVE" && t.from === "REMOVED") this.rejoins++;
  }

  finish(aliveByNode: (id: NodeId) => boolean): void {
    for (const [id, crashes] of this.pendingCrashes) {
      if (!aliveByNode(id)) this.missedDetections += crashes.length;
    }
    this.pendingCrashes.clear();
  }

  latencyStats(): LatencyStats | null {
    const n = this.detectionLatenciesMs.length;
    if (n === 0) return null;
    const sum = this.detectionLatenciesMs.reduce((a, b) => a + b, 0);
    return { count: n, avgMs: sum / n, maxMs: Math.max(...this.detectionLatenciesMs) };
  }
}
