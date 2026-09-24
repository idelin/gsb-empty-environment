/**
 * ReactiveCache：按依赖版本与访问权重管理的派生值缓存。
 *
 * 核心不变量（由 verify.ts 独立核验）：
 *  1. 驱逐只释放可重算的派生缓存值，源状态永不驱逐；
 *  2. 存活订阅的值按固定策略保留，且保留总量可证明不超缓存预算（订阅准入控制）；
 *  3. 缓存项仅在依赖版本完全匹配时有效——依赖变更后绝不命中旧缓存；
 *  4. 内存按物理共享块计费，临时重算预留同样占额度，超支显式拒绝；
 *  5. 缓存容量为 0 时系统仍功能正确（每次读都重算，不缓存）。
 */

import { Ledger } from './ledger.ts';
import { BlockStore } from './blocks.ts';
import { Store, Transaction } from './state.ts';
import type { OpRecord, StateSnapshot } from './state.ts';
import { Scheduler } from './scheduler.ts';
import type { BorrowedValue, SchedulerOps } from './scheduler.ts';
import { chooseVictim } from './eviction.ts';
import type { EvictableEntry } from './eviction.ts';

export class SubscriptionRejectedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SubscriptionRejectedError';
  }
}

export interface CacheEntry {
  block: number;
  depVersions: Record<string, number>;
  hits: number;
  lastUsed: number;
}

export interface DerivedNode {
  id: string;
  deps: string[];
  compute: (values: unknown[]) => unknown | Promise<unknown>;
  entry: CacheEntry | null;
  subscribers: number;
  version: number;
}

export interface Subscription {
  readonly nodeId: string;
  unsubscribe(): void;
}

export interface CacheStats {
  hits: number;
  misses: number;
  computes: number;
  evictions: number;
  staleDiscards: number;
}

export interface ReactiveCacheOptions {
  /** 全局物理内存预算（源状态 + 缓存 + 临时重算预留共享）。 */
  budget: number;
  /** 派生缓存子预算；0 表示不缓存，仍然功能正确。 */
  cacheBudget: number;
}

const DEFAULT_RESERVATION = 64;

export class ReactiveCache implements SchedulerOps {
  readonly ledger: Ledger;
  readonly blocks: BlockStore;
  readonly store: Store;
  readonly scheduler: Scheduler;
  readonly cacheBudget: number;
  readonly stats: CacheStats = { hits: 0, misses: 0, computes: 0, evictions: 0, staleDiscards: 0 };

  private nodes = new Map<string, DerivedNode>();
  private dependents = new Map<string, Set<string>>();
  private tick = 0;
  private cacheHolders = new Map<number, number>(); // 块 <- 引用它的缓存项子图数
  private pinnedHolders = new Map<number, number>(); // 块 <- 引用它的订阅保留子图数
  private cacheBytes_ = 0;
  private pinnedBytes_ = 0;

  constructor(opts: ReactiveCacheOptions) {
    this.ledger = new Ledger(opts.budget);
    this.blocks = new BlockStore(this.ledger);
    this.store = new Store(this.blocks);
    this.cacheBudget = opts.cacheBudget;
    this.scheduler = new Scheduler(this, this.ledger);
  }

  // ---------- 定义 ----------

  defineSource(id: string, initial: unknown): void {
    if (this.nodes.has(id)) throw new Error(`"${id}" already defined as derived`);
    this.store.defineCell(id, initial);
  }

  defineDerived(
    id: string,
    deps: readonly string[],
    compute: (values: unknown[]) => unknown | Promise<unknown>,
  ): void {
    if (this.nodes.has(id) || this.store.has(id)) throw new Error(`"${id}" already defined`);
    for (const d of deps) {
      if (!this.store.has(d) && !this.nodes.has(d)) throw new Error(`unknown dependency "${d}"`);
    }
    this.nodes.set(id, { id, deps: [...deps], compute, entry: null, subscribers: 0, version: 0 });
    for (const d of deps) {
      let set = this.dependents.get(d);
      if (!set) {
        set = new Set();
        this.dependents.set(d, set);
      }
      set.add(id);
    }
  }

  // ---------- 读取 ----------

  async read(id: string): Promise<unknown> {
    if (this.store.has(id)) return this.store.readCell(id);
    const node = this.mustNode(id);
    const e = node.entry;
    if (e !== null && this.isValid(node, e)) {
      e.hits++;
      e.lastUsed = ++this.tick;
      this.stats.hits++;
      return this.blocks.resolve(e.block);
    }
    this.stats.misses++;
    return this.scheduler.request(id);
  }

  private isValid(node: DerivedNode, entry: CacheEntry): boolean {
    for (const dep of node.deps) {
      if (this.versionOf(dep) !== entry.depVersions[dep]) return false;
    }
    return true;
  }

  // ---------- 状态操作（可回放） ----------

