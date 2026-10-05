export interface LinkEnd {
  neighbor: string;
  metric: number;
}

export interface LSA {
  origin: string;
  generation: number;
  version: number;
  withdrawn: boolean;
  links: LinkEnd[];
}

export interface LSASummary {
  origin: string;
  generation: number;
  version: number;
  withdrawn: boolean;
  digest: string;
}

export function compareFreshness(
  a: { generation: number; version: number },
  b: { generation: number; version: number },
): number {
  if (a.generation !== b.generation) return a.generation - b.generation;
  return a.version - b.version;
}

export function canonicalLinks(links: LinkEnd[]): LinkEnd[] {
  return [...links].sort((x, y) =>
    x.neighbor === y.neighbor ? x.metric - y.metric : x.neighbor < y.neighbor ? -1 : 1,
  );
}

export function canonicalLsa(lsa: LSA): string {
  const links = canonicalLinks(lsa.links)
    .map((l) => `${l.neighbor}:${l.metric}`)
    .join(',');
  return `${lsa.origin}|${lsa.generation}|${lsa.version}|${lsa.withdrawn ? 1 : 0}|${links}`;
}

export function fnv1a(input: string): string {
  let h = 0x811c9dc5;
  for (let i = 0; i < input.length; i++) {
    h ^= input.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return (h >>> 0).toString(16).padStart(8, '0');
}

export function digestOf(lsa: LSA): string {
  return fnv1a(canonicalLsa(lsa));
}

export function summarize(lsa: LSA): LSASummary {
  return {
    origin: lsa.origin,
    generation: lsa.generation,
    version: lsa.version,
    withdrawn: lsa.withdrawn,
    digest: digestOf(lsa),
  };
}
