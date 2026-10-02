import { isDeliverable, merge, tick } from "./vectorClock.ts";
import type { NodeId, VectorClock } from "./vectorClock.ts";

export interface CausalMessage<T = unknown> {
  /** Id of the node that broadcast this message. */
  sender: NodeId;
  /** Per-sender sequence number (equals clock[sender]). */
  seq: number;
  /** Vector timestamp captured at send time; encodes all causal dependencies. */
  clock: VectorClock;
  payload: T;
}

export type DeliverHandler<T> = (msg: CausalMessage<T>) => void;

/**
 * Causal broadcast middleware for one node.
 *
 * - broadcast(): stamps the message with the local vector clock and delivers
 *   it to the local application (loopback); the caller transports the returned
 *   message to the other nodes.
 * - receive(): hands a message arriving from the network to the middleware.
 *   If every causal dependency has already been delivered, the message is
 *   delivered immediately; otherwise it is parked in an isolation buffer.
 *   Each delivery triggers a cascade that drains the buffer while more
 *   messages have become deliverable.
 */
export class CausalBroadcast<T = unknown> {
  readonly id: NodeId;
  private clock: VectorClock = {};
  private buffer: CausalMessage<T>[] = [];
  private deliveredLog: CausalMessage<T>[] = [];
  private onDeliver: DeliverHandler<T>;

  constructor(id: NodeId, onDeliver: DeliverHandler<T> = () => {}) {
    this.id = id;
    this.onDeliver = onDeliver;
  }

  /** Broadcast a payload to the group. Returns the message to transport. */
  broadcast(payload: T): CausalMessage<T> {
    this.clock = tick(this.clock, this.id);
    const msg: CausalMessage<T> = {
      sender: this.id,
      seq: this.clock[this.id],
      clock: { ...this.clock },
      payload,
    };
    this.deliver(msg); // loopback: always deliverable locally
    return msg;
  }

  /** Entry point for messages arriving from the network. */
  receive(msg: CausalMessage<T>): void {
    if (this.isDuplicate(msg)) return;
    if (isDeliverable(msg.clock, msg.sender, this.clock)) {
      this.deliver(msg);
    } else {
      // Causal dependencies not yet delivered: park in the isolation buffer.
      this.buffer.push(msg);
    }
  }

  /** Number of messages currently parked in the isolation buffer. */
  get bufferedCount(): number {
    return this.buffer.length;
  }

  /** All messages delivered to the application layer, in delivery order. */
  get delivered(): readonly CausalMessage<T>[] {
    return this.deliveredLog;
  }

  /** Current local vector clock (exposed for inspection/tests). */
  get vectorClock(): VectorClock {
    return { ...this.clock };
  }

  private isDuplicate(msg: CausalMessage<T>): boolean {
    if (msg.seq <= (this.clock[msg.sender] ?? 0)) return true;
    return this.buffer.some(
      (m) => m.sender === msg.sender && m.seq === msg.seq,
    );
  }

  private deliver(msg: CausalMessage<T>): void {
    this.clock = merge(this.clock, msg.clock);
    this.deliveredLog.push(msg);
    this.onDeliver(msg);
    this.drainBuffer();
  }

  /**
   * Cascade: repeatedly scan the isolation buffer and deliver every message
   * whose dependencies are now satisfied, until no further progress is made.
   */
  private drainBuffer(): void {
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (let i = 0; i < this.buffer.length; i++) {
        const candidate = this.buffer[i];
        if (isDeliverable(candidate.clock, candidate.sender, this.clock)) {
          this.buffer.splice(i, 1);
          this.clock = merge(this.clock, candidate.clock);
          this.deliveredLog.push(candidate);
          this.onDeliver(candidate);
          progressed = true;
          break;
        }
      }
    }
  }
}
