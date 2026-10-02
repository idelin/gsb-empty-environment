import { SSTable } from './sstable.ts';

export interface GetTrace {
  key: string;
  found: boolean;
  /** 本次 Get 实际读取数据的 SSTable 数量（读放大分子） */
  tablesRead: number;
  bloomChecks: number;
  bloomFalsePositives: number;
}

export interface EngineOptions {
  /** MemTable 达到该条目数后 flush 为 SSTable */
  memtableFlushThreshold?: number;
  bloomEnabled?: boolean;
  /** 每次 compaction 合并的 SSTable 数量（compaction 权重） */
  compactionFanIn?: number;
  /** SSTable 数量超过该值时触发 compaction */
  compactionTrigger?: number;
}

type GetListener = (trace: GetTrace) => void;

/**
 * 极简 LSM 存储引擎：MemTable + L0 SSTable 列表（新→旧）。
 * Get 路径：MemTable → 逐表查找，逐表产生读放大与 Bloom 指标。
 */
export class LsmEngine {
  private memtable = new Map<string, string>();
  private tables: SSTable[] = []; // index 0 = 最新
  private readonly flushThreshold: number;
  private bloomEnabled: boolean;
  /** compaction 权重：单次合并的表数量，可由监控器建议调高 */
  compactionFanIn: number;
  compactionTrigger: number;
  private listeners: GetListener[] = [];

  constructor(options: EngineOptions = {}) {
    this.flushThreshold = options.memtableFlushThreshold ?? 64;
    this.bloomEnabled = options.bloomEnabled ?? true;
    this.compactionFanIn = options.compactionFanIn ?? 2;
    this.compactionTrigger = options.compactionTrigger ?? 8;
  }

  onGet(listener: GetListener): void {
    this.listeners.push(listener);
  }

  put(key: string, value: string): void {
    this.memtable.set(key, value);
    if (this.memtable.size >= this.flushThreshold) this.flush();
  }

  flush(): SSTable | null {
    if (this.memtable.size === 0) return null;
    const table = new SSTable(this.memtable, this.bloomEnabled);
    this.memtable = new Map();
    this.tables.unshift(table);
    if (this.tables.length > this.compactionTrigger) this.compact();
    return table;
  }

  /** 合并最旧的 compactionFanIn 张表为一张（压读放大）。 */
  compact(): void {
    if (this.tables.length < 2) return;
    const fanIn = Math.min(this.compactionFanIn, this.tables.length);
    const victims = this.tables.splice(this.tables.length - fanIn, fanIn);
    const merged = new Map<string, string>();
    // 旧表先写，新表覆盖，保证最新值胜出
    for (let i = victims.length - 1; i >= 0; i--) {
      for (const [k, v] of victims[i].entries) merged.set(k, v);
    }
    this.tables.push(new SSTable(merged, this.bloomEnabled));
  }

  get(key: string): string | null {
    const trace: GetTrace = {
      key,
      found: false,
      tablesRead: 0,
      bloomChecks: 0,
      bloomFalsePositives: 0,
    };
    const memHit = this.memtable.get(key);
    if (memHit !== undefined) {
      trace.found = true;
      this.emit(trace);
      return memHit;
    }
    for (const table of this.tables) {
      const r = table.lookup(key);
      if (r.dataRead) trace.tablesRead++;
      if (r.bloomChecked) trace.bloomChecks++;
      if (r.bloomFalsePositive) trace.bloomFalsePositives++;
      if (r.found) {
        trace.found = true;
        this.emit(trace);
        return r.value;
      }
    }
    this.emit(trace);
    return null;
  }

  private emit(trace: GetTrace): void {
    for (const l of this.listeners) l(trace);
  }

  /** 故意关闭部分 SSTable 的 Bloom Filter（故障注入 / 验收场景）。 */
  disableBloomOnSome(predicate: (table: SSTable, index: number) => boolean): number {
    let count = 0;
    this.tables.forEach((t, i) => {
      if (predicate(t, i)) {
        t.setBloomEnabled(false);
        count++;
      }
    });
    return count;
  }

  rebuildAllFilters(): void {
    for (const t of this.tables) t.rebuildFilter();
  }

  get sstableCount(): number {
    return this.tables.length;
  }

  get sstables(): readonly SSTable[] {
    return this.tables;
  }
}
