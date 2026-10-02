import { Disseminator } from './disseminator.ts';
import { MembershipTable } from './membership.ts';
import type { EventType, MemberEvent } from './types.ts';

export const MAX_PIGGYBACK_PER_PACKET = 6;

export class GossipNode {
  readonly id: string;
  incarnation = 0;
  readonly table = new MembershipTable();
  readonly disseminator: Disseminator;

  constructor(id: string, clusterSize: number) {
    this.id = id;
    this.disseminator = new Disseminator(clusterSize);
    this.table.apply({ type: 'join', nodeId: id, incarnation: 0 });
  }

  publish(type: EventType, nodeId = this.id, incarnation = this.incarnation): MemberEvent {
    const event: MemberEvent = { type, nodeId, incarnation };
    this.ingest(event);
    return event;
  }

  ingest(event: MemberEvent): void {
    if (this.table.apply(event)) {
      this.disseminator.add(event);
    }
  }

  buildPing(): MemberEvent[] {
    return this.disseminator.getPiggyback(MAX_PIGGYBACK_PER_PACKET);
  }

  handlePing(events: MemberEvent[]): MemberEvent[] {
    for (const event of events) this.ingest(event);
    return this.disseminator.getPiggyback(MAX_PIGGYBACK_PER_PACKET);
  }

  handleAck(events: MemberEvent[]): void {
    for (const event of events) this.ingest(event);
  }
}
