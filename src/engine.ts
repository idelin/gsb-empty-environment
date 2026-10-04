import { AttributionGraph, type RawChain } from './graph.ts';
import type {
  AgentId,
  LedgerEntry,
  Month,
  MonthReport,
  PendingEntry,
  RecipientMonthReport,
  SourceChain,
  TxId,
} from './types.ts';

export interface TransactionInput {
  id: TxId;
  /** 交易归属人（佣金从这里向上传导） */
  agentId: AgentId;
  /** 交易金额（最小货币单位，非负整数） */
  amount: number;
  month: Month;
}

interface DueRecord {
  recipient: AgentId;
  /** 本交易对该接收人的应发金额（计算结果，不受封顶影响） */
  amount: number;
  chain: SourceChain;
  /** 当前已实发、仍计入月封顶的净额 */
  issued: number;
}

interface TransactionRecord {
  id: TxId;
  agentId: AgentId;
  amount: number;
  month: Month;
  seq: number;
  status: 'ACTIVE' | 'REVERSED';
  dues: DueRecord[];
}

export interface ChainExplanation {
  chain: SourceChain;
  dueAmount: number;
  /** 同一上级多路径去重后，本链是否被选中 */
  selected: boolean;
}

const MONTH_PATTERN = /^\d{4}-(0[1-9]|1[0-2])$/;

/**
 * 佣金分配引擎：
 * - 沿 DAG 归属图向上分账，同一上级经多条路径到达时只计一次（取有效费率最高的链）；
 * - 每个接收人按月封顶，超出部分截断进入待补发队列；
 * - 撤单冲回已发金额并释放封顶额度，随后按原始交易顺序补发被截部分；
 * - 全部流水落只增账簿，可按月校验金额守恒（应发 = 实发净额 + 待补发）。
 */
export class CommissionEngine {
  private readonly graph = new AttributionGraph();
  private readonly caps = new Map<AgentId, number>();
  private readonly defaultCap: number;
  private readonly transactions = new Map<TxId, TransactionRecord>();
  private readonly ledger: LedgerEntry[] = [];
  private pending: PendingEntry[] = [];
  /** `${agentId} ${month}` -> 当前计入封顶的实发净额 */
  private readonly issuedNet = new Map<string, number>();
  private ledgerSeq = 0;
  private txSeq = 0;

  constructor(options: { defaultMonthlyCap?: number } = {}) {
    this.defaultCap = options.defaultMonthlyCap ?? Number.POSITIVE_INFINITY;
  }

  registerAgent(id: AgentId, options: { monthlyCap?: number } = {}): void {
    if (this.caps.has(id)) throw new Error(`agent already registered: ${id}`);
    const cap = options.monthlyCap ?? this.defaultCap;
    if (cap !== Number.POSITIVE_INFINITY) {
      CommissionEngine.assertNonNegativeInt(cap, 'monthlyCap');
    }
    this.caps.set(id, cap);
  }

  /** 添加归属边：to 是 from 的直接上级，按 rateBps（万分比）计提 */
  addAttributionEdge(from: AgentId, to: AgentId, rateBps: number): void {
    this.requireAgent(from);
    this.requireAgent(to);
    this.graph.addEdge(from, to, rateBps);
  }

  submitTransaction(input: TransactionInput): LedgerEntry[] {
    this.requireAgent(input.agentId);
    if (this.transactions.has(input.id)) {
      throw new Error(`duplicate transaction id: ${input.id}`);
    }
    CommissionEngine.assertNonNegativeInt(input.amount, 'amount');
    if (!MONTH_PATTERN.test(input.month)) {
      throw new Error(`invalid month "${input.month}", expected YYYY-MM`);
    }

    const dues = this.computeDues(input.agentId, input.amount);
    const tx: TransactionRecord = {
      id: input.id,
      agentId: input.agentId,
      amount: input.amount,
      month: input.month,
      seq: ++this.txSeq,
      status: 'ACTIVE',
      dues,
    };
    this.transactions.set(tx.id, tx);

    const before = this.ledger.length;
    for (const due of dues) {
      const paid = this.payUpToCap(due.recipient, tx.month, due.amount, {
        kind: 'PAYOUT',
        txId: tx.id,
        chain: due.chain,
      });
      due.issued += paid;
      const truncated = due.amount - paid;
      if (truncated > 0) {
        this.pending.push({
          txId: tx.id,
          txSeq: tx.seq,
          recipient: due.recipient,
          month: tx.month,
          remaining: truncated,
          chain: due.chain,
        });
      }
    }
    return this.ledger.slice(before);
  }

