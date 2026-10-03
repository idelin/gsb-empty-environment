/**
 * 多节点并发竞争与冲突仲裁器。
 *
 * 设计要点：
 * - 每个节点注册时获得唯一且单调递增的时间戳，采用 wound-wait 纪律：
 *   年长者（时间戳小）可“击伤”年轻持有者迫使其回滚；年轻者只能等待。
 *   等待图中永远只存在“年轻 → 年长”的边，从结构上杜绝死锁。
 * - 所有仲裁状态迁移在同步临界区内完成（check-and-act 之间无 await），
 *   在 Node 单线程事件循环下保证原子性、无竞态。
 * - 线性一致性：每次成功提交在仲裁器内获得全局单调序号（线性化点），
 *   完整事件历史可回放校验。
 * - FIFO 等待队列防止插队与饥饿；锁升级（SHARED→EXCLUSIVE）被显式支持。
 */

export type LockMode = 'SHARED' | 'EXCLUSIVE';

export class WoundError extends Error {
  readonly nodeId: number;
  constructor(nodeId: number, message: string) {
    super(message);
    this.name = 'WoundError';
    this.nodeId = nodeId;
  }
}

export interface LockHandle {
  readonly nodeId: number;
  readonly key: string;
  readonly mode: LockMode;
  readonly grantSeq: number;
  valid: boolean;
  wounded: boolean;
  released: boolean;
}

export interface ArbiterEvent {
  seq: number;
  kind: 'GRANT' | 'RELEASE' | 'WOUND' | 'COMMIT' | 'ABORT' | 'WAIT';
  nodeId: number;
  key: string;
  detail?: string;
}

interface HolderEntry {
  mode: LockMode;
  refcount: number;
}

interface WaitRequest {
  nodeId: number;
  mode: LockMode;
  resolve: (h: LockHandle) => void;
  reject: (e: Error) => void;
}

interface KeyState {
  holders: Map<number, HolderEntry>;
  queue: WaitRequest[];
}

function compatible(a: LockMode, b: LockMode): boolean {
  return a === 'SHARED' && b === 'SHARED';
}

export class ConflictArbiter {
  private seq = 0;
  private timestampCounter = 0;
  private readonly timestamps = new Map<number, number>();
  private readonly table = new Map<string, KeyState>();
  private readonly registers = new Map<string, unknown>();
  /** 每个节点当前存活的句柄，用于击伤时同步失效，杜绝“被击伤后仍提交”的竞态 */
  private readonly liveHandles = new Map<number, Set<LockHandle>>();
  readonly events: ArbiterEvent[] = [];

  /** 注册节点，分配唯一递增时间戳（重试时必须复用 nodeId 以保持 wound-wait 不变式） */
  registerNode(): number {
    const nodeId = this.timestamps.size + 1;
    this.timestamps.set(nodeId, ++this.timestampCounter);
    return nodeId;
  }

  timestampOf(nodeId: number): number {
    const ts = this.timestamps.get(nodeId);
    if (ts === undefined) throw new Error(`node ${nodeId} not registered`);
    return ts;
  }

  private keyState(key: string): KeyState {
    let ks = this.table.get(key);
    if (!ks) {
      ks = { holders: new Map(), queue: [] };
      this.table.set(key, ks);
    }
    return ks;
  }

  private record(kind: ArbiterEvent['kind'], nodeId: number, key: string, detail?: string): number {
    const seq = ++this.seq;
    this.events.push({ seq, kind, nodeId, key, detail });
    return seq;
  }

  /** 与 (nodeId 请求 mode) 冲突的其他持有者 */
  private conflictingHolders(ks: KeyState, nodeId: number, mode: LockMode): number[] {
    const out: number[] = [];
    for (const [holderId, h] of ks.holders) {
      if (holderId === nodeId) continue;
      if (!compatible(h.mode, mode)) out.push(holderId);
    }
    return out;
  }

