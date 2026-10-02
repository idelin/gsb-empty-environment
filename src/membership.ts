import type { MemberMeta, NodeId } from './types.ts';

export class MembershipTable {
  private readonly members = new Map<NodeId, MemberMeta>();
  private readonly selfId: NodeId;

  constructor(selfId: NodeId) {
    this.selfId = selfId;
  }

  upsert(meta: MemberMeta): boolean {
    const existing = this.members.get(meta.id);
    if (existing) {
      if (existing.heartbeat > meta.heartbeat) return false;
      if (existing.heartbeat === meta.heartbeat && existing.state === meta.state) return false;
    }
    this.members.set(meta.id, { ...meta });
    return true;
  }

  merge(incoming: MemberMeta[]): number {
    let learned = 0;
    for (const meta of incoming) {
      if (meta.id === this.selfId) continue;
      if (this.upsert(meta)) learned += 1;
    }
    return learned;
  }

  get(id: NodeId): MemberMeta | undefined {
    return this.members.get(id);
  }

  has(id: NodeId): boolean {
    return this.members.has(id);
  }

  list(): MemberMeta[] {
    return [...this.members.values()].map((meta) => ({ ...meta }));
  }

  alive(): MemberMeta[] {
    return this.list().filter((meta) => meta.state === 'alive');
  }

  get size(): number {
    return this.members.size;
  }
}
