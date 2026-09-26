/**
 * 文本测量引擎：
 * - 预估测量同步发布，最终测量异步发布，二者分离；
 * - 字体度量 / 字形簇 / 样式组合测量分别缓存，键含字体版本与字距；
 * - 字体热更新时精确失效并提升缓存写入代际下限；
 * - 旧代际异步回包被调度器丢弃后，按新代际自动重发（有上限）。
 */
import { ByteCache } from './cache.ts';
import { FontRegistry } from './fontRegistry.ts';
import { Scheduler, Task } from './scheduler.ts';
import type {
  FontFace,
  GlyphCluster,
  MeasureEvent,
  MeasureResult,
  StyleSpec,
} from './types.ts';

export const PARTITION_FONT = 'font';
export const PARTITION_CLUSTER = 'cluster';
export const PARTITION_MEASURE = 'measure';

const MAX_REISSUE = 8;

export function fontKey(font: FontFace): string {
  return `font:${font.id}@${font.version}`;
}

export function clusterKey(font: FontFace, text: string): string {
  return `cluster:${font.id}@${font.version}:${text}`;
}

/** 样式组合测量键：包含字距，相同文本不同字距不会共享结果。 */
export function measureKey(style: StyleSpec, fontVersion: number, text: string): string {
  return `measure:${style.fontId}@${fontVersion}:${style.fontSize}:${style.letterSpacing}:${text}`;
}

export interface MeasureHandle {
  cancel(): void;
}

export class TextMeasurer {
  readonly registry: FontRegistry;
  readonly scheduler: Scheduler;
  readonly cache: ByteCache;

  constructor(registry: FontRegistry, cache: ByteCache, schedulerOpts: { auto?: boolean } = {}) {
    this.registry = registry;
    this.cache = cache;
    this.scheduler = new Scheduler((task) => this.execute(task), () => this.registry.snapshot().generation, schedulerOpts);
    // 热更新：精确失效目标字体的旧版本条目 + 原子提升写入代际下限。
    this.registry.onChange((snapshot, changed) => {
      this.cache.invalidateWhere(
        (meta) => meta.fontId === changed.id && meta.fontVersion !== changed.version,
      );
      this.cache.raiseWriteFloor(snapshot.generation);
    });
  }

  /**
   * 测量文本。预估事件同步发布，最终事件异步发布。
   * 返回句柄可取消本次订阅（不影响其他等待相同结果的订阅者）。
   */
  measureText(text: string, style: StyleSpec, onEvent: (e: MeasureEvent) => void): MeasureHandle {
    return this.measureInternal(text, style, onEvent, 0);
  }

  private measureInternal(
    text: string,
    style: StyleSpec,
    onEvent: (e: MeasureEvent) => void,
    depth: number,
  ): MeasureHandle {
    const snapshot = this.registry.snapshot();
    const font = snapshot.get(style.fontId);
    if (!font) throw new Error(`字体未注册: ${style.fontId}`);
    const generation = snapshot.generation;
    const key = measureKey(style, font.version, text);

    // 预估：立即发布，与最终测量分离。
    onEvent({ phase: 'estimate', key, result: this.estimate(text, style, font), generation });

    // 缓存命中且版本匹配：直接发布最终测量。
    const cached = this.cache.get<MeasureResult>(PARTITION_MEASURE, key);
    if (cached && cached.meta.fontVersion === font.version) {
      onEvent({ phase: 'final', key, result: cached.value, generation });
      return { cancel: () => {} };
    }

    // 共享依赖任务：字体度量 -> 字形簇 -> 样式组合测量。
    const fontTask = this.scheduler.request<FontFace>(fontKey(font), [], { fontId: font.id });
    const clusterTask = this.scheduler.request<GlyphCluster>(clusterKey(font, text), [fontTask], {
      fontId: font.id,
      text,
    });
    const measureTask = this.scheduler.request<MeasureResult>(key, [clusterTask], {
      style,
      text,
      fontVersion: font.version,
    });

    const subscription = measureTask.subscribe((event) => {
      if (event.type === 'resolved') {
        // 字体加载/测量晚到：校验版本，过期结果不得发布。
        const current = this.registry.snapshot().get(style.fontId);
        if (!current || current.version !== font.version) return;
        onEvent({ phase: 'final', key, result: event.value, generation });
      } else if (event.type === 'stale') {
        // 旧代际回包被丢弃：按当前代际重发（有上限，杜绝无界循环）。
        if (depth < MAX_REISSUE) this.measureInternal(text, style, onEvent, depth + 1);
      }
    });

    return {
      cancel: () => {
        // 仅退订本订阅；共享依赖的释放由调度器按引用计数处理。
        subscription.cancel();
      },
    };
  }

