import { MinHeap } from "./heap.ts";
import { FailureDetector, type MonitorConfig } from "./monitor.ts";
import { Metrics } from "./metrics.ts";
import { SimulatedNode } from "./node.ts";
import type { NodeId, SimEvent } from "./types.ts";

interface Scheduled {
  at: number;
  order: number;
  ev: SimEvent;
}

export interface SimConfig {
  monitor: MonitorConfig;
  tickMs: number;
  delaySampler: (now: number) => number;
}

export class Simulation {
  now = 0;
  readonly detector: FailureDetector;
  readonly metrics = new Metrics();
  readonly nodes = new Map<NodeId, SimulatedNode>();
  private cfg: SimConfig;
  private heap = new MinHeap<Scheduled>((a, b) => a.at - b.at || a.order - b.order);
  private order = 0;

  constructor(cfg: SimConfig) {
    this.cfg = cfg;
    this.detector = new FailureDetector(cfg.monitor);
    this.detector.onTransition = (t) => this.metrics.noteTransition(t, this.isAlive(t.nodeId));
    this.schedule({ kind: "evaluate", at: cfg.tickMs });
  }

  addNode(id: NodeId): SimulatedNode {
    const node = new SimulatedNode(id);
    this.nodes.set(id, node);
    this.schedule({ kind: "send-heartbeat", at: 0, nodeId: id });
    return node;
  }

  isAlive(id: NodeId): boolean {
    return this.nodes.get(id)?.alive ?? false;
  }

  schedule(ev: SimEvent): void {
    this.heap.push({ at: ev.at, order: this.order++, ev });
  }

  run(untilMs: number): void {
    for (;;) {
      const next = this.heap.pop();
      if (!next || next.at > untilMs) {
        if (next) this.heap.push(next);
        break;
      }
      this.now = next.at;
      this.handle(next.ev);
    }
    this.now = untilMs;
  }

  finish(): void {
    this.metrics.finish((id) => this.isAlive(id));
  }

  private handle(ev: SimEvent): void {
    switch (ev.kind) {
      case "send-heartbeat": {
        const node = this.nodes.get(ev.nodeId);
        if (!node) break;
        const hb = node.beat(this.now);
        if (hb) {
          this.schedule({ kind: "deliver-heartbeat", at: this.now + this.cfg.delaySampler(this.now), hb });
        }
        this.schedule({
          kind: "send-heartbeat",
          at: ev.at + this.cfg.monitor.heartbeatIntervalMs,
          nodeId: ev.nodeId,
        });
        break;
      }
      case "deliver-heartbeat":
        this.detector.onHeartbeat(ev.hb, this.now);
        break;
      case "evaluate":
        this.detector.evaluate(this.now);
        this.schedule({ kind: "evaluate", at: ev.at + this.cfg.tickMs });
        break;
      case "pause-node":
        this.nodes.get(ev.nodeId)?.pause();
        break;
      case "resume-node":
        this.nodes.get(ev.nodeId)?.resume();
        break;
      case "crash-node": {
        const node = this.nodes.get(ev.nodeId);
        if (node) {
          node.crash(this.now);
          this.metrics.noteCrash(ev.nodeId, this.now);
        }
        break;
      }
      case "restart-node": {
        const node = this.nodes.get(ev.nodeId);
        if (node) {
          node.restart();
          this.metrics.noteRestart(ev.nodeId);
        }
        break;
      }
    }
  }
}
