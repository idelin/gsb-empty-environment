import type { HealthState, Heartbeat, NodeId } from "./types.ts";
import { SampleWindow } from "./window.ts";

export interface MonitorConfig {
  heartbeatIntervalMs: number;
  suspectThreshold: number;
  windowCapacity: number;
  marginK: number;
  minSuspectTimeoutMs: number;
  maxSuspectTimeoutMs: number;
}

export interface Transition {
  nodeId: NodeId;
  from: HealthState;
  to: HealthState;
  at: number;
}

interface NodeRecord {
  state: HealthState;
  epoch: number;
  lastRecvAt: number;
  suspectSince: number;
  removedAt: number;
  window: SampleWindow;
}

export class FailureDetector {
  readonly transitions: Transition[] = [];
  onTransition: ((t: Transition) => void) | null = null;
  staleIgnored = 0;
  private records = new Map<NodeId, NodeRecord>();
  private cfg: MonitorConfig;

  constructor(cfg: MonitorConfig) {
    this.cfg = cfg;
  }

  stateOf(id: NodeId): HealthState | "UNKNOWN" {
    return this.records.get(id)?.state ?? "UNKNOWN";
  }

  suspectTimeoutMs(id: NodeId): number | null {
    const rec = this.records.get(id);
    return rec ? this.timeoutFor(rec) : null;
  }

  private timeoutFor(rec: NodeRecord): number {
    const est =
      rec.window.size === 0
        ? 3 * this.cfg.heartbeatIntervalMs
        : this.cfg.heartbeatIntervalMs + rec.window.mean + this.cfg.marginK * rec.window.stddev;
    return Math.min(this.cfg.maxSuspectTimeoutMs, Math.max(this.cfg.minSuspectTimeoutMs, est));
  }

  private emit(t: Transition): void {
    this.transitions.push(t);
    this.onTransition?.(t);
  }

  onHeartbeat(hb: Heartbeat, now: number): void {
    const rec = this.records.get(hb.nodeId);

    if (!rec || hb.epoch > rec.epoch) {
      const from = rec?.state;
      this.records.set(hb.nodeId, {
        state: "ALIVE",
        epoch: hb.epoch,
        lastRecvAt: now,
        suspectSince: 0,
        removedAt: 0,
        window: new SampleWindow(this.cfg.windowCapacity),
      });
      if (from !== undefined && from !== "ALIVE") {
        this.emit({ nodeId: hb.nodeId, from, to: "ALIVE", at: now });
      }
      return;
    }

    if (hb.epoch < rec.epoch) {
      this.staleIgnored++;
      return;
    }

    if (rec.state === "REMOVED") {
      if (hb.sentAt <= rec.removedAt) {
        this.staleIgnored++;
        return;
      }
      rec.state = "ALIVE";
      rec.lastRecvAt = now;
      rec.window.add(now - hb.sentAt);
      this.emit({ nodeId: hb.nodeId, from: "REMOVED", to: "ALIVE", at: now });
      return;
    }

    rec.window.add(now - hb.sentAt);
    rec.lastRecvAt = now;
    if (rec.state === "SUSPECTED") {
      rec.state = "ALIVE";
      this.emit({ nodeId: hb.nodeId, from: "SUSPECTED", to: "ALIVE", at: now });
    }
  }

  evaluate(now: number): void {
    for (const [id, rec] of this.records) {
      if (rec.state === "ALIVE") {
        const missed = Math.floor((now - rec.lastRecvAt) / this.cfg.heartbeatIntervalMs);
        if (missed >= this.cfg.suspectThreshold) {
          rec.state = "SUSPECTED";
          rec.suspectSince = now;
          this.emit({ nodeId: id, from: "ALIVE", to: "SUSPECTED", at: now });
        }
      } else if (rec.state === "SUSPECTED") {
        if (now - rec.suspectSince >= this.timeoutFor(rec)) {
          rec.state = "REMOVED";
          rec.removedAt = now;
          this.emit({ nodeId: id, from: "SUSPECTED", to: "REMOVED", at: now });
        }
      }
    }
  }
}
