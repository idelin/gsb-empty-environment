// 边缘节点：本地原子状态决定消费/归还竞争。
// - spend 按 requestId 幂等；同 ID 不同 cost => conflict，不改状态。
// - returnGrant 把授予原子地冻结为 returned，此后 spend 不再使用它。
// - 过期（expiresAtTick < tick）与迟到授予永不可用于消费。
import { Ledger } from './ledger.ts';
import type { Alloc, Grant, Msg, RejectReason, ReturnConfirmMsg, ReturnMsg, SpendResult } from './types.ts';

export type NodeRec =
  | { t: 'grant'; grant: Grant; recvTick: number }
  | { t: 'spend-accepted'; requestId: string; cost: number; windowId: number; allocs: Alloc[]; tick: number }
  | { t: 'spend-rejected'; requestId: string; cost: number; reason: RejectReason; tick: number }
  | { t: 'spend-conflict'; requestId: string; cost: number; originalCost: number; tick: number }
  | { t: 'return'; returnId: string; grantId: string; unspent: number; tick: number }
  | { t: 'return-confirm'; returnId: string; grantId: string; unspent: number; tick: number };

interface GrantState {
  grant: Grant;
  spent: number;
  returned: boolean;
  returnId: string | null;
  returnedUnspent: number;
  confirmed: boolean;
}

interface RequestRecord {
  cost: number;
  accepted: boolean;
  reason: RejectReason | null;
  allocs: Alloc[];
  windowId: number;
}

export class EdgeNode {
  readonly id: string;
  readonly windowLength: number;
  private ledger: Ledger<NodeRec>;
  private grants = new Map<string, GrantState>();
  private requests = new Map<string, RequestRecord>();
  private outbox: Msg[] = [];

  constructor(id: string, windowLength: number, ledger: Ledger<NodeRec>) {
    this.id = id;
    this.windowLength = windowLength;
    this.ledger = ledger;
    for (const rec of ledger.all()) this.apply(rec);
  }

  windowOf(tick: number): number {
    return Math.floor(tick / this.windowLength);
  }

  onGrant(grant: Grant, tick: number): void {
    if (this.grants.has(grant.grantId)) return; // 重复投递幂等
    if (grant.nodeId !== this.id) return;
    const rec: NodeRec = { t: 'grant', grant, recvTick: tick };
    this.ledger.append(rec);
    this.apply(rec);
  }

  spend(requestId: string, cost: number, tick: number): SpendResult {
    const prev = this.requests.get(requestId);
    if (prev) {
      if (prev.cost !== cost) {
        this.ledger.append({ t: 'spend-conflict', requestId, cost, originalCost: prev.cost, tick });
        return { status: 'conflict', requestId, cost, originalCost: prev.cost };
      }
      return prev.accepted
        ? { status: 'accepted', requestId, cost, windowId: prev.windowId, allocs: prev.allocs, dedup: true }
        : { status: 'rejected', requestId, cost, reason: prev.reason as RejectReason, dedup: true };
    }

    let reason: RejectReason | null = null;
    let allocs: Alloc[] = [];
    if (!Number.isInteger(cost) || cost <= 0) {
      reason = 'non-positive-cost';
    } else {
      const spendable = [...this.grants.values()]
        .filter((g) => !g.returned && g.grant.expiresAtTick >= tick && g.spent < g.grant.amount)
        .sort((a, b) => a.grant.expiresAtTick - b.grant.expiresAtTick || a.grant.grantId.localeCompare(b.grant.grantId));
      if (spendable.length === 0) {
        reason = 'no-active-grants';
      } else {
        const avail = spendable.reduce((s, g) => s + (g.grant.amount - g.spent), 0);
        if (avail < cost) {
          reason = 'insufficient-quota';
        } else {
          let remaining = cost;
          for (const g of spendable) {
            if (remaining === 0) break;
            const take = Math.min(g.grant.amount - g.spent, remaining);
            if (take > 0) {
              allocs.push({ grantId: g.grant.grantId, amount: take });
              remaining -= take;
            }
          }
        }
      }
    }

    if (reason !== null) {
      const rec: NodeRec = { t: 'spend-rejected', requestId, cost, reason, tick };
      this.ledger.append(rec);
      this.apply(rec);
      return { status: 'rejected', requestId, cost, reason, dedup: false };
    }
    const rec: NodeRec = { t: 'spend-accepted', requestId, cost, windowId: this.windowOf(tick), allocs, tick };
    this.ledger.append(rec);
    this.apply(rec);
    return { status: 'accepted', requestId, cost, windowId: rec.windowId, allocs, dedup: false };
  }

