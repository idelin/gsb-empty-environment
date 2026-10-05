/** 月份键，格式 'YYYY-MM' */
export type Month = string;

/** 代理 / 节点 ID */
export type AgentId = string;

/** 一条从交易来源到收款上级的完整来源链 */
export interface SourceChain {
  /** 路径上的节点，path[0] 为交易来源，末位为收款上级 */
  path: AgentId[];
  /** 沿路径各边费率的乘积 */
  rate: number;
}

export interface TransactionInput {
  txId: string;
  /** 交易归属（直接产生业绩的）节点 */
  source: AgentId;
  /** 交易金额（元，正数），内部按分取整 */
  amount: number;
  /** 归属月份 'YYYY-MM' */
  month: Month;
}

/**
 * 一笔交易对某个上级的分账记录。
 * 不变式：grossCents === paidCents + reversedCents + pendingCents + voidedCents
 */
export interface DistributionRecord {
  txId: string;
  /** 交易原始顺序号（补发排序依据） */
  txSeq: number;
  month: Month;
  recipient: AgentId;
  /** 应分总额（分） */
  grossCents: number;
  /** 实付（含补发、未含已冲销）（分） */
  paidCents: number;
  /** 撤单冲销（分） */
  reversedCents: number;
  /** 因月封顶被截、等待补发（分） */
  pendingCents: number;
  /** 撤单作废的被截部分（分） */
  voidedCents: number;
  /** 全部来源链（同一上级可经多条路径到达） */
  chains: SourceChain[];
  /** 实际计酬采用的来源链（费率最高者，并列取路径字典序最小） */
  effectiveChain: SourceChain;
  status: 'open' | 'cancelled';
}

export type LedgerKind = 'pay' | 'backpay' | 'reverse' | 'void';

/** 不可变流水，撤单更正以负向分录体现，绝不改写历史 */
export interface LedgerEntry {
  seq: number;
  kind: LedgerKind;
  txId: string;
  month: Month;
  agent: AgentId;
  /** 带符号金额（分）：pay/backpay 为正，reverse/void 为负 */
  amountCents: number;
  reason?: string;
}

export interface AgentMonthStatement {
  agent: AgentId;
  month: Month;
  grossCents: number;
  paidCents: number;
  backpaidCents: number;
  reversedCents: number;
  voidedCents: number;
  pendingCents: number;
  netPaidCents: number;
  capCents: number | null;
  records: DistributionRecord[];
}

export interface MonthReconciliation {
  month: Month;
  grossCents: number;
  paidCents: number;
  backpaidCents: number;
  reversedCents: number;
  voidedCents: number;
  pendingCents: number;
  netPaidCents: number;
  /** gross === netPaid + reversed + pending + voided */
  conserved: boolean;
  /** 是否存在代理净实付超过月封顶 */
  capViolations: AgentId[];
}
