import {
  eventToStatus,
  shouldApply,
  type ChangeEvent,
  type MemberRecord,
  type MemberStatus,
} from "./events.ts";

export class MemberList {
  private readonly members = new Map<string, MemberRecord>();
  readonly selfId: string;

  constructor(selfId: string) {
    this.selfId = selfId;
  }

  apply(event: ChangeEvent): boolean {
    const current = this.members.get(event.nodeId);
    if (!shouldApply(current, event)) return false;
    this.members.set(event.nodeId, {
      id: event.nodeId,
      status: eventToStatus(event.type),
      incarnation: event.incarnation,
    });
    return true;
  }

  get(id: string): MemberRecord | undefined {
    return this.members.get(id);
  }

  statusOf(id: string): MemberStatus | undefined {
    return this.members.get(id)?.status;
  }

  get size(): number {
    return this.members.size;
  }

  aliveIds(): string[] {
    return this.snapshot()
      .filter((m) => m.status === "alive")
      .map((m) => m.id);
  }

  snapshot(): MemberRecord[] {
    return [...this.members.values()];
  }
}
