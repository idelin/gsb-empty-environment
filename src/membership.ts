import type { MemberEvent, MemberRecord, NodeStatus } from './types.ts';

export class MembershipTable {
  private members: Map<string, MemberRecord> = new Map();

  apply(event: MemberEvent): boolean {
    const existing = this.members.get(event.nodeId);
    switch (event.type) {
      case 'join':
      case 'alive': {
        if (existing && existing.incarnation > event.incarnation) return false;
        if (
          existing &&
          existing.incarnation === event.incarnation &&
          existing.status !== 'suspect'
        ) {
          return false;
        }
        this.members.set(event.nodeId, {
          nodeId: event.nodeId,
          status: 'alive',
          incarnation: event.incarnation,
        });
        return true;
      }
      case 'suspect': {
        if (!existing || existing.status !== 'alive') return false;
        if (event.incarnation < existing.incarnation) return false;
        this.members.set(event.nodeId, { ...existing, status: 'suspect' });
        return true;
      }
      case 'leave': {
        if (existing && existing.status === 'dead') return false;
        this.members.set(event.nodeId, {
          nodeId: event.nodeId,
          status: 'dead',
          incarnation: event.incarnation,
        });
        return true;
      }
    }
  }

  get(nodeId: string): MemberRecord | undefined {
    return this.members.get(nodeId);
  }

  statusOf(nodeId: string): NodeStatus | undefined {
    return this.members.get(nodeId)?.status;
  }

  get size(): number {
    return this.members.size;
  }

  entries(): MemberRecord[] {
    return [...this.members.values()];
  }
}
