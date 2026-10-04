import type { Heartbeat, NodeId } from "./types.ts";

export class SimulatedNode {
  readonly id: NodeId;
  epoch = 1;
  seq = 0;
  up = true;
  paused = false;
  crashedAt: number | null = null;

  constructor(id: NodeId) {
    this.id = id;
  }

  get alive(): boolean {
    return this.up;
  }

  beat(now: number): Heartbeat | null {
    if (!this.up || this.paused) return null;
    return { nodeId: this.id, epoch: this.epoch, seq: this.seq++, sentAt: now };
  }

  pause(): void {
    this.paused = true;
  }

  resume(): void {
    this.paused = false;
  }

  crash(at: number): void {
    this.up = false;
    this.crashedAt = at;
  }

  restart(): void {
    this.up = true;
    this.paused = false;
    this.epoch++;
    this.seq = 0;
    this.crashedAt = null;
  }
}
