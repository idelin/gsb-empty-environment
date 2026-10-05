import { BoundedHistory } from "./history.ts";
import type { Heartbeat, NodeId, NodePhase } from "./types.ts";

export interface DetectorConfig {
  intervalMs: number;
  missThreshold: number;
  historyCapacity: number;
  windowBaseIntervals: number;
  windowStdMultiplier: number;
  minWindowMs: number;
  maxWindowMs: number;
  adaptive: boolean;
  fixedWindowMs: number;
}

export const defaultDetectorConfig: DetectorConfig = {
  intervalMs: 1000,
  missThreshold: 3,
  historyCapacity: 32,
  windowBaseIntervals: 1,
  windowStdMultiplier: 3,
  minWindowMs: 500,
  maxWindowMs: 30000,
  adaptive: true,
  fixedWindowMs: 1000,
};

export type DetectorTimer =
  | { type: "checkpoint"; nodeId: NodeId; seq: number }
  | { type: "removal"; nodeId: NodeId; token: number };

export type DetectorEvent =
  | { kind: "suspected"; nodeId: NodeId; time: number; missed: number }
  | { kind: "recovered"; nodeId: NodeId; time: number; suspectDurationMs: number }
  | { kind: "removed"; nodeId: NodeId; time: number; windowMs: number }
  | { kind: "staleIgnored"; nodeId: NodeId; time: number; generation: number; currentGeneration: number }
  | { kind: "postRemoval"; nodeId: NodeId; time: number };

interface TrackedNode {
  phase: NodePhase;
  generation: number;
  missed: number;
  arrivalsSinceCheckpoint: number;
  checkpointSeq: number;
  removalToken: number;
  suspectedAt: number;
  pendingWindowMs: number;
  delayHistory: BoundedHistory;
}

export interface Scheduler {
  schedule(delayMs: number, timer: DetectorTimer): void;
}

export class Detector {
  private nodes = new Map<NodeId, TrackedNode>();
  private events: DetectorEvent[] = [];
  private config: DetectorConfig;
  private scheduler: Scheduler;

  constructor(config: DetectorConfig, scheduler: Scheduler) {
    this.config = config;
    this.scheduler = scheduler;
  }

  drainEvents(): DetectorEvent[] {
    const out = this.events;
    this.events = [];
    return out;
  }

  phaseOf(nodeId: NodeId): NodePhase | undefined {
    return this.nodes.get(nodeId)?.phase;
  }

  currentWindowMs(nodeId: NodeId): number {
    const node = this.nodes.get(nodeId);
    return node ? this.computeWindow(node) : 0;
  }

  onHeartbeat(hb: Heartbeat, now: number): void {
    let node = this.nodes.get(hb.nodeId);
    if (node && node.phase === "removed") {
      this.events.push({ kind: "postRemoval", nodeId: hb.nodeId, time: now });
      return;
    }
    if (!node) {
      node = this.freshNode(hb.generation);
      this.nodes.set(hb.nodeId, node);
      this.scheduler.schedule(this.config.intervalMs, {
        type: "checkpoint",
        nodeId: hb.nodeId,
        seq: node.checkpointSeq,
      });
    }
    if (hb.generation < node.generation) {
      this.events.push({
        kind: "staleIgnored",
        nodeId: hb.nodeId,
        time: now,
        generation: hb.generation,
        currentGeneration: node.generation,
      });
      return;
    }
    if (hb.generation > node.generation) {
      node.generation = hb.generation;
    }
    node.delayHistory.add(Math.max(0, now - hb.sentAt));
    node.arrivalsSinceCheckpoint += 1;
    node.missed = 0;
    if (node.phase === "suspected") {
      node.phase = "alive";
      node.removalToken += 1;
      this.events.push({
        kind: "recovered",
        nodeId: hb.nodeId,
        time: now,
        suspectDurationMs: now - node.suspectedAt,
      });
    }
  }

  onTimer(timer: DetectorTimer, now: number): void {
    const node = this.nodes.get(timer.nodeId);
    if (!node || node.phase === "removed") return;
    if (timer.type === "checkpoint") {
      if (timer.seq !== node.checkpointSeq) return;
      if (node.arrivalsSinceCheckpoint === 0) {
        node.missed += 1;
      } else {
        node.missed = 0;
      }
      node.arrivalsSinceCheckpoint = 0;
      if (node.missed >= this.config.missThreshold && node.phase === "alive") {
        node.phase = "suspected";
        node.suspectedAt = now;
        node.removalToken += 1;
        node.pendingWindowMs = this.computeWindow(node);
        this.events.push({ kind: "suspected", nodeId: timer.nodeId, time: now, missed: node.missed });
        this.scheduler.schedule(node.pendingWindowMs, {
          type: "removal",
          nodeId: timer.nodeId,
          token: node.removalToken,
        });
      }
      node.checkpointSeq += 1;
      this.scheduler.schedule(this.config.intervalMs, {
        type: "checkpoint",
        nodeId: timer.nodeId,
        seq: node.checkpointSeq,
      });
      return;
    }
    if (timer.token !== node.removalToken) return;
    if (node.phase !== "suspected") return;
    node.phase = "removed";
    this.events.push({
      kind: "removed",
      nodeId: timer.nodeId,
      time: now,
      windowMs: node.pendingWindowMs,
    });
  }

  private freshNode(generation: number): TrackedNode {
    return {
      phase: "alive",
      generation,
      missed: 0,
      arrivalsSinceCheckpoint: 1,
      checkpointSeq: 0,
      removalToken: 0,
      suspectedAt: 0,
      pendingWindowMs: 0,
      delayHistory: new BoundedHistory(this.config.historyCapacity),
    };
  }

  private computeWindow(node: TrackedNode): number {
    const cfg = this.config;
    if (!cfg.adaptive) return cfg.fixedWindowMs;
    const history = node.delayHistory;
    if (history.count === 0) return cfg.minWindowMs;
    const estimate =
      cfg.windowBaseIntervals * cfg.intervalMs +
      history.mean +
      cfg.windowStdMultiplier * history.stddev;
    return Math.min(cfg.maxWindowMs, Math.max(cfg.minWindowMs, estimate));
  }
}
