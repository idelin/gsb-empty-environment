/** 月份，格式 "YYYY-MM" */
export type Month = string;
export type AgentId = string;
export type TxId = string;

/** 归属链上的一条边：from 的直接上级是 to，按 rateBps（万分比）计提 */
export interface ChainStep {
  from: AgentId;
  to: AgentId;
  rateBps: number;
}

/** 一条完整来源链：从交易归属人一路到某个上级 */
export interface SourceChain {
  /** 链的终点，即本链的佣金接收人 */
  recipient: AgentId;
  steps: ChainStep[];
  /** 有效费率（万分比），等于沿途各边费率的连乘折合 */
  effectiveRateBps: number;
}

export type LedgerKind = 'PAYOUT' | 'REVERSAL' | 'REISSUE';

/** 只增不删的流水账。REVERSAL 金额为负。 */
export interface LedgerEntry {
  /** 全局单调递增序号 */
  seq: number;
  kind: LedgerKind;
  txId: TxId;
  recipient: AgentId;
  month: Month;
  /** 有符号金额（最小货币单位，整数） */
  amount: number;
  chain: SourceChain;
  note?: string;
}

/** 被月封顶截断、等待补发的部分 */
export interface PendingEntry {
  txId: TxId;
  /** 原始交易顺序号，补发按它先进先出 */
  txSeq: number;
  recipient: AgentId;
  month: Month;
  remaining: number;
  chain: SourceChain;
}

export interface RecipientMonthReport {
  recipient: AgentId;
  /** 活跃交易应发合计 */
  due: number;
  /** 当前已实发净额（PAYOUT + REISSUE - REVERSAL） */
  issued: number;
  /** 仍被截断待补发 */
  pending: number;
  /** 本月累计撤单冲回金额（正数，信息项） */
  reversed: number;
  /** 守恒校验：due === issued + pending */
  balanced: boolean;
}

export interface MonthReport {
  month: Month;
  recipients: RecipientMonthReport[];
  totals: { due: number; issued: number; pending: number; reversed: number };
  balanced: boolean;
}
