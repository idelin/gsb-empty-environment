/**
 * 多粒度锁（MGL, Multi-Granularity Locking）与意向锁调度器。
 *
 * 锁层次：数据库(Database) -> 表(Table) -> 行(Row)
 *
 * 锁模式：IS / IX / S / SIX / X
 * 规则：
 *  - 对行加 S/X 锁前，自动在父级表与库上加 IS/IX 意向锁；
 *  - 对表加 S/X 锁前，自动在库上加 IS/IX 意向锁；
 *  - 表级锁请求只需与表自身的锁条目（含意向锁）做相容性判定，
 *    无需遍历表内行锁 —— 冲突判定为 O(持有者数)，与行数无关。
 */

export type LockMode = 'IS' | 'IX' | 'S' | 'SIX' | 'X';
export type ResourceKind = 'database' | 'table' | 'row';

export interface Resource {
  readonly kind: ResourceKind;
  readonly key: string;
  readonly parent: Resource | null;
}

export function database(name: string): Resource {
  return { kind: 'database', key: `db:${name}`, parent: null };
}

export function table(db: string, name: string): Resource {
  return { kind: 'table', key: `db:${db}.table:${name}`, parent: database(db) };
}

export function row(db: string, tbl: string, id: string | number): Resource {
  return { kind: 'row', key: `db:${db}.table:${tbl}.row:${id}`, parent: table(db, tbl) };
}

/** 相容矩阵：COMPATIBLE[已持有][请求] */
const COMPATIBLE: Record<LockMode, ReadonlySet<LockMode>> = {
  IS: new Set<LockMode>(['IS', 'IX', 'S', 'SIX']),
  IX: new Set<LockMode>(['IS', 'IX']),
  S: new Set<LockMode>(['IS', 'S']),
  SIX: new Set<LockMode>(['IS']),
  X: new Set<LockMode>(),
};

export function compatible(held: LockMode, requested: LockMode): boolean {
  return COMPATIBLE[held].has(requested);
}

/** 同一事务在同一资源上的锁模式叠加（锁升级）。 */
export function combine(a: LockMode, b: LockMode): LockMode {
  if (a === b) return a;
  if (a === 'IS') return b;
  if (b === 'IS') return a;
  if (a === 'X' || b === 'X') return 'X';
  return 'SIX'; // 其余组合均含 {S, IX} 或 SIX
}

/** 子级锁模式推导父级所需意向锁模式。 */
export function intentModeFor(mode: LockMode): 'IS' | 'IX' {
  return mode === 'S' || mode === 'IS' ? 'IS' : 'IX';
}

export class DeadlockError extends Error {
  readonly cycle: number[];
  constructor(cycle: number[]) {
    super(`Deadlock detected among transactions: ${cycle.join(' -> ')}`);
    this.name = 'DeadlockError';
    this.cycle = cycle;
  }
}

interface Waiter {
  txnId: number;
  mode: LockMode; // 若为本事务已有锁的升级请求，此处为叠加后的目标模式
  resolve: () => void;
  reject: (err: Error) => void;
}

interface LockEntry {
  resource: Resource;
  granted: Map<number, LockMode>; // txnId -> mode
  queue: Waiter[]; // FIFO 等待队列，保证公平、避免饥饿
}

export interface LockManagerStats {
  /** 各层级上执行过的“持有者相容性检查”次数；表级请求的该计数恒为 0（不触行）。 */
  conflictChecks: Record<ResourceKind, number>;
}

export class LockManager {
  private readonly entries = new Map<string, LockEntry>();
  private readonly heldByTxn = new Map<number, Set<string>>();
  private readonly waitingFor = new Map<number, string>(); // txnId -> 正在等待的资源 key
  readonly stats: LockManagerStats = {
    conflictChecks: { database: 0, table: 0, row: 0 },
  };