  /** 撤单更正：冲回该单全部已发金额、丢弃其待补发部分，并按原交易顺序补发队列 */
  reverseTransaction(id: TxId, note = 'reversal'): LedgerEntry[] {
    const tx = this.transactions.get(id);
    if (!tx) throw new Error(`unknown transaction: ${id}`);
    if (tx.status === 'REVERSED') {
      throw new Error(`transaction already reversed: ${id}`);
    }
    tx.status = 'REVERSED';

    const before = this.ledger.length;
    for (const due of tx.dues) {
      if (due.issued > 0) {
        this.appendLedger('REVERSAL', tx.id, due.recipient, tx.month, -due.issued, due.chain, note);
        this.addIssuedNet(due.recipient, tx.month, -due.issued);
        due.issued = 0;
      }
    }
    this.pending = this.pending.filter((p) => p.txId !== tx.id);
    this.releasePending(tx.month);
    return this.ledger.slice(before);
  }

  /** 某笔交易的完整来源链：所有候选链、去重结果与应发金额 */
  explainTransaction(id: TxId): ChainExplanation[] {
    const tx = this.transactions.get(id);
    if (!tx) throw new Error(`unknown transaction: ${id}`);
    const raw = this.graph.enumerateRawChains(tx.agentId);
    const best = new Map<AgentId, SourceChain>();
    for (const due of tx.dues) best.set(due.recipient, due.chain);
    return raw.map((chain) => {
      const view = CommissionEngine.toSourceChain(chain);
      const selected = best.get(view.recipient) === undefined
        ? false
        : CommissionEngine.sameChain(best.get(view.recipient)!, view);
      return {
        chain: view,
        dueAmount: AttributionGraph.amountFor(tx.amount, chain),
        selected,
      };
    });
  }

  getLedger(): readonly LedgerEntry[] {
    return this.ledger;
  }

  getPending(month?: Month): readonly PendingEntry[] {
    return this.pending
      .filter((p) => month === undefined || p.month === month)
      .sort((a, b) => a.txSeq - b.txSeq);
  }

  /** 逐月金额守恒报告：对每个接收人校验 应发 = 实发净额 + 待补发 */
  monthlyReport(month: Month): MonthReport {
    const recipients = new Set<AgentId>();
    for (const tx of this.transactions.values()) {
      if (tx.month === month) for (const d of tx.dues) recipients.add(d.recipient);
    }
    for (const key of this.issuedNet.keys()) {
      if (key.endsWith(` ${month}`)) recipients.add(key.slice(0, -(month.length + 1)));
    }
    for (const p of this.pending) {
      if (p.month === month) recipients.add(p.recipient);
    }

    const rows: RecipientMonthReport[] = [];
    for (const recipient of [...recipients].sort()) {
      let due = 0;
      let reversed = 0;
      for (const tx of this.transactions.values()) {
        if (tx.month !== month) continue;
        for (const d of tx.dues) {
          if (d.recipient !== recipient) continue;
          if (tx.status === 'ACTIVE') due += d.amount;
        }
      }
      for (const entry of this.ledger) {
        if (entry.month === month && entry.recipient === recipient && entry.kind === 'REVERSAL') {
          reversed += -entry.amount;
        }
      }
      const issued = this.issuedNet.get(CommissionEngine.capKey(recipient, month)) ?? 0;
      const pending = this.pending
        .filter((p) => p.month === month && p.recipient === recipient)
        .reduce((sum, p) => sum + p.remaining, 0);
      rows.push({
        recipient,
        due,
        issued,
        pending,
        reversed,
        balanced: due === issued + pending,
      });
    }

    const totals = rows.reduce(
      (acc, r) => ({
        due: acc.due + r.due,
        issued: acc.issued + r.issued,
        pending: acc.pending + r.pending,
        reversed: acc.reversed + r.reversed,
      }),
      { due: 0, issued: 0, pending: 0, reversed: 0 },
    );
    return {
      month,
      recipients: rows,
      totals,
      balanced: rows.every((r) => r.balanced) && totals.due === totals.issued + totals.pending,
    };
  }

  /** 全部月份的守恒校验 */
  verifyConservation(): boolean {
    const months = new Set<Month>();
    for (const tx of this.transactions.values()) months.add(tx.month);
    for (const p of this.pending) months.add(p.month);
    for (const e of this.ledger) months.add(e.month);
    return [...months].every((m) => this.monthlyReport(m).balanced);
  }

