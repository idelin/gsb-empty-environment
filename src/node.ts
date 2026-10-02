import { PiggybackDisseminator } from "./disseminator.ts";
import {
  statusToEventType,
  type ChangeEvent,
  type MemberRecord,
} from "./events.ts";
import { MemberList } from "./memberlist.ts";

export type PacketKind = "ping" | "ack" | "join";

export interface Packet {
  kind: PacketKind;
  from: string;
  to: string;
  seq: number;
  piggyback: ChangeEvent[];
  snapshot?: MemberRecord[];
}

export interface NodeOptions {
  clusterSize?: number;
  lambda?: number;
  piggybackBudgetBytes?: number;
}

export const DEFAULT_PIGGYBACK_BUDGET_BYTES = 140;

export class Node {
  readonly members: MemberList;
  readonly gossip: PiggybackDisseminator;
  readonly id: string;
  private incarnation = 0;
  private seq = 0;
  private readonly budgetBytes: number;

  constructor(id: string, options: NodeOptions = {}) {
    this.id = id;
    this.members = new MemberList(id);
    this.gossip = new PiggybackDisseminator(
      options.clusterSize ?? 16,
      options.lambda,
    );
    this.budgetBytes =
      options.piggybackBudgetBytes ?? DEFAULT_PIGGYBACK_BUDGET_BYTES;
    this.members.apply({ type: "join", nodeId: id, incarnation: 0 });
  }

  setClusterSize(size: number): void {
    this.gossip.setClusterSize(size);
  }

  announceJoin(): void {
    this.gossip.queueEvent({
      type: "join",
      nodeId: this.id,
      incarnation: this.incarnation,
    });
  }

  leave(): ChangeEvent {
    const event: ChangeEvent = {
      type: "leave",
      nodeId: this.id,
      incarnation: this.incarnation,
    };
    this.members.apply(event);
    this.gossip.queueEvent(event);
    return event;
  }

  suspectPeer(nodeId: string): ChangeEvent | null {
    const record = this.members.get(nodeId);
    if (!record || record.status !== "alive") return null;
    const event: ChangeEvent = {
      type: "suspect",
      nodeId,
      incarnation: record.incarnation,
    };
    this.members.apply(event);
    this.gossip.queueEvent(event);
    return event;
  }

  makePing(to: string): Packet {
    this.seq += 1;
    return {
      kind: "ping",
      from: this.id,
      to,
      seq: this.seq,
      piggyback: this.gossip.getPiggyback(this.budgetBytes),
    };
  }

  makeJoin(to: string): Packet {
    this.seq += 1;
    return {
      kind: "join",
      from: this.id,
      to,
      seq: this.seq,
      piggyback: [
        { type: "join", nodeId: this.id, incarnation: this.incarnation },
      ],
    };
  }

  handlePacket(packet: Packet): Packet | null {
    this.receiveEvents(packet.piggyback);
    if (packet.snapshot) {
      for (const record of packet.snapshot) {
        this.members.apply({
          type: statusToEventType(record.status),
          nodeId: record.id,
          incarnation: record.incarnation,
        });
      }
    }
    if (packet.kind === "ack") return null;
    const ack: Packet = {
      kind: "ack",
      from: this.id,
      to: packet.from,
      seq: packet.seq,
      piggyback: this.gossip.getPiggyback(this.budgetBytes),
    };
    if (packet.kind === "join") {
      ack.snapshot = this.members.snapshot();
    }
    return ack;
  }

  private receiveEvents(events: ChangeEvent[]): void {
    for (const event of events) {
      if (event.nodeId === this.id) {
        this.refuteIfNeeded(event);
        continue;
      }
      if (this.members.apply(event)) {
        this.gossip.queueEvent(event);
      }
    }
  }

  private refuteIfNeeded(event: ChangeEvent): void {
    if (event.type === "leave") return;
    if (event.type !== "suspect" && event.incarnation > this.incarnation) {
      this.incarnation = event.incarnation;
    }
    if (event.type === "suspect" && event.incarnation >= this.incarnation) {
      this.incarnation = event.incarnation + 1;
      const alive: ChangeEvent = {
        type: "alive",
        nodeId: this.id,
        incarnation: this.incarnation,
      };
      this.members.apply(alive);
      this.gossip.queueEvent(alive);
    }
  }
}
