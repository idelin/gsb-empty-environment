import { BloomFilter } from './bloomFilter.ts';

export interface SSTableLookup {
  found: boolean;
  value: string | null;
  /** 是否真正读取了表数据（bloom 判否时跳过数据读取） */
  dataRead: boolean;
  /** 是否执行了 bloom 检查 */
  bloomChecked: boolean;
  /** bloom 误判：bloom 称"可能存在"但表中实际不存在 */
  bloomFalsePositive: boolean;
}

let nextTableId = 1;

/** 不可变有序字符串表（SSTable），可携带一个可选的 Bloom Filter。 */
export class SSTable {
  readonly id: number;
  readonly entries: Map<string, string>;
  private filter: BloomFilter | null;

  constructor(entries: Map<string, string>, bloomEnabled = true) {
    this.id = nextTableId++;
    this.entries = new Map([...entries].sort(([a], [b]) => (a < b ? -1 : 1)));
    this.filter = bloomEnabled ? new BloomFilter(this.entries.size, 0.01) : null;
    if (this.filter) {
      for (const key of this.entries.keys()) this.filter.add(key);
    }
  }

  get bloomEnabled(): boolean {
    return this.filter !== null;
  }

  /** 运行时开关 Bloom Filter（用于模拟"故意关闭部分 Filter"的场景）。 */
  setBloomEnabled(enabled: boolean): void {
    if (enabled && !this.filter) {
      this.filter = new BloomFilter(this.entries.size, 0.01);
      for (const key of this.entries.keys()) this.filter.add(key);
    } else if (!enabled) {
      this.filter = null;
    }
  }

  /** 重建 Bloom Filter（监控器建议的修复动作之一）。 */
  rebuildFilter(): void {
    this.setBloomEnabled(true);
  }

  lookup(key: string): SSTableLookup {
    if (this.filter) {
      if (!this.filter.mightContain(key)) {
        return { found: false, value: null, dataRead: false, bloomChecked: true, bloomFalsePositive: false };
      }
      const value = this.entries.get(key);
      if (value === undefined) {
        // Bloom 误判：付出了数据读取代价却未命中
        return { found: false, value: null, dataRead: true, bloomChecked: true, bloomFalsePositive: true };
      }
      return { found: true, value, dataRead: true, bloomChecked: true, bloomFalsePositive: false };
    }
    const value = this.entries.get(key);
    return {
      found: value !== undefined,
      value: value ?? null,
      dataRead: true,
      bloomChecked: false,
      bloomFalsePositive: false,
    };
  }

  get size(): number {
    return this.entries.size;
  }
}
