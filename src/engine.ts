import { AttributionGraph } from './graph.ts';
import type {
  AgentId,
  AgentMonthStatement,
  DistributionRecord,
  LedgerEntry,
  LedgerKind,
  Month,
  MonthReconciliation,
  SourceChain,
  TransactionInput,
} from './types.ts';

const MONTH_RE = /^\d{4}-(0[1-9]|1[0-2])$/;

interface TxState {
  input: TransactionInput & { seq: number };
  records: DistributionRecord[];
  cancelled: boolean;
}

const toCents = (amount: number): number => Math.round(amount * 100);

/**
 * 佣金分配引擎：
 * - 沿 DAG 归属图向上分账，同一上级经多条路径到达时每笔交易只计一次（取费率最高链）；
 * - 每个代理可按月设置封顶，超出部分截断进入待补发队列；
 * - 撤单产生负向更正分录并释放封顶额度，随后按原交易顺序补发被截部分；
 * - 输出完整来源链，并提供逐月金额守恒核对。
 */
export class CommissionEngine {
  readonly graph = new AttributionGraph();

  private readonly caps = new Map<AgentId, number>();
  private readonly txs = new Map<string, TxState>();
  private readonly records: DistributionRecord[] = [];
  private readonly ledgerEntries: LedgerEntry[] = [];
  /** `${month}${agent}` -> 当月净实付（分），用于 O(1) 计算剩余封顶额度 */
  private readonly netPaidByMonthAgent = new Map<string, number>();
  private txSeq = 0;
  private ledgerSeq = 0;

  addAgent(id: AgentId, options: { monthlyCap?: number } = {}): void {
    this.graph.addNode(id);
    if (options.monthlyCap !== undefined) {
      if (!Number.isFinite(options.monthlyCap) || options.monthlyCap < 0) {
        throw new Error(`invalid monthlyCap: ${options.monthlyCap}`);
      }
      this.caps.set(id, toCents(options.monthlyCap));
    }
  }

  /** 登记 downline 的直接上级 upline 及该边费率 */
  setUpline(downline: AgentId, upline: AgentId, rate: number): void {
    this.graph.addEdge(downline, upline, rate);
  }

  /** 分账：返回本笔交易产生的全部分账记录（含完整来源链） */
  distribute(input: TransactionInput): DistributionRecord[] {
    if (!MONTH_RE.test(input.month)) throw new Error(`bad month: ${input.month}`);
    if (!Number.isFinite(input.amount) || input.amount <= 0) {
      throw new Error(`amount must be positive, got ${input.amount}`);
    }
    if (this.txs.has(input.txId)) throw new Error(`duplicate txId: ${input.txId}`);
    if (!this.graph.hasNode(input.source)) throw new Error(`unknown agent: ${input.source}`);

    const tx: TxState = {
      input: { ...input, seq: ++this.txSeq },
      records: [],
      cancelled: false,
    };
    this.txs.set(input.txId, tx);

    const amountCents = toCents(input.amount);
    const chainsByUpline = this.graph.enumerateChains(input.source);

    for (const [recipient, chains] of chainsByUpline) {
      const effectiveChain = pickEffectiveChain(chains);
      const grossCents = Math.round(amountCents * effectiveChain.rate);
      if (grossCents <= 0) continue;

      const paidCents = Math.min(grossCents, this.capRoom(input.month, recipient));
      const record: DistributionRecord = {
        txId: input.txId,
        txSeq: tx.input.seq,
        month: input.month,
        recipient,
        grossCents,
        paidCents: 0,
        reversedCents: 0,
        pendingCents: grossCents - paidCents,
        voidedCents: 0,
        chains,
        effectiveChain,
        status: 'open',
      };
      tx.records.push(record);
      this.records.push(record);
      if (paidCents > 0) this.apply('pay', record, paidCents);
    }
    return tx.records;
  }

  /**
   * 撤单更正：冲销该笔交易全部实付、作废其待补发部分，
   * 随后对释放了封顶额度的月份按原交易顺序补发被截部分。
   */
  cancel(txId: string, reason?: string): LedgerEntry[] {
    const tx = this.txs.get(txId);
    if (!tx) throw new Error(`unknown txId: ${txId}`);
    if (tx.cancelled) throw new Error(`tx already cancelled: ${txId}`);
    tx.cancelled = true;

    const before = this.ledgerEntries.length;
    for (const record of tx.records) {
      if (record.paidCents > 0) {
        this.apply('reverse', record, record.paidCents, reason);
      }
      if (record.pendingCents > 0) {
        this.apply('void', record, record.pendingCents, reason);
      }
      record.status = 'cancelled';
    }
    this.backpay(tx.input.month);
    return this.ledgerEntries.slice(before);
  }

