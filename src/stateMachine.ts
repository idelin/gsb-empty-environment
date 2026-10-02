import type {
  ApplyRecord,
  AuditRecord,
  LogEntry,
  PrepareEvent,
  TieBreakEvidence,
  TxnEvent,
  TxnId,
} from './types.ts';
import { compareOrderKey, orderKeyOf, TIE_BREAK_RULE } from './types.ts';

export interface StateMachineOptions {
  numPartitions: number;
  /** 未决事务表上限：超过即拒绝新的 prepare（内存有界）。 */
  maxPending?: number;
  /** 已决事务结果缓存上限（FIFO 逐出，用于去重迟到事件）。 */
  resolvedCacheSize?: number;
  /** 先于 prepare 到达的 commit/abort 缓冲上限。 */
  maxEarlyDecisions?: number;
  /** 审计环形缓冲容量。 */
  auditCapacity?: number;
}

interface PendingTxn {
  prepare: PrepareEvent;
  decision: 'commit' | 'abort' | null;
}

export interface Snapshot {
  version: 1;
  consumedSeq: number;
  numPartitions: number;
  partitions: [string, string][][];
  pending: { txnId: TxnId; prepare: PrepareEvent; decision: PendingTxn['decision'] }[];
  earlyDecisions: [TxnId, 'commit' | 'abort'][];
  resolved: [TxnId, 'committed' | 'aborted'][];
  appliedOrder: TxnId[];
  auditTail: AuditRecord[];
  auditTotal: number;
}