  // 原子冻结：进入 returned 状态后，spend 立即不再看到该授予。
  returnGrant(grantId: string, tick: number): ReturnMsg | null {
    const g = this.grants.get(grantId);
    if (!g) return null;
    if (g.returned) {
      if (!g.confirmed && g.returnId !== null) {
        const resend: ReturnMsg = { kind: 'return', returnId: g.returnId, grantId, nodeId: this.id, unspent: g.returnedUnspent, sentTick: tick };
        this.outbox.push(resend);
        return resend;
      }
      return null;
    }
    const returnId = `R-${this.id}-${grantId}`;
    const unspent = g.grant.amount - g.spent;
    const rec: NodeRec = { t: 'return', returnId, grantId, unspent, tick };
    this.ledger.append(rec);
    this.apply(rec);
    const msg: ReturnMsg = { kind: 'return', returnId, grantId, nodeId: this.id, unspent, sentTick: tick };
    this.outbox.push(msg);
    return msg;
  }

  returnAll(tick: number): ReturnMsg[] {
    const msgs: ReturnMsg[] = [];
    for (const grantId of [...this.grants.keys()].sort()) {
      const msg = this.returnGrant(grantId, tick);
      if (msg) msgs.push(msg);
    }
    return msgs;
  }

  onReturnConfirm(msg: ReturnConfirmMsg, tick: number): void {
    const g = this.grants.get(msg.grantId);
    if (!g || !g.returned || g.confirmed) return;
    if (g.returnId !== msg.returnId || g.returnedUnspent !== msg.unspent) return;
    const rec: NodeRec = { t: 'return-confirm', returnId: msg.returnId, grantId: msg.grantId, unspent: msg.unspent, tick };
    this.ledger.append(rec);
    this.apply(rec);
  }

  grantState(grantId: string): { spent: number; returned: boolean; confirmed: boolean; unspent: number } | null {
    const g = this.grants.get(grantId);
    if (!g) return null;
    return { spent: g.spent, returned: g.returned, confirmed: g.confirmed, unspent: g.grant.amount - g.spent };
  }

  private apply(rec: NodeRec): void {
    switch (rec.t) {
      case 'grant':
        this.grants.set(rec.grant.grantId, { grant: rec.grant, spent: 0, returned: false, returnId: null, returnedUnspent: 0, confirmed: false });
        break;
      case 'spend-accepted': {
        this.requests.set(rec.requestId, { cost: rec.cost, accepted: true, reason: null, allocs: rec.allocs, windowId: rec.windowId });
        for (const a of rec.allocs) {
          const g = this.grants.get(a.grantId);
          if (g) g.spent += a.amount;
        }
        break;
      }
      case 'spend-rejected':
        this.requests.set(rec.requestId, { cost: rec.cost, accepted: false, reason: rec.reason, allocs: [], windowId: -1 });
        break;
      case 'return': {
        const g = this.grants.get(rec.grantId);
        if (!g) break;
        g.returned = true;
        g.returnId = rec.returnId;
        g.returnedUnspent = rec.unspent;
        break;
      }
      case 'return-confirm': {
        const g = this.grants.get(rec.grantId);
        if (!g) break;
        g.confirmed = true;
        break;
      }
      case 'spend-conflict':
        break;
    }
  }

  drainOutbox(): Msg[] {
    const out = this.outbox;
    this.outbox = [];
    return out;
  }
}