  /** 申请锁；无法立即授予时返回挂起的 Promise，由调度器在可授予时兑现。 */
  acquire(txnId: number, resource: Resource, mode: LockMode): Promise<void> {
    const entry = this.getOrCreateEntry(resource);
    const existing = entry.granted.get(txnId);
    const target = existing !== undefined ? combine(existing, mode) : mode;

    if (this.canGrant(entry, txnId, target)) {
      entry.granted.set(txnId, target);
      this.trackGrant(txnId, entry.resource.key);
      return Promise.resolve();
    }

    return new Promise<void>((resolve, reject) => {
      const waiter: Waiter = { txnId, mode: target, resolve, reject };
      entry.queue.push(waiter);
      this.waitingFor.set(txnId, resource.key);
      const cycle = this.detectCycle(txnId);
      if (cycle !== null) {
        this.removeWaiter(entry, waiter);
        reject(new DeadlockError(cycle));
      }
    });
  }

  /** 释放某事务在某资源上的锁，并唤醒队列中可授予的等待者。 */
  release(txnId: number, resource: Resource): void {
    const entry = this.entries.get(resource.key);
    if (entry === undefined || !entry.granted.delete(txnId)) return;
    this.trackRelease(txnId, resource.key);
    this.drain(entry);
    this.gc(entry);
  }

  /** 释放事务持有的全部锁（提交/回滚时调用）。 */
  releaseAll(txnId: number): void {
    const keys = this.heldByTxn.get(txnId);
    if (keys === undefined) return;
    for (const key of [...keys]) {
      const entry = this.entries.get(key);
      if (entry !== undefined && entry.granted.delete(txnId)) {
        this.trackRelease(txnId, key);
        this.drain(entry);
        this.gc(entry);
      }
    }
    this.heldByTxn.delete(txnId);
  }

  heldMode(txnId: number, resource: Resource): LockMode | undefined {
    return this.entries.get(resource.key)?.granted.get(txnId);
  }

  isWaiting(txnId: number): boolean {
    return this.waitingFor.has(txnId);
  }

  /** 当前锁表快照，用于演示与调试。 */
  snapshot(): Array<{ key: string; granted: Record<string, LockMode>; waiting: number[] }> {
    const out: Array<{ key: string; granted: Record<string, LockMode>; waiting: number[] }> = [];
    for (const entry of this.entries.values()) {
      const granted: Record<string, LockMode> = {};
      for (const [txn, mode] of entry.granted) granted[`T${txn}`] = mode;
      out.push({ key: entry.resource.key, granted, waiting: entry.queue.map((w) => w.txnId) });
    }
    return out;
  }

  private getOrCreateEntry(resource: Resource): LockEntry {
    let entry = this.entries.get(resource.key);
    if (entry === undefined) {
      entry = { resource, granted: new Map(), queue: [] };
      this.entries.set(resource.key, entry);
    }
    return entry;
  }

  /**
   * 授予判定：仅检查该资源自身锁条目上的持有者与等待队列。
   * 行级冲突已被父级意向锁“汇总”到表条目上，因此表级请求在此即为完整判定，
   * 不会访问任何行级锁条目。
   */
  private canGrant(entry: LockEntry, txnId: number, mode: LockMode): boolean {
    if (!this.compatibleWithHolders(entry, txnId, mode)) return false;
    if (entry.queue.length === 0) return true;
    // 队列非空：仅允许“已持有者的相容升级”插队，其余一律排队以保证公平。
    if (!entry.granted.has(txnId)) return false;
    return entry.queue.every((w) => compatible(w.mode, mode) && compatible(mode, w.mode));
  }

  /** 与该条目当前所有其他持有者的模式相容性检查（冲突判定的唯一入口）。 */
  private compatibleWithHolders(entry: LockEntry, txnId: number, mode: LockMode): boolean {
    for (const [holder, heldMode] of entry.granted) {
      if (holder === txnId) continue;
      this.stats.conflictChecks[entry.resource.kind]++;
      if (!compatible(heldMode, mode)) return false;
    }
    return true;
  }