  /**
   * 申请锁。同步完成“检查-击伤-授予或入队”决策，返回 Promise：
   * 立即授予则已 resolve；否则进入 FIFO 队列等待。
   */
  acquire(nodeId: number, key: string, mode: LockMode): Promise<LockHandle> {
    this.timestampOf(nodeId);
    const ks = this.keyState(key);
    const myTs = this.timestampOf(nodeId);

    // 同一节点已持有：同级幂等或升级
    const mine = ks.holders.get(nodeId);
    if (mine) {
      if (mine.mode === 'EXCLUSIVE' || mine.mode === mode) {
        mine.refcount++;
        const grantSeq = this.record('GRANT', nodeId, key, `reentrant ${mode}`);
        return Promise.resolve(this.newHandle(nodeId, key, mode, grantSeq));
      }
      // SHARED -> EXCLUSIVE 升级：落入下方冲突/击伤逻辑（自身不视为冲突）
    }

    const conflicts = this.conflictingHolders(ks, nodeId, mode);
    if (conflicts.length === 0 && ks.queue.length === 0) {
      this.grant(ks, nodeId, key, mode);
      const grantSeq = this.record('GRANT', nodeId, key, mode);
      return Promise.resolve(this.newHandle(nodeId, key, mode, grantSeq));
    }

    // wound-wait：击伤所有比自己年轻的冲突持有者
    const younger = conflicts.filter((h) => this.timestampOf(h) > myTs);
    for (const victim of younger) this.wound(victim, `wounded by older node ${nodeId} on ${key}`);

    // 击伤可能重组锁表：重新获取该键的最新状态
    const fresh = this.keyState(key);
    const stillConflicting = this.conflictingHolders(fresh, nodeId, mode);
    if (stillConflicting.length === 0 && fresh.queue.length === 0) {
      this.grant(fresh, nodeId, key, mode);
      const grantSeq = this.record('GRANT', nodeId, key, `${mode} after wounding`);
      return Promise.resolve(this.newHandle(nodeId, key, mode, grantSeq));
    }

    // 存在更年长的冲突持有者或队列非空：只能等待
    this.record('WAIT', nodeId, key, `${mode} queued`);
    return new Promise<LockHandle>((resolve, reject) => {
      fresh.queue.push({ nodeId, mode, resolve, reject });
    });
  }

  private newHandle(nodeId: number, key: string, mode: LockMode, grantSeq: number): LockHandle {
    const handle: LockHandle = { nodeId, key, mode, grantSeq, valid: true, wounded: false, released: false };
    let set = this.liveHandles.get(nodeId);
    if (!set) {
      set = new Set();
      this.liveHandles.set(nodeId, set);
    }
    set.add(handle);
    return handle;
  }

  private grant(ks: KeyState, nodeId: number, key: string, mode: LockMode): void {
    const mine = ks.holders.get(nodeId);
    if (mine) {
      mine.mode = mode === 'EXCLUSIVE' ? 'EXCLUSIVE' : mine.mode;
      mine.refcount++;
    } else {
      ks.holders.set(nodeId, { mode, refcount: 1 });
    }
  }

  /** 释放锁；重复释放或被击伤后释放会抛错（防泄漏/防误用） */
  release(handle: LockHandle): void {
    if (handle.released) throw new Error(`double release by node ${handle.nodeId} on ${handle.key}`);
    if (handle.wounded) throw new WoundError(handle.nodeId, `node ${handle.nodeId} was wounded`);
    handle.released = true;
    handle.valid = false;
    this.liveHandles.get(handle.nodeId)?.delete(handle);
    const ks = this.table.get(handle.key);
    if (!ks) throw new Error(`no lock state for ${handle.key}`);
    const mine = ks.holders.get(handle.nodeId);
    if (!mine) throw new Error(`node ${handle.nodeId} holds no lock on ${handle.key}`);
    mine.refcount--;
    if (mine.refcount === 0) {
      ks.holders.delete(handle.nodeId);
      this.record('RELEASE', handle.nodeId, handle.key, handle.mode);
    }
    this.processQueue(handle.key, ks);
    if (ks.holders.size === 0 && ks.queue.length === 0) this.table.delete(handle.key);
  }

  /** 锁释放后按 FIFO 授予兼容前缀 */
  private processQueue(key: string, ks: KeyState): void {
    for (;;) {
      const req = ks.queue[0];
      if (!req) return;
      if (this.conflictingHolders(ks, req.nodeId, req.mode).length > 0) return;
      ks.queue.shift();
      this.grant(ks, req.nodeId, key, req.mode);
      const grantSeq = this.record('GRANT', req.nodeId, key, `${req.mode} from queue`);
      req.resolve(this.newHandle(req.nodeId, key, req.mode, grantSeq));
    }
  }

