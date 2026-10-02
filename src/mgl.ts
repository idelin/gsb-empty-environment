/**
 * 多粒度锁（MGL）与意向锁调度器。
 *
 * 锁层次：数据库(db) -> 表(table) -> 行(row)。
 * 锁模式：IS / IX / S / SIX / X。
 *
 * 核心思想：每个锁节点维护"聚合模式计数"（持有该节点锁的各模式数量，
 * 含由后代行锁向上传播的意向锁）。因此表级锁请求的冲突判定只需读取
 * 表节点自身的聚合计数，O(1) 完成，绝不遍历子行节点。
 */

export type LockMode = 'IS' | 'IX' | 'S' | 'SIX' | 'X';

export const ALL_MODES: readonly LockMode[] = ['IS', 'IX', 'S', 'SIX', 'X'];

/** 兼容矩阵：COMPATIBLE[已持有模式] 包含可共存的新请求模式。 */
const COMPATIBLE: Record<LockMode, readonly LockMode[]> = {
  IS: ['IS', 'IX', 'S', 'SIX'],
  IX: ['IS', 'IX'],
  S: ['IS', 'S'],
  SIX: ['IS'],
  X: [],
};

export function compatible(held: LockMode, requested: LockMode): boolean {
  return COMPATIBLE[held].includes(requested);
}

/** 同一事务在同一节点上的锁升级/合并。 */
export function combineModes(held: LockMode, requested: LockMode): LockMode {
  if (held === requested) return held;
  if (held === 'X' || requested === 'IS') return held;
  if (requested === 'X' || held === 'IS') return requested;
  if (held === 'SIX' || requested === 'SIX') return 'SIX';
  // 剩余组合仅为 S+IX
  return 'SIX';
}

export interface LockOptions {
  /** 等待超时（毫秒）。超时后请求从等待队列移除并 reject。 */
  timeoutMs?: number;
}

interface Waiter {
  txn: string;
  mode: LockMode;
  resolve: () => void;
  reject: (err: Error) => void;
  timer: ReturnType<typeof setTimeout> | null;
}

/** 单个锁节点（一个库 / 一张表 / 一行）。 */
class LockNode {
  /** txn -> 当前持有的（已合并）模式 */
  readonly holders = new Map<string, LockMode>();
  /** 各模式的聚合持有计数：冲突判定的全部依据，O(1) 可查 */
  readonly counts: Record<LockMode, number> = { IS: 0, IX: 0, S: 0, SIX: 0, X: 0 };
  /** FIFO 等待队列，保证公平、避免饥饿 */
  readonly queue: Waiter[] = [];
}

export interface LockManagerStats {
  /** 冲突判定次数 */
  conflictChecks: number;
  /** 冲突判定中检查的聚合计数条目数（每次 <= 5，证明 O(1)） */
  aggregateEntriesExamined: number;
  /** 冲突判定中访问的行级节点数（本设计恒为 0，证明无需遍历行） */
  rowNodesExamined: number;
}

export class LockTimeoutError extends Error {
  constructor(txn: string, key: string, mode: LockMode) {
    super(`txn ${txn} timed out waiting for ${mode} on ${key}`);
    this.name = 'LockTimeoutError';
  }
}

export class LockManager {
  private readonly nodes = new Map<string, LockNode>();
  private readonly txnIndex = new Map<string, Set<string>>();

  readonly stats: LockManagerStats = {
    conflictChecks: 0,
    aggregateEntriesExamined: 0,
    rowNodesExamined: 0,
  };

  // ---------- 底层：单节点加/解锁 ----------

  private node(key: string): LockNode {
    let n = this.nodes.get(key);
    if (!n) {
      n = new LockNode();
      this.nodes.set(key, n);
    }
    return n;
  }

  /**
   * 冲突判定：只读本节点聚合计数（最多 5 个条目），
   * 不访问任何子节点 —— 这是"快速判定、无需遍历全表"的实现核心。
   */
  private canGrant(n: LockNode, txn: string, mode: LockMode): boolean {
    this.stats.conflictChecks++;
    const own = n.holders.get(txn);
    for (const m of ALL_MODES) {
      let c = n.counts[m];
      if (c === 0) continue;
      this.stats.aggregateEntriesExamined++;
      if (own === m) c -= 1; // 排除自己持有的部分（支持锁升级）
      if (c > 0 && !compatible(m, mode)) return false;
    }
    return true;
  }

  /** 队列中没有其它事务的请求排在前面时才允许立即授予（FIFO 公平）。 */
  private queueClearFor(n: LockNode, txn: string): boolean {
    return n.queue.every((w) => w.txn === txn);
  }

  private grant(n: LockNode, txn: string, key: string, mode: LockMode): void {
    const prev = n.holders.get(txn);
    const combined = prev !== undefined ? combineModes(prev, mode) : mode;
    if (prev !== undefined) n.counts[prev]--;
    n.counts[combined]++;
    n.holders.set(txn, combined);
    let keys = this.txnIndex.get(txn);
    if (!keys) {
      keys = new Set();
      this.txnIndex.set(txn, keys);
    }
    keys.add(key);
  }

