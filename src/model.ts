export type ReaderId = string;
export type SourceId = string;

export interface SourceInfo {
  readonly id: SourceId;
  readonly readers: ReadonlySet<ReaderId>;
  readonly derivedFrom: readonly SourceId[];
}

export class SourceRegistry {
  private readonly sources = new Map<SourceId, SourceInfo>();

  addSource(
    id: SourceId,
    readers: Iterable<ReaderId>,
    derivedFrom: readonly SourceId[] = [],
  ): SourceInfo {
    const readerSet = new Set(readers);
    const existing = this.sources.get(id);
    if (existing) {
      const sameReaders =
        existing.readers.size === readerSet.size &&
        [...readerSet].every((r) => existing.readers.has(r));
      const sameOrigin =
        existing.derivedFrom.length === derivedFrom.length &&
        existing.derivedFrom.every((d, i) => d === derivedFrom[i]);
      if (!sameReaders || !sameOrigin) {
        throw new Error(`source "${id}" already registered with different permissions`);
      }
      return existing;
    }
    const info: SourceInfo = { id, readers: readerSet, derivedFrom: [...derivedFrom] };
    this.sources.set(id, info);
    return info;
  }

  has(id: SourceId): boolean {
    return this.sources.has(id);
  }

  get(id: SourceId): SourceInfo {
    const info = this.sources.get(id);
    if (!info) throw new Error(`unknown source "${id}"`);
    return info;
  }

  readersOf(id: SourceId): ReadonlySet<ReaderId> {
    return this.get(id).readers;
  }

  sourceIds(): SourceId[] {
    return [...this.sources.keys()];
  }

  allReaders(): Set<ReaderId> {
    const all = new Set<ReaderId>();
    for (const info of this.sources.values()) {
      for (const reader of info.readers) all.add(reader);
    }
    return all;
  }
}

export function normalizeSources(sources: Iterable<SourceId>): SourceId[] {
  return [...new Set(sources)].sort();
}

export function sameSources(
  a: readonly SourceId[],
  b: readonly SourceId[],
): boolean {
  return a.length === b.length && a.every((s, i) => s === b[i]);
}

/**
 * 计算携带来源集合 `sources` 的字符对哪些读者可见。
 * 返回 null 表示不受限（无来源，对所有人可见）。
 */
export function effectiveReaders(
  registry: SourceRegistry,
  sources: readonly SourceId[],
): Set<ReaderId> | null {
  if (sources.length === 0) return null;
  let acc: Set<ReaderId> | null = null;
  for (const id of sources) {
    const readers = registry.readersOf(id);
    acc = acc === null ? new Set(readers) : new Set([...acc].filter((r) => readers.has(r)));
  }
  return acc;
}

export interface CharCell {
  readonly ch: string;
  readonly sources: readonly SourceId[];
}