/** FNV-1a 32bit：确定性的键 -> 分区映射。 */
export function hashKey(key: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < key.length; i++) {
    h ^= key.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

/** 读写集合冲突键（ww / wr / rw；纯读读不算冲突），返回排序后的键列表。 */
export function conflictKeys(a: PrepareEvent, b: PrepareEvent): string[] {
  const wsA = new Set(Object.keys(a.writes));
  const rsA = new Set(a.reads);
  const wsB = new Set(Object.keys(b.writes));
  const rsB = new Set(b.reads);
  const keys = new Set<string>();
  for (const k of wsA) if (wsB.has(k) || rsB.has(k)) keys.add(k);
  for (const k of rsA) if (wsB.has(k)) keys.add(k);
  return [...keys].sort();
}

/** 有界环形审计缓冲：保留最近 N 条，total 记录历史总量。 */
export class AuditLog {
  private buf: AuditRecord[] = [];
  total = 0;
  readonly capacity: number;

  constructor(capacity: number) {
    this.capacity = capacity;
  }

  push(record: AuditRecord): void {
    this.buf.push(record);
    if (this.buf.length > this.capacity) this.buf.shift();
    this.total++;
  }

  records(): readonly AuditRecord[] {
    return this.buf;
  }

  ofType<T extends AuditRecord['type']>(
    type: T,
  ): Extract<AuditRecord, { type: T }>[] {
    return this.buf.filter((r) => r.type === type) as never;
  }
}

/**
 * 多分区确定性事务状态机。
 *
 * 确定性保证：事务的应用顺序只由 (lamport, txnId) 排序键与读写集合冲突图
 * 决定（冲突图上的拓扑排序，排序键作决胜），与事件到达顺序无关，
 * 因此乱序网络包不会改变最终键值状态。
 *
 * 处理方式为确定性批处理：事件先进入未决缓冲，批次边界 seal() 时
 * 状态机已拥有完整视图，对所有已决事务按冲突图拓扑序统一定局。
 * 最终状态因此只是事件集合的函数，而非到达顺序的函数。
 */
export class StateMachine {
  readonly numPartitions: number;
  readonly audit: AuditLog;

  private readonly maxPending: number;
  private readonly resolvedCacheSize: number;
  private readonly maxEarlyDecisions: number;

  private partitions: Map<string, string>[];
  private pending = new Map<TxnId, PendingTxn>();
  private resolved = new Map<TxnId, 'committed' | 'aborted'>();
  private earlyDecisions = new Map<TxnId, 'commit' | 'abort'>();
  private appliedOrder: TxnId[] = [];
  /** 已消费的日志位置（快照切片点）。 */
  consumedSeq = 0;

  constructor(options: StateMachineOptions) {
    this.numPartitions = options.numPartitions;
    this.maxPending = options.maxPending ?? 1024;
    this.resolvedCacheSize = options.resolvedCacheSize ?? 4096;
    this.maxEarlyDecisions = options.maxEarlyDecisions ?? 1024;
    this.audit = new AuditLog(options.auditCapacity ?? 10_000);
    this.partitions = Array.from({ length: this.numPartitions }, () => new Map());
  }

  partitionOf(key: string): number {
    return hashKey(key) % this.numPartitions;
  }

  /** 消费一条日志条目（回放入口）。 */
  applyEntry(entry: LogEntry): void {
    this.consumedSeq = entry.seq + 1;
    this.dispatch(entry.event);
  }

  applyEvent(event: TxnEvent): void {
    this.dispatch(event);
  }

  private dispatch(event: TxnEvent): void {
    switch (event.kind) {
      case 'prepare':
        this.onPrepare(event);
        break;
      case 'commit':
      case 'abort':
        this.onDecision(event.txnId, event.kind);
        break;
    }
  }

  private onPrepare(event: PrepareEvent): void {
    const id = event.txnId;
    if (this.pending.has(id) || this.resolved.has(id)) {
      this.audit.push({ type: 'duplicate-event', txnId: id, eventKind: 'prepare' });
      return;
    }
    if (this.pending.size >= this.maxPending) {
      this.audit.push({
        type: 'reject-prepare',
        txnId: id,
        reason: 'max-pending-exceeded',
        pendingCount: this.pending.size,
        maxPending: this.maxPending,
      });
      return;
    }

    // 与现有未决事务比对冲突，产出可审计决胜证据（每个无序对只产出一次）。
    for (const other of this.pending.values()) {
      const keys = conflictKeys(event, other.prepare);
      if (keys.length === 0) continue;
      const keyA = orderKeyOf(event);
      const keyB = orderKeyOf(other.prepare);
      const aFirst = compareOrderKey(keyA, keyB) < 0;
      const evidence: TieBreakEvidence = {
        type: 'tie-break',
        txnA: aFirst ? id : other.prepare.txnId,
        txnB: aFirst ? other.prepare.txnId : id,
        conflictKeys: keys,
        orderKeyA: aFirst ? keyA : keyB,
        orderKeyB: aFirst ? keyB : keyA,
        winner: aFirst ? id : other.prepare.txnId,
        rule: TIE_BREAK_RULE,
      };
      this.audit.push(evidence);
    }

    const early = this.earlyDecisions.get(id);
    if (early !== undefined) this.earlyDecisions.delete(id);
    this.pending.set(id, { prepare: event, decision: early ?? null });
  }

  private onDecision(txnId: TxnId, decision: 'commit' | 'abort'): void {
    const txn = this.pending.get(txnId);
    if (txn) {
      if (txn.decision !== null) {
        this.audit.push({ type: 'duplicate-event', txnId, eventKind: decision });
        return;
      }
      txn.decision = decision;
      return;
    }
    if (this.resolved.has(txnId)) {
      this.audit.push({ type: 'duplicate-event', txnId, eventKind: decision });
      return;
    }
    // 乱序网络：commit/abort 可能先于 prepare 到达，缓冲等待。
    if (!this.earlyDecisions.has(txnId)) {
      if (this.earlyDecisions.size >= this.maxEarlyDecisions) {
        const oldest = this.earlyDecisions.keys().next().value!;
        this.earlyDecisions.delete(oldest);
      }
      this.earlyDecisions.set(txnId, decision);
    } else {
      this.audit.push({ type: 'duplicate-event', txnId, eventKind: decision });
    }
  }

  /**
   * 批次边界（确定性落定点）。
   *
   * 调用时本批次的事件均已到达，状态机拥有完整冲突图；
   * 所有已决事务按拓扑序应用，结果与批次内的到达顺序无关。
   */
  seal(): void {
    this.drain();
  }

  /**
   * 冲突图拓扑排序的增量执行：
   * 已决事务中，所有排序键更小的冲突前驱都已落定时，该事务即可应用；
   * 多个候选同时就绪时取排序键最小者（确定性决胜）。
   */
  private drain(): void {
    for (;;) {
      let best: PendingTxn | null = null;
      for (const txn of this.pending.values()) {
        if (txn.decision === null) continue;
        if (this.isBlocked(txn)) continue;
        if (
          best === null ||
          compareOrderKey(orderKeyOf(txn.prepare), orderKeyOf(best.prepare)) < 0
        ) {
          best = txn;
        }
      }
      if (best === null) return;
      this.resolve(best);
    }
  }

  private isBlocked(txn: PendingTxn): boolean {
    const key = orderKeyOf(txn.prepare);
    for (const other of this.pending.values()) {
      if (other.prepare.txnId === txn.prepare.txnId) continue;
      if (compareOrderKey(orderKeyOf(other.prepare), key) >= 0) continue;
      if (conflictKeys(txn.prepare, other.prepare).length > 0) return true;
    }
    return false;
  }

  private resolve(txn: PendingTxn): void {
    const id = txn.prepare.txnId;
    if (txn.decision === 'commit') {
      const touched = new Set<number>();
      for (const [key, value] of Object.entries(txn.prepare.writes)) {
        const p = this.partitionOf(key);
        this.partitions[p]!.set(key, value);
        touched.add(p);
      }
      const record: ApplyRecord = {
        type: 'apply',
        txnId: id,
        orderKey: orderKeyOf(txn.prepare),
        writes: { ...txn.prepare.writes },
        partitions: [...touched].sort((x, y) => x - y),
      };
      this.audit.push(record);
      this.resolved.set(id, 'committed');
    } else {
      this.audit.push({
        type: 'abort-applied',
        txnId: id,
        orderKey: orderKeyOf(txn.prepare),
      });
      this.resolved.set(id, 'aborted');
    }
    this.pending.delete(id);
    this.appliedOrder.push(id);
    // 有界内存：已决结果缓存 FIFO 逐出。
    while (this.resolved.size > this.resolvedCacheSize) {
      const oldest = this.resolved.keys().next().value!;
      this.resolved.delete(oldest);
    }
  }

  /** 合并各分区的只读视图（键排序，便于确定性比较）。 */
  state(): Record<string, string> {
    const merged: Record<string, string> = {};
    for (const part of this.partitions) {
      for (const [k, v] of part) merged[k] = v;
    }
    return Object.fromEntries(
      Object.entries(merged).sort(([a], [b]) => (a < b ? -1 : 1)),
    );
  }

  partitionState(p: number): Record<string, string> {
    return Object.fromEntries(
      [...this.partitions[p]!.entries()].sort(([a], [b]) => (a < b ? -1 : 1)),
    );
  }

  getAppliedOrder(): readonly TxnId[] {
    return this.appliedOrder;
  }

  pendingCount(): number {
    return this.pending.size;
  }

  resolvedCount(): number {
    return this.resolved.size;
  }

  /** 快照：捕获当前状态与已消费日志位置（切片点）。 */
  snapshot(): Snapshot {
    return {
      version: 1,
      consumedSeq: this.consumedSeq,
      numPartitions: this.numPartitions,
      partitions: this.partitions.map((m) => [...m.entries()].sort()),
      pending: [...this.pending.values()].map((p) => ({
        txnId: p.prepare.txnId,
        prepare: p.prepare,
        decision: p.decision,
      })),
      earlyDecisions: [...this.earlyDecisions.entries()],
      resolved: [...this.resolved.entries()],
      appliedOrder: [...this.appliedOrder],
      auditTail: [...this.audit.records()],
      auditTotal: this.audit.total,
    };
  }

  /** 崩溃恢复：从快照重建，之后用日志从 snapshot.consumedSeq 继续回放。 */
  static restore(
    snap: Snapshot,
    options?: Partial<StateMachineOptions>,
  ): StateMachine {
    const sm = new StateMachine({
      numPartitions: snap.numPartitions,
      ...options,
    });
    sm.consumedSeq = snap.consumedSeq;
    sm.partitions = snap.partitions.map((entries) => new Map(entries));
    for (const p of snap.pending) {
      sm.pending.set(p.txnId, { prepare: p.prepare, decision: p.decision });
    }
    sm.earlyDecisions = new Map(snap.earlyDecisions);
    sm.resolved = new Map(snap.resolved);
    sm.appliedOrder = [...snap.appliedOrder];
    for (const record of snap.auditTail) sm.audit.push(record);
    return sm;
  }
}
