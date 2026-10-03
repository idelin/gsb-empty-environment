import type { MemberState, MemberStatus } from './messages.ts';

/**
 * 两套确定性冲突解决规则：
 * - Gossip（分区期间）：保守。incarnation 高者胜；并列时 dead > suspect > alive，
 *   让失败信息在子网内扩散而不抖动（节点被误标时通过提升自身 incarnation 反驳）。
 * - Merge（愈合合并）：乐观。incarnation 高者胜；并列时 alive > suspect > dead，
 *   因为对方子网的心跳是“存活”的最新证据，合并应当恢复连通性。
 */
const GOSSIP_RANK: Record<MemberStatus, number> = { alive: 0, suspect: 1, dead: 2 };
const MERGE_RANK: Record<MemberStatus, number> = { dead: 0, suspect: 1, alive: 2 };

function resolveBy(a: MemberState, b: MemberState, rank: Record<MemberStatus, number>): MemberState {
  if (a.incarnation !== b.incarnation) return a.incarnation > b.incarnation ? a : b;
  return rank[a.status] >= rank[b.status] ? a : b;
}

export function resolveGossip(local: MemberState, incoming: MemberState): MemberState {
  return resolveBy(local, incoming, GOSSIP_RANK);
}

export function resolveMerge(a: MemberState, b: MemberState): MemberState {
  return resolveBy(a, b, MERGE_RANK);
}

export class Membership {
  private members = new Map<number, MemberState>();

  constructor(allIds: number[]) {
    for (const id of allIds) {
      this.members.set(id, { id, status: 'alive', incarnation: 0 });
    }
  }

  get(id: number): MemberState {
    const member = this.members.get(id);
    if (!member) throw new Error(`unknown member ${id}`);
    return member;
  }

  all(): MemberState[] {
    return [...this.members.values()]
      .sort((x, y) => x.id - y.id)
      .map((m) => ({ ...m }));
  }

  aliveIds(): number[] {
    return this.all()
      .filter((m) => m.status === 'alive')
      .map((m) => m.id);
  }

  deadIds(): number[] {
    return this.all()
      .filter((m) => m.status === 'dead')
      .map((m) => m.id);
  }

  aliveCount(): number {
    return this.aliveIds().length;
  }

  minAlive(): number {
    return this.aliveIds()[0] ?? 0;
  }

  mark(id: number, status: MemberStatus): boolean {
    const member = this.get(id);
    if (member.status === status) return false;
    member.status = status;
    return true;
  }

  /** 应用同域节点 gossip 来的成员摘要；自身被误标时提升 incarnation 反驳。 */
  applyDigest(digest: MemberState[], selfId: number): { changed: boolean; refuted: boolean } {
    let changed = false;
    let refuted = false;
    for (const entry of digest) {
      const local = this.members.get(entry.id);
      if (!local) {
        this.members.set(entry.id, { ...entry });
        changed = true;
        continue;
      }
      if (entry.id === selfId && entry.status !== 'alive' && entry.incarnation >= local.incarnation) {
        local.status = 'alive';
        local.incarnation = entry.incarnation + 1;
        changed = true;
        refuted = true;
        continue;
      }
      const winner = resolveGossip(local, entry);
      if (winner === entry && (local.status !== entry.status || local.incarnation !== entry.incarnation)) {
        local.status = entry.status;
        local.incarnation = entry.incarnation;
        changed = true;
      }
    }
    return { changed, refuted };
  }

  /** 合并提交：整体替换视图（幂等）。 */
  install(view: MemberState[]): void {
    this.members.clear();
    for (const entry of view) {
      this.members.set(entry.id, { ...entry });
    }
  }
}
