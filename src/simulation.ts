import { Detector, type DetectorConfig, type DetectorTimer, defaultDetectorConfig } from "./detector.ts";
import { EventQueue } from "./eventQueue.ts";
import { Metrics, type MetricsSummary } from "./metrics.ts";
import { SimNode } from "./node.ts";
import type { DelaySampler } from "./random.ts";
import type { Heartbeat, NodeId } from "./types.ts";

export type SimPayload =
  | { type: "sendTick"; nodeId: NodeId }
  | { type: "heartbeatArrival"; heartbeat: Heartbeat }
  | { type: "detectorTimer"; timer: DetectorTimer }
  | { type: "pause"; nodeId: NodeId; durationMs: number }
  | { type: "crash"; nodeId: NodeId }
  | { type: "restart"; nodeId: NodeId };

export interface SimulationOptions {
  intervalMs?: number;
  delaySampler: DelaySampler;
  delaySamplerFor?: (nodeId: NodeId) => DelaySampler;
  detectorConfig?: Partial<DetectorConfig>;
}

export class Simulation {
  readonly metrics = new Metrics();
  readonly detector: Detector;
  readonly nodes = new Map<NodeId, SimNode>();

  private queue = new EventQueue<SimPayload>();
  private now = 0;
  private readonly intervalMs: number;
  private readonly delaySampler: DelaySampler;
  private readonly delaySamplerFor?: (nodeId: NodeId) => DelaySampler;
  private readonly samplers = new Map<NodeId, DelaySampler>();

  constructor(options: SimulationOptions) {
    this.intervalMs = options.intervalMs ?? 1000;
    this.delaySampler = options.delaySampler;
    this.delaySamplerFor = options.delaySamplerFor;
    const config: DetectorConfig = {
      ...defaultDetectorConfig,
      intervalMs: this.intervalMs,
      ...options.detectorConfig,
    };
    this.detector = new Detector(config, {
      schedule: (delayMs, timer) => {
        this.queue.push(this.now + delayMs, { type: "detectorTimer", timer });
      },
    });
  }

  addNode(id: NodeId): SimNode {
    const node = new SimNode(id);
    this.nodes.set(id, node);
    this.samplers.set(id, this.delaySamplerFor ? this.delaySamplerFor(id) : this.delaySampler);
    this.queue.push(this.intervalMs, { type: "sendTick", nodeId: id });
    return node;
  }

  schedulePause(nodeId: NodeId, atMs: number, durationMs: number): void {
    this.queue.push(atMs, { type: "pause", nodeId, durationMs });
  }

  scheduleCrash(nodeId: NodeId, atMs: number): void {
    this.queue.push(atMs, { type: "crash", nodeId });
  }

  scheduleRestart(nodeId: NodeId, atMs: number): void {
    this.queue.push(atMs, { type: "restart", nodeId });
  }

  injectHeartbeat(nodeId: NodeId, generation: number, sentAt: number, arriveAt: number): void {
    this.queue.push(arriveAt, {
      type: "heartbeatArrival",
      heartbeat: { nodeId, generation, sentAt },
    });
  }

  run(untilMs: number): MetricsSummary {
    for (;;) {
      const event = this.queue.pop();
      if (!event || event.time > untilMs) break;
      this.now = event.time;
      this.dispatch(event.payload);
      this.collectDetectorEvents();
    }
    return this.metrics.summary();
  }

  private dispatch(payload: SimPayload): void {
    switch (payload.type) {
      case "sendTick": {
        const node = this.nodes.get(payload.nodeId);
        if (!node) return;
        if (node.alive && !node.isPaused(this.now)) {
          const sampler = this.samplers.get(node.id) ?? this.delaySampler;
          const heartbeat: Heartbeat = {
            nodeId: node.id,
            generation: node.generation,
            sentAt: this.now,
          };
          this.queue.push(this.now + sampler(this.now), {
            type: "heartbeatArrival",
            heartbeat,
          });
        }
        this.queue.push(this.now + this.intervalMs, { type: "sendTick", nodeId: node.id });
        return;
      }
      case "heartbeatArrival": {
        this.detector.onHeartbeat(payload.heartbeat, this.now);
        return;
      }
      case "detectorTimer": {
        this.detector.onTimer(payload.timer, this.now);
        return;
      }
      case "pause": {
        const node = this.nodes.get(payload.nodeId);
        if (node) node.pausedUntil = this.now + payload.durationMs;
        return;
      }
      case "crash": {
        const node = this.nodes.get(payload.nodeId);
        if (node) {
          node.alive = false;
          node.pausedUntil = -Infinity;
          node.crashedAt = this.now;
        }
        return;
      }
      case "restart": {
        const node = this.nodes.get(payload.nodeId);
        if (node) {
          node.alive = true;
          node.generation += 1;
          node.pausedUntil = -Infinity;
        }
        return;
      }
    }
  }

  private collectDetectorEvents(): void {
    for (const event of this.detector.drainEvents()) {
      switch (event.kind) {
        case "suspected":
          this.metrics.suspicions += 1;
          break;
        case "recovered":
          this.metrics.falseSuspicions += 1;
          break;
        case "removed": {
          const node = this.nodes.get(event.nodeId);
          if (node && !node.alive) {
            this.metrics.trueRemovals += 1;
            if (node.crashedAt !== undefined) {
              this.metrics.recordDetectionLatency(event.time - node.crashedAt);
            }
          } else {
            this.metrics.falsePositives += 1;
          }
          break;
        }
        case "staleIgnored":
          this.metrics.staleHeartbeatsIgnored += 1;
          break;
        case "postRemoval":
          this.metrics.postRemovalHeartbeats += 1;
          break;
      }
    }
  }
}
