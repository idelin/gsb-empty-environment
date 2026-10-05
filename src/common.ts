import type { ChainEdge, ModuleSpec, Resolution, SourceChain, SymbolRef } from './types.ts';

export function refKey(ref: SymbolRef): string {
  return ref.module + '\0' + ref.name;
}

export function edgeKey(edge: ChainEdge): string {
  return (
    edge.from.module + '\0' + edge.from.name + '\0' + edge.via +
    '\0' + edge.to.module + '\0' + edge.to.name
  );
}

export function edgesKey(edges: readonly ChainEdge[]): string {
  return edges.map(edgeKey).join('\x01');
}

export function chainKey(chain: SourceChain): string {
  return edgesKey(chain.edges) + '\x01->' + refKey(chain.target);
}

/** Canonical order: shorter chains first, ties broken by serialized hops. */
export function compareChains(a: SourceChain, b: SourceChain): number {
  if (a.edges.length !== b.edges.length) return a.edges.length - b.edges.length;
  const ka = chainKey(a);
  const kb = chainKey(b);
  return ka < kb ? -1 : ka > kb ? 1 : 0;
}

/**
 * Outgoing resolution edges of (ref.module, ref.name): explicit imports bound
 * to that local name plus re-exports of that exported name. Deduped, sorted.
 */
export function outgoingEdges(spec: ModuleSpec, ref: SymbolRef): ChainEdge[] {
  const out: ChainEdge[] = [];
  const seen = new Set<string>();
  const push = (via: 'import' | 'reexport', to: SymbolRef): void => {
    const key = via + '\0' + refKey(to);
    if (seen.has(key)) return;
    seen.add(key);
    out.push({ from: ref, via, to });
  };
  for (const imp of spec.imports) {
    if (imp.local === ref.name) push('import', { module: imp.from, name: imp.imported });
  }
  for (const re of spec.reexports) {
    if (re.exported === ref.name) push('reexport', { module: re.from, name: re.imported });
  }
  out.sort((a, b) => (edgeKey(a) < edgeKey(b) ? -1 : edgeKey(a) > edgeKey(b) ? 1 : 0));
  return out;
}

export function resolutionKey(res: Resolution): string {
  switch (res.kind) {
    case 'undefined':
      return 'undefined';
    case 'defined':
      return `defined ${refKey(res.target)} <= ${edgesKey(res.chain.edges)}`;
    case 'conflict':
      return `conflict [${chainKey(res.chains[0])}] [${chainKey(res.chains[1])}]`;
    case 'cyclic-reexport':
      return `cyclic-reexport ${edgesKey(res.cycle)}`;
  }
}

export function pretty(key: string): string {
  return key.replace(/\0/g, '.').replace(/\x01/g, '  then  ');
}
