import { VirtualClock } from './clock.ts';
import type { SwimNode } from './swim.ts';

export interface PingMessage {
  type: 'ping';
  from: string;
  to: string;
  seq: number;
  /** Set when this ping is issued by a proxy on behalf of an originator. */
  onBehalfOf?: string;
}

export interface AckMessage {
  type: 'ack';
  from: string;
  to: string;
  seq: number;
  onBehalfOf?: string;
  /** Set on a proxy-relayed Ack to identify the probed target. */
  target?: string;
}

export interface PingReqMessage {
  type: 'ping-req';
  from: string;
  to: string;
  target: string;
  seq: number;
}

export type Message = PingMessage | AckMessage | PingReqMessage;

export interface LogEntry {
  at: number;
  message: Message;
  delivered: boolean;
}

/**
 * Simulated point-to-point network with configurable one-way latency and
 * directional link drops, used to model weak-network conditions in tests.
 */
export class SimulatedNetwork {
  readonly log: LogEntry[] = [];
  latencyMs: number;
  private readonly clock: VirtualClock;
  private readonly nodes = new Map<string, SwimNode>();
  private readonly dropped = new Set<string>();

  constructor(clock: VirtualClock, latencyMs = 1) {
    this.clock = clock;
    this.latencyMs = latencyMs;
  }

  register(node: SwimNode): void {
    this.nodes.set(node.id, node);
  }

  /** Drop all traffic between `a` and `b` in both directions. */
  dropLink(a: string, b: string): void {
    this.dropped.add(`${a}->${b}`);
    this.dropped.add(`${b}->${a}`);
  }

  send(message: Message): void {
    const lost =
      this.dropped.has(`${message.from}->${message.to}`) ||
      !this.nodes.has(message.to);
    this.log.push({ at: this.clock.now, message, delivered: !lost });
    if (lost) return;
    const target = this.nodes.get(message.to)!;
    this.clock.schedule(this.latencyMs, () => target.receive(message));
  }

  sentOfType<T extends Message['type']>(
    type: T,
  ): Extract<Message, { type: T }>[] {
    return this.log
      .map((entry) => entry.message)
      .filter((m): m is Extract<Message, { type: T }> => m.type === type);
  }
}