  /** 同步尝试加锁；成功返回 true，否则返回 false（不排队）。 */
  tryLock(txn: string, key: string, mode: LockMode): boolean {
    const n = this.node(key);
    if (this.queueClearFor(n, txn) && this.canGrant(n, txn, mode)) {
      this.grant(n, txn, key, mode);
      return true;
    }
    return false;
  }

  /** 异步加锁；冲突时进入 FIFO 等待队列，直到被授予或超时。 */
  lock(txn: string, key: string, mode: LockMode, opts: LockOptions = {}): Promise<void> {
    if (this.tryLock(txn, key, mode)) return Promise.resolve();
    const n = this.node(key);
    return new Promise<void>((resolve, reject) => {
      const waiter: Waiter = { txn, mode, resolve, reject, timer: null };
      if (opts.timeoutMs !== undefined) {
        waiter.timer = setTimeout(() => {
          const i = n.queue.indexOf(waiter);
          if (i >= 0) n.queue.splice(i, 1);
          reject(new LockTimeoutError(txn, key, mode));
        }, opts.timeoutMs);
      }
      n.queue.push(waiter);
    });
  }

  /** 释放事务在某节点上的锁，并唤醒队列中可授予的等待者。 */
  unlock(txn: string, key: string): boolean {
    const n = this.nodes.get(key);
    if (!n) return false;
    const held = n.holders.get(txn);
    if (held === undefined) return false;
    n.counts[held]--;
    n.holders.delete(txn);
    this.txnIndex.get(txn)?.delete(key);
    this.drain(key, n);
    return true;
  }

  /** 从事务提交/回滚：释放其持有的全部锁。 */
  releaseAll(txn: string): void {
    const keys = this.txnIndex.get(txn);
    if (!keys) return;
    for (const key of [...keys]) this.unlock(txn, key);
  }

  /** 依次授予队首可兼容的等待者（兼容的连续请求可成组授予，如多个 S）。 */
  private drain(key: string, n: LockNode): void {
    while (n.queue.length > 0) {
      const w = n.queue[0];
      if (!this.canGrant(n, w.txn, w.mode)) break;
      n.queue.shift();
      if (w.timer !== null) clearTimeout(w.timer);
      this.grant(n, w.txn, key, w.mode);
      w.resolve();
    }
  }

  // ---------- 上层：多粒度层次锁（自动加意向锁） ----------

  static dbKey(db: string): string {
    return `db:${db}`;
  }
  static tableKey(db: string, table: string): string {
    return `db:${db}.tbl:${table}`;
  }
  static rowKey(db: string, table: string, row: string): string {
    return `db:${db}.tbl:${table}.row:${row}`;
  }

  /** 库级 S/X 锁。 */
  async lockDatabase(txn: string, db: string, mode: 'S' | 'X', opts?: LockOptions): Promise<void> {
    await this.lock(txn, LockManager.dbKey(db), mode, opts);
  }

  /** 表级 S/X/SIX 锁；自动先在库上加意向锁（S->IS，X/SIX->IX）。 */
  async lockTable(
    txn: string,
    db: string,
    table: string,
    mode: 'S' | 'X' | 'SIX',
    opts?: LockOptions,
  ): Promise<void> {
    await this.lock(txn, LockManager.dbKey(db), mode === 'S' ? 'IS' : 'IX', opts);
    await this.lock(txn, LockManager.tableKey(db, table), mode, opts);
  }

  /** 行级 S/X 锁；自动沿层次向上在表和库上加 IS/IX 意向锁。 */
  async lockRow(
    txn: string,
    db: string,
    table: string,
    row: string,
    mode: 'S' | 'X',
    opts?: LockOptions,
  ): Promise<void> {
    const intention: LockMode = mode === 'S' ? 'IS' : 'IX';
    await this.lock(txn, LockManager.dbKey(db), intention, opts);
    await this.lock(txn, LockManager.tableKey(db, table), intention, opts);
    await this.lock(txn, LockManager.rowKey(db, table, row), mode, opts);
  }

  // ---------- 观测接口（测试与演示用） ----------

  /** 某节点上当前各事务持有的模式。 */
  holdersOf(key: string): ReadonlyMap<string, LockMode> {
    return this.node(key).holders;
  }

  /** 某节点的聚合模式计数（冲突判定的直接依据）。 */
  aggregateCounts(key: string): Readonly<Record<LockMode, number>> {
    return this.node(key).counts;
  }

  /** 某节点等待队列长度。 */
  queueLength(key: string): number {
    return this.node(key).queue.length;
  }

  /** 当前锁状态快照，便于打印演示。 */
  snapshot(): Record<string, Record<string, LockMode>> {
    const out: Record<string, Record<string, LockMode>> = {};
    for (const [key, n] of [...this.nodes.entries()].sort()) {
      if (n.holders.size === 0) continue;
      out[key] = Object.fromEntries(n.holders);
    }
    return out;
  }
}
