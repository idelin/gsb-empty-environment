import { BloomFilter } from './bloomFilter.ts';

/**
 * SSTable：不可变有序字符串表。键为定长散列串，字典序即区间序。
 * bloomFilter 为 null 表示该表的 Bloom Filter 被关闭（故障注入点）。
 */
export class SSTable {
  readonly id: number;
  readonly entries: ReadonlyMap<string, string>;
  readonly minKey: string;
  readonly maxKey: string;
  bloomFilter: BloomFilter | null;
  private readonly bitsPerKey: number;

  private constructor(
    id: number,
    entries: ReadonlyMap<string, string>,
    minKey: string,
    maxKey: string,
    bloomFilter: BloomFilter | null,
    bitsPerKey: number,
  ) {
    this.id = id;
    this.entries = entries;
    this.minKey = minKey;
    this.maxKey = maxKey;
    this.bloomFilter = bloomFilter;
    this.bitsPerKey = bitsPerKey;
  }

  static fromEntries(
    id: number,
    entries: Map<string, string>,
    buildFilter: boolean,
    bitsPerKey: number,
  ): SSTable {
    if (entries.size === 0) throw new Error('cannot build SSTable from empty entries');
    let minKey = '';
    let maxKey = '';
    for (const key of entries.keys()) {
      if (minKey === '' || key < minKey) minKey = key;
      if (maxKey === '' || key > maxKey) maxKey = key;
    }
    let filter: BloomFilter | null = null;
    if (buildFilter) {
      filter = new BloomFilter(entries.size, bitsPerKey);
      for (const key of entries.keys()) filter.add(key);
    }
    return new SSTable(id, entries, minKey, maxKey, filter, bitsPerKey);
  }

  get filterEnabled(): boolean {
    return this.bloomFilter !== null;
  }

  coversKey(key: string): boolean {
    return key >= this.minKey && key <= this.maxKey;
  }

  disableBloomFilter(): void {
    this.bloomFilter = null;
  }

  rebuildBloomFilter(bitsPerKey?: number): void {
    const bpk = bitsPerKey ?? this.bitsPerKey;
    const filter = new BloomFilter(this.entries.size, bpk);
    for (const key of this.entries.keys()) filter.add(key);
    this.bloomFilter = filter;
  }
}