  async transact(fn: (tx: Transaction) => void | Promise<void>): Promise<number> {
    const res = await this.store.transact(fn);
    this.cascadeInvalidate(res.cells);
    return res.seq;
  }

  /**
   * 级联失效：被写单元的所有传递依赖者版本号递增。
   * 依赖版本不再匹配 => 旧缓存项立即失效，之后任何读都不会命中旧缓存。
   */
  private cascadeInvalidate(cells: readonly string[]): void {
    const queue = [...cells];
    const seen = new Set<string>();
    while (queue.length > 0) {
      const id = queue.pop() as string;
      for (const dependent of this.dependents.get(id) ?? []) {
        if (seen.has(dependent)) continue;
        seen.add(dependent);
        const n = this.nodes.get(dependent);
        if (n) n.version++;
        queue.push(dependent);
      }
    }
  }

  snapshot(): StateSnapshot {
    return this.store.snapshot();
  }

  get opLog(): OpRecord[] {
    return this.store.opLog;
  }

  /** 重放操作日志构建等价系统；派生定义是代码，由 define 回调重新声明。 */
  static replay(
    log: readonly OpRecord[],
    opts: ReactiveCacheOptions,
    define?: (cache: ReactiveCache) => void,
  ): ReactiveCache {
    const cache = new ReactiveCache(opts);
    for (const op of log) {
      if (op.type === 'define') {
        cache.defineSource(op.cell, op.value);
      } else {
        const tx = new Transaction();
        for (const w of op.writes) tx.set(w.cell, w.value);
        const res = cache.store.commit(tx);
        cache.cascadeInvalidate(res.cells);
      }
    }
    if (define) define(cache);
    return cache;
  }

  // ---------- 订阅保留 ----------

  /**
   * 订阅一个派生节点：其缓存值按固定策略保留（永不被驱逐）。
   * 准入控制：若保留后 pinnedBytes 将超过缓存预算，则明确拒绝。
   */
  async subscribe(id: string): Promise<Subscription> {
    const node = this.mustNode(id);
    await this.read(id);
    const e = node.entry;
    if (e === null) {
      throw new SubscriptionRejectedError(
        this.cacheBudget <= 0
          ? `cannot retain subscribed value for "${id}": cache capacity is 0`
          : `cannot retain subscribed value for "${id}": cache budget ${this.cacheBudget}B ` +
              `already exhausted (pinned=${this.pinnedBytes_}B)`,
      );
    }
    if (node.subscribers === 0) {
      // pin 按节点计（首个订阅者建立，末个退订者解除），与 publish 的整块迁移对齐
      const added = this.subtreeBytes(e.block, this.pinnedHolders);
      if (this.pinnedBytes_ + added > this.cacheBudget) {
        throw new SubscriptionRejectedError(
          `retaining "${id}" would exceed cache budget: ` +
            `${this.pinnedBytes_ + added}B > ${this.cacheBudget}B`,
        );
      }
      this.pinnedBytes_ += this.adjustHolders(this.pinnedHolders, e.block, +1);
    }
    node.subscribers++;
    let active = true;
    const self = this;
    return {
      nodeId: id,
      unsubscribe(): void {
        if (!active) return;
        active = false;
        node.subscribers--;
        if (node.subscribers === 0 && node.entry !== null) {
          self.pinnedBytes_ += self.adjustHolders(self.pinnedHolders, node.entry.block, -1);
        }
        self.evictIfNeeded();
      },
    };
  }

  // ---------- 驱逐 ----------

  /** 手动驱逐全部可驱逐项（未订阅的派生缓存值），返回被驱逐节点 id。 */
  flush(): string[] {
    const evicted: string[] = [];
    for (;;) {
      const victim = chooseVictim(this.evictableEntries());
      if (victim === null) break;
      this.removeEntry(this.mustNode(victim));
      evicted.push(victim);
    }
    return evicted;
  }

  /** 当前驱逐候选（供核验：仅含未订阅的派生缓存项）。 */
  evictionCandidates(): string[] {
    return this.evictableEntries().filter((e) => !e.pinned).map((e) => e.nodeId);
  }

  private evictIfNeeded(): void {
    while (this.cacheBytes_ > this.cacheBudget) {
      const victim = chooseVictim(this.evictableEntries());
      if (victim === null) break; // 其余全部为订阅保留值，且保留量已被准入控制限制在预算内
      this.removeEntry(this.mustNode(victim));
    }
  }

  private evictableEntries(): EvictableEntry[] {
    const out: EvictableEntry[] = [];
    for (const n of this.nodes.values()) {
      if (n.entry !== null) {
        out.push({ nodeId: n.id, hits: n.entry.hits, lastUsed: n.entry.lastUsed, pinned: n.subscribers > 0 });
      }
    }
    return out;
  }

  private removeEntry(node: DerivedNode): void {
    const e = node.entry;
    if (e === null) return;
    node.entry = null;
    if (node.subscribers > 0) this.pinnedBytes_ += this.adjustHolders(this.pinnedHolders, e.block, -1);
    this.cacheBytes_ += this.adjustHolders(this.cacheHolders, e.block, -1);
    this.blocks.release(e.block);
    this.stats.evictions++;
  }