  // ---- 内部实现 ----

  private computeDues(agentId: AgentId, amount: number): DueRecord[] {
    const bestByRecipient = new Map<AgentId, RawChain>();
    for (const chain of this.graph.enumerateRawChains(agentId)) {
      const current = bestByRecipient.get(chain.recipient);
      if (current === undefined) {
        bestByRecipient.set(chain.recipient, chain);
        continue;
      }
      const cmp = AttributionGraph.compareRates(chain, current);
      if (cmp > 0 || (cmp === 0 && CommissionEngine.chainKey(chain) < CommissionEngine.chainKey(current))) {
        bestByRecipient.set(chain.recipient, chain);
      }
    }
    return [...bestByRecipient.values()]
      .map((chain) => ({
        recipient: chain.recipient,
        amount: AttributionGraph.amountFor(amount, chain),
        chain: CommissionEngine.toSourceChain(chain),
        issued: 0,
      }))
      .filter((due) => due.amount > 0)
      .sort((a, b) => (a.recipient < b.recipient ? -1 : 1));
  }

  /** 在封顶额度内尽量支付，返回实际支付额 */
  private payUpToCap(
    recipient: AgentId,
    month: Month,
    want: number,
    ref: { kind: 'PAYOUT' | 'REISSUE'; txId: TxId; chain: SourceChain },
  ): number {
    const room = this.capFor(recipient) - this.issuedOf(recipient, month);
    const pay = Math.min(want, Math.max(room, 0));
    if (pay > 0) {
      this.appendLedger(ref.kind, ref.txId, recipient, month, pay, ref.chain);
      this.addIssuedNet(recipient, month, pay);
    }
    return pay;
  }

  /** 封顶释放后，按原始交易顺序（txSeq 升序）补发被截部分 */
  private releasePending(month: Month): void {
    const queue = this.pending
      .filter((p) => p.month === month)
      .sort((a, b) => a.txSeq - b.txSeq);
    for (const entry of queue) {
      if (entry.remaining <= 0) continue;
      const paid = this.payUpToCap(entry.recipient, month, entry.remaining, {
        kind: 'REISSUE',
        txId: entry.txId,
        chain: entry.chain,
      });
      if (paid > 0) {
        entry.remaining -= paid;
        const tx = this.transactions.get(entry.txId)!;
        const due = tx.dues.find((d) => d.recipient === entry.recipient);
        if (due) due.issued += paid;
      }
    }
    this.pending = this.pending.filter((p) => p.remaining > 0);
  }

  private capFor(agentId: AgentId): number {
    return this.caps.get(agentId) ?? this.defaultCap;
  }

  private issuedOf(agentId: AgentId, month: Month): number {
    return this.issuedNet.get(CommissionEngine.capKey(agentId, month)) ?? 0;
  }

  private addIssuedNet(agentId: AgentId, month: Month, delta: number): void {
    const key = CommissionEngine.capKey(agentId, month);
    this.issuedNet.set(key, (this.issuedNet.get(key) ?? 0) + delta);
  }

  private appendLedger(
    kind: LedgerEntry['kind'],
    txId: TxId,
    recipient: AgentId,
    month: Month,
    amount: number,
    chain: SourceChain,
    note?: string,
  ): void {
    this.ledger.push({ seq: ++this.ledgerSeq, kind, txId, recipient, month, amount, chain, note });
  }

  private requireAgent(id: AgentId): void {
    if (!this.caps.has(id)) throw new Error(`unknown agent: ${id}`);
  }

  private static capKey(agentId: AgentId, month: Month): string {
    return `${agentId} ${month}`;
  }

  private static toSourceChain(chain: RawChain): SourceChain {
    return {
      recipient: chain.recipient,
      steps: chain.steps,
      effectiveRateBps: AttributionGraph.effectiveRateBps(chain),
    };
  }

  private static chainKey(chain: { steps: SourceChain['steps'] }): string {
    return chain.steps.map((s) => `${s.from}>${s.to}`).join('|');
  }

  private static sameChain(a: SourceChain, b: SourceChain): boolean {
    return CommissionEngine.chainKey(a) === CommissionEngine.chainKey(b);
  }

  private static assertNonNegativeInt(value: number, name: string): void {
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new Error(`${name} must be a non-negative safe integer, got ${value}`);
    }
  }
}
