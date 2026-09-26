import { flatten } from './flatten.ts';
import { layoutAtoms, type LayoutParams } from './layout.ts';
import { MetricsStore } from './metrics.ts';
import type { Atom, GlyphMetricTable, InlineNode, LayoutResult } from './types.ts';

export interface EngineState {
  atoms: Atom[];
  accessibleName: string;
  result: LayoutResult;
}

/**
 * 截断布局引擎:
 * - 节点树与字形度量全部由夹具注入,不借浏览器布局引擎;
 * - 度量晚到时增量重排(只重算宽度变化的项);
 * - 旧版本度量一律拒绝,不会覆盖新布局。
 */
export class TruncationEngine {
  readonly store: MetricsStore;
  private generation = 0;
  private widthCache = new Map<string, { version: number; width: number }>();
  /** 增量重排证据:本轮重用了多少缓存宽度、新算了多少 */
  stats = { reused: 0, computed: 0 };
  private nodes: InlineNode[] | null = null;
  private params: LayoutParams | null = null;
  private state: EngineState | null = null;

  constructor(initialMetrics: GlyphMetricTable) {
    this.store = new MetricsStore(initialMetrics);
  }

  private widthsFor(atoms: Atom[]): number[] {
    const v = this.store.version;
    this.stats = { reused: 0, computed: 0 };
    return atoms.map((a) => {
      const hit = this.widthCache.get(a.text);
      if (hit && hit.version === v) {
        this.stats.reused++;
        return hit.width;
      }
      const w = this.store.advance(a.text);
      this.widthCache.set(a.text, { version: v, width: w });
      this.stats.computed++;
      return w;
    });
  }

  private runLayout(): LayoutResult {
    const { atoms, accessibleName } = flatten(this.nodes!);
    const widths = this.widthsFor(atoms);
    const ellipsisWidth = this.store.advance(this.params!.ellipsis ?? '…');
    const result = layoutAtoms(
      atoms, widths, this.params!, ellipsisWidth, this.store.version, ++this.generation);
    this.state = { atoms, accessibleName, result };
    return result;
  }

  layout(nodes: InlineNode[], params: LayoutParams): LayoutResult {
    this.nodes = nodes;
    this.params = params;
    return this.runLayout();
  }

  /**
   * 提交新度量(晚到的字体度量 / 版本切换)。
   * 仅当版本更新时才接受并增量重排;否则拒绝,现有布局保持不变。
   */
  updateMetrics(table: GlyphMetricTable): { accepted: boolean; result: LayoutResult | null } {
    if (!this.store.commit(table)) return { accepted: false, result: null };
    if (!this.nodes) return { accepted: true, result: null };
    return { accepted: true, result: this.runLayout() };
  }

  /** 模拟异步晚到的度量;resolve 时若版本已过期则被丢弃,不触发重排。 */
  updateMetricsAsync(table: GlyphMetricTable, delayMs = 0):
    Promise<{ accepted: boolean; result: LayoutResult | null }> {
    return new Promise((resolve) => {
      setTimeout(() => resolve(this.updateMetrics(table)), delayMs);
    });
  }

  get current(): EngineState | null {
    return this.state;
  }
}
