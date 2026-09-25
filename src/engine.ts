/**
 * 增量命名引擎：
 *  - 缓存按 (引擎实例, 树版本) 隔离；不同树/实例之间互不共享，
 *    一棵树的错误引用不会串到另一棵树。
 *  - 每次计算记录依赖键集合；DOM 式变更后调用 sync()，
 *    仅失效并原地重算依赖受影响的条目，输出变化通知。
 *  - 未受影响的条目在依赖校验后随版本前滚，继续命中缓存。
 */
import { computeNameAndDescription } from './accname.ts';
import type { GlyphMetrics } from './metrics.ts';
import type { NodeTree } from './tree.ts';

export interface NameResult {
  name: string;
  description: string;
}

export interface ChangeNotification {
  key: string;
  before: NameResult | null;
  after: NameResult | null;
}

interface CacheEntry {
  version: number;
  result: NameResult;
  deps: Set<string>;
}

export class NameEngine {
  readonly stats = { computed: 0, recomputed: 0, hits: 0 };
  private cache = new Map<string, CacheEntry>();
  private readonly tree: NodeTree;
  private readonly metrics: GlyphMetrics;

  constructor(tree: NodeTree, metrics: GlyphMetrics) {
    this.tree = tree;
    this.metrics = metrics;
  }

  get(key: string): NameResult {
    const hit = this.cache.get(key);
    if (hit && hit.version === this.tree.version) {
      this.stats.hits++;
      return hit.result;
    }
    const deps = new Set<string>();
    const result = computeNameAndDescription(this.tree, this.metrics, key, deps);
    this.stats.computed++;
    this.cache.set(key, { version: this.tree.version, result, deps });
    return result;
  }

  /** 消费树的累积变更，增量重算并返回结果发生变化的节点通知。 */
  sync(): ChangeNotification[] {
    const { keys, removed } = this.tree.drainChanges();
    const notifications: ChangeNotification[] = [];

    for (const key of removed) {
      const entry = this.cache.get(key);
      if (entry) {
        this.cache.delete(key);
        notifications.push({ key, before: entry.result, after: null });
      }
    }

    const affected: string[] = [];
    for (const [key, entry] of this.cache) {
      let hit = false;
      for (const dep of entry.deps) {
        if (keys.has(dep)) { hit = true; break; }
      }
      if (hit) affected.push(key);
    }
    affected.sort((a, b) => this.tree.compareKeys(a, b));

    for (const key of affected) {
      const before = this.cache.get(key)?.result ?? null;
      this.cache.delete(key);
      this.stats.recomputed++;
      const after = this.get(key);
      if (!before || before.name !== after.name || before.description !== after.description) {
        notifications.push({ key, before, after });
      }
    }

    // 未受影响的条目通过依赖校验，随当前树版本前滚，继续复用。
    for (const entry of this.cache.values()) {
      entry.version = this.tree.version;
    }
    return notifications;
  }
}
