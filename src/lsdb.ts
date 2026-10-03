import type { LSA, NodeId } from './types.ts';

export type ApplyResult = 'applied' | 'duplicate' | 'stale';

export class Lsdb {
  private entries = new Map<NodeId, LSA>();

  apply(lsa: LSA): ApplyResult {
    const current = this.entries.get(lsa.originator);
    if (current) {
      if (lsa.seq < current.seq) return 'stale';
      if (lsa.seq === current.seq) return 'duplicate';
    }
    this.entries.set(lsa.originator, {
      originator: lsa.originator,
      seq: lsa.seq,
      links: lsa.links.map((l) => ({ ...l })),
    });
    return 'applied';
  }

  get(id: NodeId): LSA | undefined {
    return this.entries.get(id);
  }

  all(): LSA[] {
    return [...this.entries.values()];
  }

  hasLink(from: NodeId, to: NodeId): boolean {
    const lsa = this.entries.get(from);
    return lsa !== undefined && lsa.links.some((l) => l.neighbor === to);
  }
}