  /** 某代理某月对账单（含每条记录的完整来源链） */
  statement(agent: AgentId, month: Month): AgentMonthStatement {
    const records = this.records.filter(
      (r) => r.recipient === agent && r.month === month,
    );
    const sum = (f: (r: DistributionRecord) => number): number =>
      records.reduce((acc, r) => acc + f(r), 0);
    const backpaidCents = this.ledgerEntries
      .filter((e) => e.kind === 'backpay' && e.agent === agent && e.month === month)
      .reduce((acc, e) => acc + e.amountCents, 0);
    return {
      agent,
      month,
      grossCents: sum((r) => r.grossCents),
      paidCents: sum((r) => r.paidCents),
      backpaidCents,
      reversedCents: sum((r) => r.reversedCents),
      voidedCents: sum((r) => r.voidedCents),
      pendingCents: sum((r) => r.pendingCents),
      netPaidCents: this.netPaid(month, agent),
      capCents: this.caps.get(agent) ?? null,
      records,
    };
  }

  /** 逐月金额守恒核对：gross === netPaid + reversed + pending + voided，且无人超封顶 */
  reconcile(): MonthReconciliation[] {
    const months = [...new Set(this.records.map((r) => r.month))].sort();
    return months.map((month) => {
      const recs = this.records.filter((r) => r.month === month);
      const sum = (f: (r: DistributionRecord) => number): number =>
        recs.reduce((acc, r) => acc + f(r), 0);
      const gross = sum((r) => r.grossCents);
      const reversed = sum((r) => r.reversedCents);
      const pending = sum((r) => r.pendingCents);
      const voided = sum((r) => r.voidedCents);
      const netPaid = sum((r) => r.paidCents);
      const agents = [...new Set(recs.map((r) => r.recipient))];
      const capViolations = agents.filter((a) => {
        const cap = this.caps.get(a);
        return cap !== undefined && this.netPaid(month, a) > cap;
      });
      return {
        month,
        grossCents: gross,
        paidCents: netPaid,
        backpaidCents: this.ledgerEntries
          .filter((e) => e.kind === 'backpay' && e.month === month)
          .reduce((acc, e) => acc + e.amountCents, 0),
        reversedCents: reversed,
        voidedCents: voided,
        pendingCents: pending,
        netPaidCents: netPaid,
        conserved: gross === netPaid + reversed + pending + voided,
        capViolations,
      };
    });
  }

  ledger(): readonly LedgerEntry[] {
    return this.ledgerEntries;
  }

  /** 全部分账记录（只读视图） */
  allRecords(): readonly DistributionRecord[] {
    return this.records;
  }

  private netPaid(month: Month, agent: AgentId): number {
    return this.netPaidByMonthAgent.get(`${month}${agent}`) ?? 0;
  }

  private capRoom(month: Month, agent: AgentId): number {
    const cap = this.caps.get(agent);
    if (cap === undefined) return Number.POSITIVE_INFINITY;
    return Math.max(0, cap - this.netPaid(month, agent));
  }

  /** 应用一条分录并同步记录与月度净实付 */
  private apply(
    kind: LedgerKind,
    record: DistributionRecord,
    cents: number,
    reason?: string,
  ): void {
    const sign = kind === 'pay' || kind === 'backpay' ? 1 : -1;
    switch (kind) {
      case 'pay':
      case 'backpay':
        record.paidCents += cents;
        if (kind === 'backpay') record.pendingCents -= cents;
        break;
      case 'reverse':
        record.paidCents -= cents;
        record.reversedCents += cents;
        break;
      case 'void':
        record.pendingCents -= cents;
        record.voidedCents += cents;
        break;
    }
    const key = `${record.month}${record.recipient}`;
    if (kind === 'pay' || kind === 'backpay') {
      this.netPaidByMonthAgent.set(key, this.netPaid(record.month, record.recipient) + cents);
    } else if (kind === 'reverse') {
      this.netPaidByMonthAgent.set(key, this.netPaid(record.month, record.recipient) - cents);
    }
    this.ledgerEntries.push({
      seq: ++this.ledgerSeq,
      kind,
      txId: record.txId,
      month: record.month,
      agent: record.recipient,
      amountCents: sign * cents,
      reason,
    });
  }

  /** 封顶释放后，按原交易顺序（txSeq，其次入档顺序）补发该月被截部分 */
  private backpay(month: Month): void {
    const queue = this.records
      .filter((r) => r.month === month && r.pendingCents > 0)
      .sort((a, b) => a.txSeq - b.txSeq);
    for (const record of queue) {
      const room = this.capRoom(month, record.recipient);
      if (room <= 0) continue;
      this.apply('backpay', record, Math.min(record.pendingCents, room));
    }
  }
}

/** 多路径去重：取费率最高的链，并列时取路径字典序最小者，保证确定性 */
function pickEffectiveChain(chains: SourceChain[]): SourceChain {
  let best = chains[0];
  for (const chain of chains.slice(1)) {
    if (
      chain.rate > best.rate ||
      (chain.rate === best.rate && chain.path.join('') < best.path.join(''))
    ) {
      best = chain;
    }
  }
  return best;
}