  /** 从队首起连续授予与当前持有组相容的等待者（FIFO 前缀授予）。 */
  private drain(entry: LockEntry): void {
    while (entry.queue.length > 0) {
      const head = entry.queue[0];
      if (!this.compatibleWithHolders(entry, head.txnId, head.mode)) return;
      entry.queue.shift();
      this.waitingFor.delete(head.txnId);
      entry.granted.set(head.txnId, head.mode);
      this.trackGrant(head.txnId, entry.resource.key);
      head.resolve();
    }
  }

  /** 等待图（wait-for）环检测；成环时返回环上的事务序列。 */
  private detectCycle(startTxn: number): number[] | null {
    const visited = new Set<number>();
    const stack: Array<{ txn: number; path: number[] }> = [{ txn: startTxn, path: [startTxn] }];
    while (stack.length > 0) {
      const { txn, path } = stack.pop()!;
      if (visited.has(txn)) continue;
      visited.add(txn);
      const key = this.waitingFor.get(txn);
      if (key === undefined) continue;
      const entry = this.entries.get(key);
      if (entry === undefined) continue;
      for (const blocker of this.blockersOf(entry, txn)) {
        if (blocker === startTxn) return [...path, startTxn];
        stack.push({ txn: blocker, path: [...path, blocker] });
      }
    }
    return null;
  }

  /** txn 在 entry 上的阻塞者：其他持有者 + 排在其前面的等待者。 */
  private blockersOf(entry: LockEntry, txnId: number): Set<number> {
    const blockers = new Set<number>();
    for (const holder of entry.granted.keys()) {
      if (holder !== txnId) blockers.add(holder);
    }
    for (const w of entry.queue) {
      if (w.txnId === txnId) break;
      blockers.add(w.txnId);
    }
    return blockers;
  }

  private removeWaiter(entry: LockEntry, waiter: Waiter): void {
    const idx = entry.queue.indexOf(waiter);
    if (idx >= 0) entry.queue.splice(idx, 1);
    if (this.waitingFor.get(waiter.txnId) === entry.resource.key) {
      this.waitingFor.delete(waiter.txnId);
    }
    this.gc(entry);
  }

  private trackGrant(txnId: number, key: string): void {
    let keys = this.heldByTxn.get(txnId);
    if (keys === undefined) {
      keys = new Set();
      this.heldByTxn.set(txnId, keys);
    }
    keys.add(key);
  }

  private trackRelease(txnId: number, key: string): void {
    const keys = this.heldByTxn.get(txnId);
    if (keys !== undefined) {
      keys.delete(key);
      if (keys.size === 0) this.heldByTxn.delete(txnId);
    }
  }

  private gc(entry: LockEntry): void {
    if (entry.granted.size === 0 && entry.queue.length === 0) {
      this.entries.delete(entry.resource.key);
    }
  }
}

let nextTxnId = 1;

/** 事务句柄：封装多粒度加锁协议（自动沿层次向上加意向锁）。 */
export class Transaction {
  readonly id: number;
  private readonly lm: LockManager;

  constructor(lm: LockManager, id?: number) {
    this.lm = lm;
    this.id = id ?? nextTxnId++;
  }

  /** 库级锁。 */
  lockDatabase(db: string, mode: LockMode): Promise<void> {
    return this.lm.acquire(this.id, database(db), mode);
  }

  /** 表级锁；自动先在库上加意向锁。 */
  async lockTable(db: string, tbl: string, mode: LockMode): Promise<void> {
    await this.lm.acquire(this.id, database(db), intentModeFor(mode));
    await this.lm.acquire(this.id, table(db, tbl), mode);
  }

  /** 行级 S/X 锁；自动在表与库上加 IS/IX 意向锁。 */
  async lockRow(db: string, tbl: string, rowId: string | number, mode: 'S' | 'X'): Promise<void> {
    const intent = intentModeFor(mode);
    await this.lm.acquire(this.id, database(db), intent);
    await this.lm.acquire(this.id, table(db, tbl), intent);
    await this.lm.acquire(this.id, row(db, tbl, rowId), mode);
  }

  /** 提交（或回滚）：释放全部锁。 */
  commit(): void {
    this.lm.releaseAll(this.id);
  }

  abort(): void {
    this.lm.releaseAll(this.id);
  }
}
