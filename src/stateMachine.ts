/**
 * 多分区确定性事务状态机。
 *
 * 核心不变式：
 *  1. 事件只按全局序号 seq 连续回放（乱序到达先进入重排缓冲）。
 *  2. 同一日志流 => 同一回放顺序 => 同一最终状态（与网络到达顺序无关）。
 *  3. 内存有界：重排缓冲、未决事务表均有上限；快照后截断已回放日志。
 */

import type {
  ConflictDetail,
  Evidence,
  LogEvent,
  PartitionId,
  Snapshot,
  TxnId,
} from './types.ts';
import { topoSort } from './topo.ts';

export interface StateMachineOptions {
  partitionCount: number;
  /** 未决事务表上限（默认 1024），超限按 seq 最旧者确定性驱逐。 */
  maxPending?: number;
  /** 乱序重排缓冲上限（默认 4096），超限说明日志缺口过大。 */
  maxBuffer?: number;
}

export const TIE_BREAK_RULE =
  'commit-in-log-order-wins; pending-conflict-losers-aborted; order=(seq,txnId)';

export class StateMachine {
  readonly partitionCount: number;
  private readonly maxPending: number;
  private readonly maxBuffer: number;

  private readonly partitions: Map<PartitionId, Map<string, string>> = new Map();
  /** 已 prepare 未裁决的事务（txnId -> prepare 事件）。 */
  private readonly pending: Map<TxnId, LogEvent> = new Map();
  /** 乱序到达的重排缓冲（seq -> 事件）。 */
  private readonly buffer: Map<number, LogEvent> = new Map();
  /** 下一个期望回放的序号（快照切片点）。 */
  private nextSeq = 0;

  /** 审计证据流（决胜 + 驱逐）。 */
  readonly evidence: Evidence[] = [];
  /** 已提交事务顺序（回放确定性的可观察结果）。 */
  readonly committed: TxnId[] = [];
  /** 切片点之后保留的已回放日志；快照时截断，验证内存有界。 */
  readonly retainedLog: LogEvent[] = [];
  readonly stats = { applied: 0, duplicates: 0, ignoredCommits: 0, evictions: 0 };

  constructor(options: StateMachineOptions) {
    this.partitionCount = options.partitionCount;
    this.maxPending = options.maxPending ?? 1024;
    this.maxBuffer = options.maxBuffer ?? 4096;
    for (let p = 0; p < this.partitionCount; p++) this.partitions.set(p, new Map());
  }

  /** 事件入口：允许乱序、重复到达；内部按 seq 连续回放。 */
  ingest(event: LogEvent): void {
    if (event.seq < this.nextSeq || this.buffer.has(event.seq)) {
      this.stats.duplicates++; // 已回放或已在缓冲：幂等忽略
      return;
    }
    this.buffer.set(event.seq, event);
    if (this.buffer.size > this.maxBuffer) {
      throw new Error(
        `reorder buffer overflow (>${this.maxBuffer}): log gap too large at seq=${event.seq}`,
      );
    }
    while (this.buffer.has(this.nextSeq)) {
      const e = this.buffer.get(this.nextSeq) as LogEvent;
      this.buffer.delete(this.nextSeq);
      this.apply(e);
      this.retainedLog.push(e);
      this.stats.applied++;
      this.nextSeq++;
    }
  }

  /** 当前已回放到（不含）的序号，即快照可切的位置。 */
  get appliedSeq(): number {
    return this.nextSeq;
  }

  get pendingCount(): number {
    return this.pending.size;
  }

  get(partition: PartitionId, key: string): string | undefined {
    return this.partitions.get(partition)?.get(key);
  }

  private apply(event: LogEvent): void {
    switch (event.kind) {
      case 'prepare':
        this.applyPrepare(event);
        break;
      case 'commit':
        this.applyCommit(event);
        break;
      case 'abort':
        this.pending.delete(event.txnId);
        break;
    }
  }

  private applyPrepare(event: LogEvent): void {
    if (this.pending.size >= this.maxPending) {
      // 内存有界：确定性驱逐 seq 最旧的未决事务，并留下审计证据。
      let oldest: LogEvent | undefined;
      for (const p of this.pending.values()) {
        if (!oldest || p.seq < oldest.seq || (p.seq === oldest.seq && p.txnId < oldest.txnId)) {
          oldest = p;
        }
      }
      if (oldest) {
        this.pending.delete(oldest.txnId);
        this.stats.evictions++;
        this.evidence.push({
          kind: 'memory-bound-eviction',
          evicted: oldest.txnId,
          reason: 'pending-table-full: evicted lowest (seq,txnId)',
          pendingSize: this.pending.size,
          maxPending: this.maxPending,
        });
      }
    }
    this.pending.set(event.txnId, event);
  }

