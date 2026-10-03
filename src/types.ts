/**
 * 全局读视图对齐器 —— 类型定义
 *
 * 各分片拥有独立的本地提交时间戳（commitTs），通过 CDC 流上报事件。
 * 跨分片事务（如转账）由 txnId 关联，并声明其因果闭包所需的全部参与者，
 * 对齐器据此判断一笔事务是否“因果闭合”。
 */

/** 分片产生的一条 CDC 变更事件 */
export interface CdcEvent {
  /** 产生事件的分片 ID */
  shardId: string;
  /** 全局事务 ID（跨分片事务的关联键） */
  txnId: string;
  /** 事件类型：扣款 / 入账 */
  kind: 'debit' | 'credit';
  /** 账户 ID */
  account: string;
  /** 金额（正数） */
  amount: number;
  /** 分片本地提交时间戳（分片内单调递增，分片间不可直接比较） */
  commitTs: number;
  /** 因果闭包元信息：本事务涉及的全部分片，以及预期的事件总条数 */
  closure: {
    participants: string[];
    expectedParts: number;
  };
}

/** 分片水位线心跳：承诺不再产生 commitTs <= ts 的事件 */
export interface Heartbeat {
  shardId: string;
  ts: number;
}

/** 一致性快照（仅在因果闭合时产出） */
export interface ReadView {
  status: 'ready';
  /** 全局安全时间戳：所有 commitTs <= safeTs 的事件构成自洽数据集 */
  safeTs: number;
  /** 快照中的账户余额 */
  balances: Record<string, number>;
  /** 快照包含的已闭合事务 */
  closedTxns: string[];
}

/** 对齐器尚未闭合时的阻塞响应 */
export interface BlockedView {
  status: 'blocked';
  reason: string;
  /** 正在等待闭合的事务 */
  pendingTxns: string[];
}

export type ViewResult = ReadView | BlockedView;
