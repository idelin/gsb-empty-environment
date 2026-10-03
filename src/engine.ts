import { SSTable } from './sstable.ts';
import { LsmDiagnosticsMonitor, type Alert, type GetSample } from './monitor.ts';

export interface EngineOptions {
  /** 每个键分配的 Bloom Filter 位数，默认 10 */
  bitsPerKey?: number;
  monitor?: LsmDiagnosticsMonitor;
}

export interface GetTrace extends GetSample {
  key: string;
  /** 因键区间不覆盖而跳过的 SSTable 数 */
  skippedByRange: number;
}

export interface GetResult {
  value: string | undefined;
  trace: GetTrace;
}

/**
 * 简化 LSM 存储引擎：MemTable + 新→旧排列的 L0 SSTable 列表。
 * Get 路径逐表探测：区间过滤 → Bloom Filter → 实际读取，全程记录追踪指标。
 */
export class LsmEngine {
  private memtable = new Map<string, string>();
  private tables: SSTable[] = []; // 索引 0 为最新
  private nextTableId = 1;
  private pendingAlerts: Alert[] = [];
  readonly bitsPerKey: number;
  private readonly monitor?: LsmDiagnosticsMonitor;

  constructor(options: EngineOptions = {}) {
    this.bitsPerKey = options.bitsPerKey ?? 10;
    this.monitor = options.monitor;
  }

  put(key: string, value: string): void {
    this.memtable.set(key, value);
  }

  /** 将 MemTable 落盘为新的 SSTable（默认构建 Bloom Filter）。 */
  flush(): SSTable {
    const table = SSTable.fromEntries(this.nextTableId++, this.memtable, true, this.bitsPerKey);
    this.tables.unshift(table);
    this.memtable = new Map();
    return table;
  }

  get tableCount(): number {
    return this.tables.length;
  }

  getTables(): readonly SSTable[] {
    return this.tables;
  }

  get disabledFilterCount(): number {
    return this.tables.filter((t) => !t.filterEnabled).length;
  }

  get(key: string): string | undefined {
    return this.getWithTrace(key).value;
  }

  getWithTrace(key: string): GetResult {
    const trace: GetTrace = {
      key,
      found: false,
      sstablesRead: 0,
      bloomChecks: 0,
      bloomNegatives: 0,
      falsePositives: 0,
      skippedByRange: 0,
    };

    let value: string | undefined;
    if (this.memtable.has(key)) {
      value = this.memtable.get(key);
      trace.found = true;
    } else {
      for (const table of this.tables) {
        if (!table.coversKey(key)) {
          trace.skippedByRange++;
          continue;
        }
        if (table.bloomFilter) {
          trace.bloomChecks++;
          if (!table.bloomFilter.mightContain(key)) {
            trace.bloomNegatives++;
            continue;
          }
        }
        trace.sstablesRead++;
        const hit = table.entries.get(key);
        if (hit === undefined) {
          if (table.bloomFilter) trace.falsePositives++;
          continue;
        }
        value = hit;
        trace.found = true;
        break;
      }
    }

    if (this.monitor) {
      const { key: _k, skippedByRange: _s, ...sample } = trace;
      this.pendingAlerts.push(...this.monitor.record(sample));
    }
    return { value, trace };
  }

  /** 全量 Compaction：合并全部 SSTable 为一张（新值覆盖旧值），并重建 Bloom Filter。 */
  compactAll(): void {
    if (this.memtable.size > 0) this.flush();
    const merged = new Map<string, string>();
    for (let i = this.tables.length - 1; i >= 0; i--) {
      for (const [k, v] of this.tables[i].entries) merged.set(k, v);
    }
    this.tables = [SSTable.fromEntries(this.nextTableId++, merged, true, this.bitsPerKey)];
  }

  /** 取出并清空自上次调用以来监控器新触发的告警。 */
  drainAlerts(): Alert[] {
    const out = this.pendingAlerts;
    this.pendingAlerts = [];
    return out;
  }
}
