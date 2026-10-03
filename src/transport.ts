import type { Message } from "./types.ts";
import type { Scheduler } from "./scheduler.ts";

export interface Endpoint {
  readonly id: string;
  isOnline(): boolean;
  receive(from: string, msg: Message): void;
}

/**
 * Simulated network on top of the discrete-event scheduler.
 * Messages are delivered after a fixed latency; a message is dropped
 * if the sender or the receiver is crashed at delivery time.
 */
export class Transport {
  private endpoints = new Map<string, Endpoint>();
  readonly delivered: Array<{ from: string; to: string; msg: Message }> = [];
  private readonly scheduler: Scheduler;
  private readonly latency: number;

  constructor(scheduler: Scheduler, latency = 1) {
    this.scheduler = scheduler;
    this.latency = latency;
  }

  register(endpoint: Endpoint): void {
    this.endpoints.set(endpoint.id, endpoint);
  }

  send(from: string, to: string, msg: Message): void {
    const sender = this.endpoints.get(from);
    if (!sender || !sender.isOnline()) return;
    this.scheduler.schedule(this.latency, () => {
      // in-flight message is lost if the sender crashed meanwhile
      if (!sender.isOnline()) return;
      const target = this.endpoints.get(to);
      if (!target || !target.isOnline()) return;
      this.delivered.push({ from, to, msg });
      target.receive(from, msg);
    });
  }
}
