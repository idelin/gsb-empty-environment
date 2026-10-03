/**
 * 全局读视图对齐器（Global Read-View Aligner）
 *
 * 核心职责：协调各分片的 CDC 流，计算全局因果闭合的安全时间戳 T_Safe，
 * 仅在因果闭合时对外输出一致性读视图。
 *
 * 算法：
 *   1. 水位线基线 base = min(各分片 heartbeat 水位线)。
 *      任一分片未上报心跳 => 无法保证其 commitTs <= T 的事件已全部到达 => 阻塞。
 *   2. 因果闭合约束：任何已收到部分事件、但未收齐全部参与者事件的事务，
 *      都视为“未闭合”。只要存在未闭合事务，对齐器拒绝生成查询视图——
 *      否则快照可能只含转出、不含转入，导致钱凭空消失。
 *   3. 闭合后 T_Safe = min(各分片水位线)，快照应用所有 commitTs <= T_Safe
 *      的已闭合事务事件，并校验不变量：快照内绝不允许出现半事务。
 */
import type { CdcEvent, Heartbeat, ReadView, ViewResult } from './types.ts';

interface TxnTracker {
  txnId: string;
  expectedParts: number;
  events: CdcEvent[];
}

export class GlobalReadViewAligner {
  private readonly shardIds: string[];
  private readonly watermarks = new Map<string, number>();
  private readonly txns = new Map<string, TxnTracker>();
  private readonly initialBalances: Record<string, number>;

  constructor(shardIds: string[], initialBalances: Record<string, number>) {
    this.shardIds = [...shardIds];
    this.initialBalances = { ...initialBalances };
  }

  /** 摄入一条 CDC 事件 */
  ingest(event: CdcEvent): void {
    if (!this.shardIds.includes(event.shardId)) {
      throw new Error(`unknown shard: ${event.shardId}`);
    }
    let tracker = this.txns.get(event.txnId);
    if (!tracker) {
      tracker = {
        txnId: event.txnId,
        expectedParts: event.closure.expectedParts,
        events: [],
      };
      this.txns.set(event.txnId, tracker);
    }
    tracker.events.push(event);
  }

  /** 摄入分片水位线心跳 */
  heartbeat(hb: Heartbeat): void {
    if (!this.shardIds.includes(hb.shardId)) {
      throw new Error(`unknown shard: ${hb.shardId}`);
    }
    const current = this.watermarks.get(hb.shardId) ?? Number.NEGATIVE_INFINITY;
    if (hb.ts > current) this.watermarks.set(hb.shardId, hb.ts);
  }

  /** 未闭合事务（已收到部分事件但未收齐） */
  private pendingTxns(): TxnTracker[] {
    return [...this.txns.values()].filter(
      (t) => t.events.length > 0 && t.events.length < t.expectedParts,
    );
  }

  /**
   * 计算 T_Safe。
   * 返回 undefined 表示无法给出任何安全时间戳（有分片从未心跳）。
   * 未闭合事务会把 T_Safe 压到其首个事件之前，保证快照不含半事务。
   */
  computeSafeTs(): number | undefined {
    let base = Number.POSITIVE_INFINITY;
    for (const shardId of this.shardIds) {
      const wm = this.watermarks.get(shardId);
      if (wm === undefined) return undefined; // 分片从未心跳，无法保证完整性
      base = Math.min(base, wm);
    }
    for (const txn of this.pendingTxns()) {
      const t0 = Math.min(...txn.events.map((e) => e.commitTs));
      base = Math.min(base, t0 - 1); // 因果闭合约束：快照不得包含半事务
    }
    return base;
  }

  /**
   * 尝试生成全局一致性读视图。
   * 只要存在未闭合事务（或分片心跳缺失），即拒绝输出视图。
   */
  getReadView(): ViewResult {
    const pending = this.pendingTxns();
    const safeTs = this.computeSafeTs();

    if (safeTs === undefined) {
      return {
        status: 'blocked',
        reason: '存在未上报水位线的分片，无法保证事件完整性',
        pendingTxns: pending.map((t) => t.txnId),
      };
    }

    // 硬约束：存在未闭合事务时，阻止外部生成查询视图
    if (pending.length > 0) {
      return {
        status: 'blocked',
        reason: `事务未因果闭合：${pending
          .map((t) => t.txnId)
          .join(', ')}（已收到部分事件，等待其余分片事件就绪）`,
        pendingTxns: pending.map((t) => t.txnId),
      };
    }

    // 物化快照：应用所有 commitTs <= safeTs 的事件
    const balances: Record<string, number> = { ...this.initialBalances };
    const closedTxns: string[] = [];
    for (const txn of this.txns.values()) {
      const visible = txn.events.filter((e) => e.commitTs <= safeTs);
      if (visible.length === 0) continue;
      if (visible.length !== txn.expectedParts) {
        // 防御性不变量：快照内绝不允许出现半事务
        throw new Error(`invariant violated: partial txn ${txn.txnId} in snapshot`);
      }
      for (const e of visible) {
        balances[e.account] =
          (balances[e.account] ?? 0) + (e.kind === 'credit' ? e.amount : -e.amount);
      }
      closedTxns.push(txn.txnId);
    }

    const view: ReadView = { status: 'ready', safeTs, balances, closedTxns };
    return view;
  }
}
