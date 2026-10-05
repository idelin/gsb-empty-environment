// 中心：按固定窗口签发授予。不变式：任一窗口 w 恒有
//   grossIssued(w) - confirmedReturned(w) <= budget(w)
// 未消费额度只有在中心确认归还后才回到可用池；超时绝不触发回收。
import { Ledger } from './ledger.ts';
import type { Grant, Msg, ReturnMsg } from './types.ts';

export type CenterRec =
  | { t: 'issue'; grant: Grant }
  | { t: 'issue-rejected'; nodeId: string; amount: number; reason: string; tick: number }
  | { t: 'return'; returnId: string; grantId: string; nodeId: string; unspent: number; tick: number }
  | { t: 'return-dup'; returnId: string; grantId: string; nodeId: string; tick: number }
  | { t: 'return-rejected'; returnId: string; grantId: string; nodeId: string; reason: string; tick: number };

interface GrantBook {
  grant: Grant;
  returned: boolean;
  returnedUnspent: number;
}

export class Center {
  readonly windowLength: number;
  readonly budgetPerWindow: number;
  private ledger: Ledger<CenterRec>;
  private grants = new Map<string, GrantBook>();
  private grossIssued = new Map<number, number>();
  private confirmedReturned = new Map<number, number>();
  private seq = 0;
  private outbox: Msg[] = [];

  constructor(windowLength: number, budgetPerWindow: number, ledger: Ledger<CenterRec>) {
    this.windowLength = windowLength;
    this.budgetPerWindow = budgetPerWindow;
    this.ledger = ledger;
    for (const rec of ledger.all()) this.apply(rec);
  }

  windowOf(tick: number): number {
    return Math.floor(tick / this.windowLength);
  }

  windowEnd(windowId: number): number {
    return (windowId + 1) * this.windowLength - 1;
  }

  available(windowId: number): number {
    return (
      this.budgetPerWindow -
      (this.grossIssued.get(windowId) ?? 0) +
      (this.confirmedReturned.get(windowId) ?? 0)
    );
  }

  issue(nodeId: string, amount: number, tick: number): { ok: boolean; grant?: Grant; reason?: string } {
    const windowId = this.windowOf(tick);
    if (!Number.isInteger(amount) || amount <= 0) {
      this.ledger.append({ t: 'issue-rejected', nodeId, amount, reason: 'non-positive-amount', tick });
      return { ok: false, reason: 'non-positive-amount' };
    }
    if (amount > this.available(windowId)) {
      this.ledger.append({ t: 'issue-rejected', nodeId, amount, reason: 'window-budget-exceeded', tick });
      return { ok: false, reason: 'window-budget-exceeded' };
    }
    const grant: Grant = {
      grantId: `G${this.seq + 1}`,
      windowId,
      nodeId,
      amount,
      issuedTick: tick,
      expiresAtTick: this.windowEnd(windowId),
    };
    const rec: CenterRec = { t: 'issue', grant };
    this.ledger.append(rec);
    this.apply(rec);
    this.outbox.push({ kind: 'grant', grant });
    return { ok: true, grant };
  }

  onReturn(msg: ReturnMsg, tick: number): void {
    const book = this.grants.get(msg.grantId);
    if (!book || book.grant.nodeId !== msg.nodeId) {
      this.ledger.append({ t: 'return-rejected', returnId: msg.returnId, grantId: msg.grantId, nodeId: msg.nodeId, reason: 'unknown-grant', tick });
      return;
    }
    if (book.returned) {
      // 重复归还：幂等。按首次确认的金额重新确认，不重复计入可用池。
      this.ledger.append({ t: 'return-dup', returnId: msg.returnId, grantId: msg.grantId, nodeId: msg.nodeId, tick });
      this.outbox.push({ kind: 'return-confirm', returnId: msg.returnId, grantId: msg.grantId, nodeId: msg.nodeId, unspent: book.returnedUnspent });
      return;
    }
    if (!Number.isInteger(msg.unspent) || msg.unspent < 0 || msg.unspent > book.grant.amount) {
      this.ledger.append({ t: 'return-rejected', returnId: msg.returnId, grantId: msg.grantId, nodeId: msg.nodeId, reason: 'invalid-amount', tick });
      return;
    }
    const rec: CenterRec = { t: 'return', returnId: msg.returnId, grantId: msg.grantId, nodeId: msg.nodeId, unspent: msg.unspent, tick };
    this.ledger.append(rec);
    this.apply(rec);
    this.outbox.push({ kind: 'return-confirm', returnId: msg.returnId, grantId: msg.grantId, nodeId: msg.nodeId, unspent: msg.unspent });
  }

  private apply(rec: CenterRec): void {
    switch (rec.t) {
      case 'issue': {
        this.seq += 1;
        this.grants.set(rec.grant.grantId, { grant: rec.grant, returned: false, returnedUnspent: 0 });
        const w = rec.grant.windowId;
        this.grossIssued.set(w, (this.grossIssued.get(w) ?? 0) + rec.grant.amount);
        break;
      }
      case 'return': {
        const book = this.grants.get(rec.grantId);
        if (!book) break;
        book.returned = true;
        book.returnedUnspent = rec.unspent;
        const w = book.grant.windowId;
        this.confirmedReturned.set(w, (this.confirmedReturned.get(w) ?? 0) + rec.unspent);
        break;
      }
      default:
        break;
    }
  }

  drainOutbox(): Msg[] {
    const out = this.outbox;
    this.outbox = [];
    return out;
  }
}