  private applyCommit(event: LogEvent): void {
    const txn = this.pending.get(event.txnId);
    if (!txn) {
      this.stats.ignoredCommits++; // 已被决胜/驱逐/中止：commit 为幂等空操作
      return;
    }

    // 1. 找出与提交事务直接冲突的未决事务。
    const contenders: { txn: LogEvent; conflicts: ConflictDetail[] }[] = [];
    for (const other of this.pending.values()) {
      if (other.txnId === txn.txnId) continue;
      const conflicts = conflictDetails(txn, other);
      if (conflicts.length > 0) contenders.push({ txn: other, conflicts });
    }

    if (contenders.length > 0) {
      // 2. 构建依赖图（seq 小 -> seq 大）并做确定性拓扑排序。
      const nodes = [txn, ...contenders.map((c) => c.txn)];
      const edges = new Map<string, Set<string>>();
      for (let i = 0; i < nodes.length; i++) {
        for (let j = i + 1; j < nodes.length; j++) {
          if (conflictDetails(nodes[i], nodes[j]).length === 0) continue;
          const [a, b] = nodes[i].seq < nodes[j].seq ? [nodes[i], nodes[j]] : [nodes[j], nodes[i]];
          if (!edges.has(a.txnId)) edges.set(a.txnId, new Set());
          (edges.get(a.txnId) as Set<string>).add(b.txnId);
        }
      }
      const topoOrder = topoSort(
        nodes.map((n) => ({ id: n.txnId, seq: n.seq })),
        edges,
      ).map((n) => n.id);

      // 3. 决胜：日志序先到 commit 者胜，未决冲突方全部中止，证据可审计。
      const losers = contenders.map((c) => c.txn.txnId);
      this.evidence.push({
        kind: 'conflict-tiebreak',
        decidingSeq: event.seq,
        winner: txn.txnId,
        losers,
        rule: TIE_BREAK_RULE,
        topoOrder,
        conflicts: contenders.flatMap((c) => c.conflicts),
      });
      for (const c of contenders) this.pending.delete(c.txn.txnId);
    }

    // 4. 应用写集：分区、键均按序遍历，保证副作用顺序确定。
    for (const p of sortedPartitionIds(txn.writes)) {
      const store = this.partitions.get(p);
      if (!store) throw new Error(`unknown partition ${p}`);
      for (const key of Object.keys(txn.writes[p]).sort()) {
        store.set(key, txn.writes[p][key]);
      }
    }
    this.pending.delete(txn.txnId);
    this.committed.push(txn.txnId);
  }

  /** 生成快照并截断已回放日志（内存有界）。 */
  createSnapshot(): Snapshot {
    const partitions: Record<PartitionId, Record<string, string>> = {};
    for (const [p, store] of this.partitions) {
      const slice: Record<string, string> = {};
      for (const key of [...store.keys()].sort()) slice[key] = store.get(key) as string;
      partitions[p] = slice;
    }
    const snapshot: Snapshot = {
      nextSeq: this.nextSeq,
      partitions,
      pending: [...this.pending.values()].map((e) => structuredClone(e)),
    };
    this.retainedLog.length = 0; // 切片点之前的日志可安全丢弃
    return snapshot;
  }

  /** 从快照恢复（崩溃恢复的第一步）。 */
  static restore(snapshot: Snapshot, options: StateMachineOptions): StateMachine {
    const sm = new StateMachine(options);
    sm.nextSeq = snapshot.nextSeq;
    for (const [p, slice] of Object.entries(snapshot.partitions)) {
      const store = sm.partitions.get(Number(p));
      if (!store) throw new Error(`unknown partition ${p}`);
      for (const [k, v] of Object.entries(slice)) store.set(k, v);
    }
    for (const e of snapshot.pending) sm.pending.set(e.txnId, structuredClone(e));
    return sm;
  }

  /**
   * 崩溃恢复：快照（可为空）+ 切片点之后的日志重放。
   * 日志允许乱序、允许包含切片点之前的旧事件（幂等忽略）。
   */
  static recover(
    snapshot: Snapshot | null,
    events: Iterable<LogEvent>,
    options: StateMachineOptions,
  ): StateMachine {
    const sm = snapshot ? StateMachine.restore(snapshot, options) : new StateMachine(options);
    for (const e of events) sm.ingest(e);
    return sm;
  }

  /** 状态的确定性序列化（分区、键均排序），用于一致性比对。 */
  canonicalState(): string {
    const out: Record<string, Record<string, string>> = {};
    for (const p of [...this.partitions.keys()].sort((a, b) => a - b)) {
      const store = this.partitions.get(p) as Map<string, string>;
      const slice: Record<string, string> = {};
      for (const key of [...store.keys()].sort()) slice[key] = store.get(key) as string;
      out[String(p)] = slice;
    }
    return JSON.stringify(out);
  }
}

/** 计算 a（提交方）与 b（未决方）之间的全部键级冲突。 */
export function conflictDetails(a: LogEvent, b: LogEvent): ConflictDetail[] {
  const out: ConflictDetail[] = [];
  const shared = new Set([...a.partitions, ...b.partitions]);
  for (const p of [...shared].sort((x, y) => x - y)) {
    const aW = new Set(Object.keys(a.writes[p] ?? {}));
    const aR = new Set(a.reads[p] ?? []);
    const bW = new Set(Object.keys(b.writes[p] ?? {}));
    const bR = new Set(b.reads[p] ?? []);
    const push = (keys: Set<string>, kind: ConflictDetail['kind']) => {
      for (const key of [...keys].sort()) out.push({ other: b.txnId, partition: p, key, kind });
    };
    push(intersection(aW, bW), 'write-write');
    push(intersection(aW, bR), 'write-read');
    push(intersection(aR, bW), 'read-write');
  }
  return out;
}

function intersection(a: Set<string>, b: Set<string>): Set<string> {
  const out = new Set<string>();
  for (const x of a) if (b.has(x)) out.add(x);
  return out;
}

function sortedPartitionIds(writes: Record<PartitionId, Record<string, string>>): PartitionId[] {
  return Object.keys(writes).map(Number).sort((a, b) => a - b);
}
