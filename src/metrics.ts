import type { GlyphMetricTable } from './types.ts';

/**
 * 版本化字形度量库。
 * 度量可能晚到(异步字体加载):commit 只接受比当前更新的版本,
 * 旧版本的测量结果一律拒绝,保证不会覆盖新布局所依据的度量。
 */
export class MetricsStore {
  private table: GlyphMetricTable;

  constructor(initial: GlyphMetricTable) {
    this.table = initial;
  }

  get version(): number {
    return this.table.version;
  }

  /** 返回 false 表示该度量版本已过期,被丢弃 */
  commit(table: GlyphMetricTable): boolean {
    if (table.version <= this.table.version) return false;
    this.table = table;
    return true;
  }

  /** 单个簇/字符串的前进宽度 */
  advance(text: string): number {
    const hit = this.table.clusters[text];
    if (hit !== undefined) return hit;
    return Array.from(text).length * this.table.fallbackAdvance;
  }

  snapshot(): GlyphMetricTable {
    return this.table;
  }
}