  /** 预估启发式：平均字宽近似，刻意与最终值不同。 */
  private estimate(text: string, style: StyleSpec, font: FontFace): MeasureResult {
    const scale = style.fontSize / font.unitsPerEm;
    const n = [...text].length;
    const width = n * 0.5 * font.unitsPerEm * scale + style.letterSpacing * Math.max(0, n - 1);
    const ascent = font.ascent * scale;
    const descent = font.descent * scale;
    return { width, height: ascent + descent, ascent, descent };
  }

  /** 调度器执行器：按任务键前缀分派，核心计算纯数据驱动。 */
  private execute(task: Task): unknown {
    if (task.key.startsWith('font:')) return this.execFont(task);
    if (task.key.startsWith('cluster:')) return this.execCluster(task);
    if (task.key.startsWith('measure:')) return this.execMeasure(task);
    throw new Error(`未知任务: ${task.key}`);
  }

  private execFont(task: Task): FontFace {
    const cached = this.cache.get<FontFace>(PARTITION_FONT, task.key);
    if (cached) return cached.value;
    const { fontId } = task.payload as { fontId: string };
    const font = this.registry.snapshot().get(fontId);
    if (!font) throw new Error(`字体未注册: ${fontId}`);
    this.cache.set(PARTITION_FONT, task.key, font, {
      fontId: font.id,
      fontVersion: font.version,
      generation: task.generation,
    });
    return font;
  }

  /** 字形簇成形：查夹具字形表，缺失字形回退 0.5em。 */
  private execCluster(task: Task): GlyphCluster {
    const cached = this.cache.get<GlyphCluster>(PARTITION_CLUSTER, task.key);
    if (cached) return cached.value;
    const { fontId, text } = task.payload as { fontId: string; text: string };
    const font = this.registry.snapshot().get(fontId);
    if (!font) throw new Error(`字体未注册: ${fontId}`);
    const advances = [...text].map((ch) => font.glyphs[ch] ?? font.unitsPerEm / 2);
    const cluster: GlyphCluster = { text, fontId, fontVersion: font.version, advances };
    this.cache.set(PARTITION_CLUSTER, task.key, cluster, {
      fontId,
      fontVersion: font.version,
      generation: task.generation,
    });
    return cluster;
  }

  /** 最终测量：簇 advance 求和 + 字距，纯计算不借浏览器。 */
  private execMeasure(task: Task): MeasureResult {
    const { style } = task.payload as { style: StyleSpec; text: string; fontVersion: number };
    const clusterTask = task.deps[0];
    const cluster = clusterTask.value as GlyphCluster;
    const font = this.registry.snapshot().get(style.fontId);
    if (!font) throw new Error(`字体未注册: ${style.fontId}`);
    const scale = style.fontSize / font.unitsPerEm;
    const n = cluster.advances.length;
    let advanceSum = 0;
    for (const a of cluster.advances) advanceSum += a;
    const width = advanceSum * scale + style.letterSpacing * Math.max(0, n - 1);
    const ascent = font.ascent * scale;
    const descent = font.descent * scale;
    const result: MeasureResult = { width, height: ascent + descent, ascent, descent };
    this.cache.set(PARTITION_MEASURE, task.key, result, {
      fontId: font.id,
      fontVersion: font.version,
      generation: task.generation,
    });
    return result;
  }
}
