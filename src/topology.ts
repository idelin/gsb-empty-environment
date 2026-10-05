import type { LSA } from './lsa.ts';

export function adjacencyFromLsdb(lsdb: Map<string, LSA>): Map<string, Map<string, number>> {
  const active = new Map<string, LSA>();
  for (const [origin, lsa] of lsdb) {
    if (!lsa.withdrawn) active.set(origin, lsa);
  }
  const adj = new Map<string, Map<string, number>>();
  for (const origin of active.keys()) adj.set(origin, new Map());
  for (const [origin, lsa] of active) {
    for (const link of lsa.links) {
      const peer = active.get(link.neighbor);
      if (!peer) continue;
      const mutual = peer.links.some((l) => l.neighbor === origin);
      if (!mutual) continue;
      adj.get(origin)!.set(link.neighbor, link.metric);
    }
  }
  return adj;
}

export function canonicalTopology(lsdb: Map<string, LSA>): string {
  const adj = adjacencyFromLsdb(lsdb);
  const lines: string[] = [];
  for (const origin of [...adj.keys()].sort()) {
    const peers = [...adj.get(origin)!.entries()]
      .sort((a, b) => (a[0] < b[0] ? -1 : 1))
      .map(([n, m]) => `${n}:${m}`)
      .join(',');
    lines.push(`${origin} -> ${peers}`);
  }
  return lines.join('\n');
}