  // ---------- SchedulerOps ----------

  depsOf(nodeId: string): string[] {
    return this.mustNode(nodeId).deps;
  }

  computeOf(nodeId: string): (values: unknown[]) => unknown | Promise<unknown> {
    return this.mustNode(nodeId).compute;
  }

  estimateOf(nodeId: string): number {
    const n = this.mustNode(nodeId);
    return n.entry !== null ? this.blocks.sizeOf(n.entry.block) : DEFAULT_RESERVATION;
  }

  versionOf(id: string): number {
    if (this.store.has(id)) return this.store.cellVersion(id);
    return this.mustNode(id).version;
  }

  async readBorrowed(id: string): Promise<BorrowedValue> {
    if (this.store.has(id)) {
      const block = this.store.cellBlock(id);
      this.blocks.retain(block);
      return { block, value: this.blocks.resolve(block) };
    }
    const value = await this.read(id);
    const node = this.mustNode(id);
    if (node.entry !== null) {
      this.blocks.retain(node.entry.block);
      return { block: node.entry.block, value };
    }
    // 缓存容量为 0：物化一个临时块供借用，重算结束后释放。
    const block = this.blocks.intern(value);
    return { block, value };
  }

  releaseBorrow(block: number): void {
    this.blocks.release(block);
  }

  publish(nodeId: string, value: unknown, depVersions: Record<string, number>): void {
    const node = this.mustNode(nodeId);
    const block = this.blocks.intern(value);
    if (this.cacheBudget <= 0) {
      this.blocks.release(block);
      return;
    }
    const old = node.entry;
    node.entry = { block, depVersions, hits: old !== null ? old.hits : 0, lastUsed: ++this.tick };
    this.cacheBytes_ += this.adjustHolders(this.cacheHolders, block, +1);
    if (node.subscribers > 0) this.pinnedBytes_ += this.adjustHolders(this.pinnedHolders, block, +1);
    if (old !== null) {
      if (node.subscribers > 0) this.pinnedBytes_ += this.adjustHolders(this.pinnedHolders, old.block, -1);
      this.cacheBytes_ += this.adjustHolders(this.cacheHolders, old.block, -1);
      this.blocks.release(old.block);
      if (old.block !== block) node.version++;
    } else {
      node.version++;
    }
    this.evictIfNeeded();
  }

  onCompute(): void {
    this.stats.computes++;
  }

  onStaleDiscard(): void {
    this.stats.staleDiscards++;
  }

  // ---------- 核验与观测 ----------

  get cacheBytes(): number {
    return this.cacheBytes_;
  }

  get pinnedBytes(): number {
    return this.pinnedBytes_;
  }

  storeCells(): { id: string; block: number; version: number }[] {
    return this.store.cells_();
  }

  derivedNodes(): DerivedNode[] {
    return [...this.nodes.values()];
  }

  childrenOf(block: number): number[] {
    return this.blocks.childrenOf(block);
  }

  listBlocks() {
    return this.blocks.listBlocks();
  }

  private mustNode(id: string): DerivedNode {
    const n = this.nodes.get(id);
    if (!n) throw new Error(`unknown derived node "${id}"`);
    return n;
  }

  /**
   * 调整 holder 计数并返回物理字节变化量：
   * 某物理块的持有者数 0->1 时计入，1->0 时移出——共享块只计一次。
   */
  private adjustHolders(map: Map<number, number>, root: number, delta: number): number {
    let bytesDelta = 0;
    const visited = new Set<number>();
    const stack = [root];
    while (stack.length > 0) {
      const id = stack.pop() as number;
      if (visited.has(id)) continue;
      visited.add(id);
      const prev = map.get(id) ?? 0;
      const next = prev + delta;
      if (next < 0) throw new Error(`holder count underflow on block ${id}`);
      if (next === 0) map.delete(id);
      else map.set(id, next);
      if (prev === 0 && next > 0) bytesDelta += this.blocks.sizeOf(id);
      if (prev > 0 && next === 0) bytesDelta -= this.blocks.sizeOf(id);
      for (const c of this.blocks.childrenOf(id)) stack.push(c);
    }
    return bytesDelta;
  }

  /** 计算 root 子图中尚未被 map 覆盖的物理字节（用于订阅准入预估）。 */
  private subtreeBytes(root: number, map: Map<number, number>): number {
    let bytes = 0;
    const visited = new Set<number>();
    const stack = [root];
    while (stack.length > 0) {
      const id = stack.pop() as number;
      if (visited.has(id)) continue;
      visited.add(id);
      if (!map.has(id)) bytes += this.blocks.sizeOf(id);
      for (const c of this.blocks.childrenOf(id)) stack.push(c);
    }
    return bytes;
  }
}
