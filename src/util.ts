import { MemberInfo, NodeId } from './types';

export function digestOf(epoch: number, members: ReadonlyMap<NodeId, MemberInfo>): string {
  const parts = [...members.values()]
    .sort((a, b) => a.id - b.id)
    .map((m) => `${m.id}:${m.incarnation}:${m.status}`);
  return `${epoch}|${parts.join(',')}`;
}

export function digestOfSnapshot(view: { epoch: number; members: MemberInfo[] }): string {
  const map = new Map<NodeId, MemberInfo>(view.members.map((m) => [m.id, m]));
  return digestOf(view.epoch, map);
}

const STATUS_RANK: Record<string, number> = { alive: 0, suspect: 1, dead: 2 };

/**
 * SWIM-style precedence: higher incarnation always wins; at equal
 * incarnation the more pessimistic status (dead > suspect > alive) wins.
 */
export function resolveMember(a: MemberInfo, b: MemberInfo): MemberInfo {
  if (a.incarnation !== b.incarnation) {
    return { ...(a.incarnation > b.incarnation ? a : b) };
  }
  const winner = STATUS_RANK[a.status] >= STATUS_RANK[b.status] ? a : b;
  return { ...winner, statusChangedAt: Math.max(a.statusChangedAt, b.statusChangedAt) };
}

export function mergeMembers(
  base: ReadonlyMap<NodeId, MemberInfo>,
  incoming: readonly MemberInfo[],
): Map<NodeId, MemberInfo> {
  const merged = new Map<NodeId, MemberInfo>();
  for (const m of base.values()) merged.set(m.id, { ...m });
  for (const m of incoming) {
    const existing = merged.get(m.id);
    merged.set(m.id, existing ? resolveMember(existing, m) : { ...m });
  }
  return merged;
}

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
