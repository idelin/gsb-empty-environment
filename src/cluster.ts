import { BackpressureError, CausalBroadcastNode } from "./node.ts";
import type { ReclaimInterval } from "./node.ts";
import { streamKey } from "./types.ts";
import type { Message, MessageId, PrefixVector } from "./types.ts";

export interface DeliveryRecord {
  at: string;
  id: MessageId;
  payload: unknown;
}

export interface ReclaimRecord extends ReclaimInterval {
  at: string;
}

export interface BackpressureRecord {
  at: string;
  id: MessageId;
}

export type ClusterEvent =
  | { type: "broadcast"; from: string; payload: unknown }
  | { type: "receive"; at: string; message: Message }
  | { type: "ack"; from: string; stream: string; counter: number }
  | { type: "switch"; member: string }
  | { type: "checkpoint" };

export class Cluster {
  readonly stagingLimit: number;
  readonly nodes = new Map<string, CausalBroadcastNode>();
  readonly deliveries: DeliveryRecord[] = [];
  readonly reclaims: ReclaimRecord[] = [];
  readonly backpressure: BackpressureRecord[] = [];
  readonly retired = new Set<string>();
  readonly log: ClusterEvent[] = [];

  private readonly acks = new Map<string, Map<string, number>>();
  private readonly finalCounter = new Map<string, number>();
  private lastStable = new Map<string, number>();

  constructor(members: readonly string[], stagingLimit = 16) {
    this.stagingLimit = stagingLimit;
    for (const member of members) {
      this.nodes.set(member, new CausalBroadcastNode(member, stagingLimit));
    }
  }

  node(member: string): CausalBroadcastNode {
    const node = this.nodes.get(member);
    if (!node) throw new Error(`unknown member ${member}`);
    return node;
  }

  get members(): string[] {
    return [...this.nodes.keys()];
  }

  apply(event: ClusterEvent): unknown {
    this.log.push(event);
    switch (event.type) {
      case "broadcast":
        return this.broadcast(event.from, event.payload);
      case "receive":
        return this.receive(event.at, event.message);
      case "ack":
        this.ack(event.from, event.stream, event.counter);
        return undefined;
      case "switch":
        this.switchEpoch(event.member);
        return undefined;
      case "checkpoint":
        return this.checkpoint();
    }
  }

  broadcast(from: string, payload: unknown): Message {
    const node = this.node(from);
    const message = node.createMessage(payload);
    this.recordDeliveries(from, node.receive(message));
    return message;
  }

  receive(at: string, message: Message): Message[] {
    const node = this.node(at);
    try {
      const delivered = node.receive(message);
      this.recordDeliveries(at, delivered);
      return delivered;
    } catch (err) {
      if (err instanceof BackpressureError) {
        this.backpressure.push({ at, id: message.id });
        return [];
      }
      throw err;
    }
  }

  ack(from: string, stream: string, counter: number): void {
    this.node(from);
    this.raiseAck(from, stream, counter);
  }

  switchEpoch(member: string): void {
    const sealed = this.node(member).switchEpoch();
    this.finalCounter.set(sealed.stream, sealed.finalCounter);
    this.checkRetirement(sealed.stream);
  }

  checkpoint(): Map<string, number> {
    const streams = new Set<string>();
    for (const stream of this.acks.keys()) streams.add(stream);
    for (const stream of this.finalCounter.keys()) streams.add(stream);
    for (const node of this.nodes.values()) {
      for (const stream of Object.keys(node.localPrefixVector())) streams.add(stream);
    }
    const stable = new Map<string, number>();
    for (const stream of streams) {
      let min = Number.POSITIVE_INFINITY;
      for (const member of this.nodes.keys()) {
        min = Math.min(min, this.acks.get(stream)?.get(member) ?? 0);
      }
      if (min > 0) stable.set(stream, min);
    }
    const retiredFinal = new Map<string, number>();
    for (const [stream, final] of this.finalCounter) {
      if (this.retired.has(stream)) retiredFinal.set(stream, final);
    }
    const stableVector: PrefixVector = Object.fromEntries(stable);
    for (const [member, node] of this.nodes) {
      for (const interval of node.reclaim(stableVector, retiredFinal)) {
        this.reclaims.push({ at: member, ...interval });
      }
    }
    this.lastStable = stable;
    return stable;
  }

  stablePrefix(): ReadonlyMap<string, number> {
    return this.lastStable;
  }

  private recordDeliveries(at: string, delivered: Message[]): void {
    const node = this.node(at);
    for (const message of delivered) {
      this.deliveries.push({ at, id: message.id, payload: message.payload });
      const stream = streamKey(message.id.node, message.id.epoch);
      this.raiseAck(at, stream, node.prefixOf(stream));
    }
  }

  private raiseAck(member: string, stream: string, counter: number): void {
    let perStream = this.acks.get(stream);
    if (!perStream) {
      perStream = new Map();
      this.acks.set(stream, perStream);
    }
    perStream.set(member, Math.max(perStream.get(member) ?? 0, counter));
    this.checkRetirement(stream);
  }

  private checkRetirement(stream: string): void {
    if (this.retired.has(stream)) return;
    const final = this.finalCounter.get(stream);
    if (final === undefined) return;
    const perStream = this.acks.get(stream);
    for (const member of this.nodes.keys()) {
      if ((perStream?.get(member) ?? 0) < final) return;
    }
    this.retired.add(stream);
  }
}