  /** 击伤节点：立即剥夺其全部锁、拒绝其全部等待请求，迫使其回滚 */
  private wound(nodeId: number, reason: string): void {
    // 1) 使该节点全部存活句柄失效：其进行中的事务将在提交前校验处被拦截
    for (const h of this.liveHandles.get(nodeId) ?? []) {
      h.valid = false;
      h.wounded = true;
    }
    this.liveHandles.delete(nodeId);
    // 2) 剥离其全部持有锁与排队请求（先剥离，避免等待队列处理时误授予被击伤者）
    const affected: string[] = [];
    for (const [key, ks] of this.table) {
      let touched = false;
      if (ks.holders.delete(nodeId)) {
        this.record('WOUND', nodeId, key, reason);
        touched = true;
      }
      for (let i = ks.queue.length - 1; i >= 0; i--) {
        if (ks.queue[i].nodeId === nodeId) {
          const [req] = ks.queue.splice(i, 1);
          this.record('ABORT', nodeId, key, reason);
          req.reject(new WoundError(nodeId, reason));
          touched = true;
        }
      }
      if (touched) affected.push(key);
    }
    // 3) 统一唤醒受影响键的等待队列
    for (const key of affected) {
      const ks = this.table.get(key);
      if (!ks) continue;
      this.processQueue(key, ks);
      if (ks.holders.size === 0 && ks.queue.length === 0) this.table.delete(key);
    }
    this.onWound(nodeId, reason);
  }

  /** 节点被击伤时的回调（测试/上层可订阅） */
  onWound: (nodeId: number, reason: string) => void = () => {};

  /**
   * 在排他锁保护下对 key 的寄存器做“读-改-写”事务。
   * 提交前校验句柄有效性：若期间被击伤则丢弃修改并抛 WoundError，
   * 成功提交则记录全局序号（线性化点）。
   */
  async transact<T>(nodeId: number, key: string, fn: (current: T | undefined) => T | Promise<T>): Promise<T> {
    const handle = await this.acquire(nodeId, key, 'EXCLUSIVE');
    try {
      const current = this.registers.get(key) as T | undefined;
      const next = await fn(current);
      // 检查与提交之间无 await：原子提交点
      if (!handle.valid || handle.wounded) {
        throw new WoundError(nodeId, `node ${nodeId} wounded before commit on ${key}`);
      }
      this.registers.set(key, next);
      this.record('COMMIT', nodeId, key);
      return next;
    } finally {
      if (!handle.released && !handle.wounded) this.release(handle);
    }
  }

  /** 带重试的事务：复用同一 nodeId（时间戳不变），保持 wound-wait 无死锁不变式 */
  async transactWithRetry<T>(
    nodeId: number,
    key: string,
    fn: (current: T | undefined) => T | Promise<T>,
    maxRetries = 1000,
  ): Promise<T> {
    for (let attempt = 0; ; attempt++) {
      try {
        return await this.transact(nodeId, key, fn);
      } catch (e) {
        if (e instanceof WoundError && attempt < maxRetries) continue;
        throw e;
      }
    }
  }

  readRegister<T>(key: string): T | undefined {
    return this.registers.get(key) as T | undefined;
  }

  /** 无泄漏断言：所有锁已释放、所有等待队列已清空 */
  assertQuiescent(): void {
    const leaks: string[] = [];
    for (const [key, ks] of this.table) {
      if (ks.holders.size > 0) leaks.push(`key ${key} held by [${[...ks.holders.keys()]}]`);
      if (ks.queue.length > 0) leaks.push(`key ${key} has ${ks.queue.length} waiters`);
    }
    for (const [nodeId, set] of this.liveHandles) {
      if (set.size > 0) leaks.push(`node ${nodeId} has ${set.size} live handles`);
    }
    if (leaks.length > 0) throw new Error(`arbiter leak: ${leaks.join('; ')}`);
  }

  heldLockCount(): number {
    let n = 0;
    for (const ks of this.table.values()) n += ks.holders.size;
    return n;
  }

  pendingWaiterCount(): number {
    let n = 0;
    for (const ks of this.table.values()) n += ks.queue.length;
    return n;
  }
}
