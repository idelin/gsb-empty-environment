import type { Allocation, Grant, Message, NodeId, Tick } from "./types.ts";
import { Ledger, type LedgerEvent } from "./ledger.ts";

interface LocalGrant {
  grant: Grant;
  remaining: number;
  expired: boolean;
  returned: boolean;
  acked: boolean;
}

interface RequestRecord {
  requestId: string;
  cost: number;
  tick: Tick;
  status: "accepted" | "rejected";
  allocations: Allocation[];
  reason?: string;
}

export type ConsumeResult =
  | { status: "accepted"; requestId: string; cost: number; allocations: Allocation[]; replay: boolean }
  | { status: "rejected"; requestId: string; cost: number; reason: string; replay: boolean }
  | { status: "conflict"; requestId: string; cost: number; existingCost: number };

export interface StepOutput {
  outbox: Array<{ to: "center"; msg: Message }>;
}

const RETRANSMIT_INTERVAL = 5;

/**
 * Edge node. All quota mutations happen synchronously against local
 * state plus an append to the local ledger, so a consume racing a
 * return is decided atomically by call order at this node: whichever
 * operation runs first observes and mutates `remaining`, and the
 * other sees the result. Expired budget (expiryTick < tick) is never
 * spendable, and a grant arriving after its expiry is returned in
 * full instead of reviving the budget.
 */
export class EdgeNode {
  readonly nodeId: NodeId;
  readonly windowLength: number;
  private ledger: Ledger;
  private grants = new Map<string, LocalGrant>();
  private requests = new Map<string, RequestRecord>();
  private pendingReturns = new Map<string, { grantId: string; amount: number; lastSentTick: number }>();
  private pendingBorrows = new Map<string, { amount: number; lastSentTick: number }>();
  private returnSeq = 0;

  constructor(opts: { nodeId: NodeId; windowLength: number; ledger: Ledger }) {
    this.nodeId = opts.nodeId;
    this.windowLength = opts.windowLength;
    this.ledger = opts.ledger;
  }

  static restore(path: string, opts: { nodeId: NodeId; windowLength: number }): EdgeNode {
    const node = new EdgeNode({ ...opts, ledger: new Ledger(path) });
    for (const event of Ledger.read(path)) node.apply(event);
    return node;
  }

  available(tick: Tick): number {
    let total = 0;
    for (const local of this.grants.values()) {
      if (!local.expired && !local.returned && local.grant.expiryTick >= tick) {
        total += local.remaining;
      }
    }
    return total;
  }

  hasPendingBorrow(): boolean {
    return this.pendingBorrows.size > 0;
  }

  borrow(requestId: string, amount: number, tick: Tick): StepOutput {
    if (!this.pendingBorrows.has(requestId)) {
      this.pendingBorrows.set(requestId, { amount, lastSentTick: tick });
      this.ledger.append({ ev: "borrow_sent", requestId, amount, tick });
    }
    return {
      outbox: [{ to: "center", msg: { kind: "BorrowRequest", requestId, nodeId: this.nodeId, amount } }],
    };
  }

  consume(requestId: string, cost: number, tick: Tick): { result: ConsumeResult } & StepOutput {
    const existing = this.requests.get(requestId);
    if (existing) {
      if (existing.cost !== cost) {
        this.ledger.append({
          ev: "consume_conflict",
          requestId,
          cost,
          existingCost: existing.cost,
          tick,
        });
        return { result: { status: "conflict", requestId, cost, existingCost: existing.cost }, outbox: [] };
      }
      if (existing.status === "accepted") {
        return {
          result: { status: "accepted", requestId, cost, allocations: existing.allocations, replay: true },
          outbox: [],
        };
      }
      return {
        result: { status: "rejected", requestId, cost, reason: existing.reason ?? "insufficient_quota", replay: true },
        outbox: [],
      };
    }

    const candidates = [...this.grants.values()]
      .filter((g) => !g.expired && !g.returned && g.remaining > 0 && g.grant.expiryTick >= tick)
      .sort((a, b) => (a.grant.grantId < b.grant.grantId ? -1 : 1));
    const available = candidates.reduce((sum, g) => sum + g.remaining, 0);

    if (available < cost) {
      this.requests.set(requestId, {
        requestId,
        cost,
        tick,
        status: "rejected",
        allocations: [],
        reason: "insufficient_quota",
      });
      this.ledger.append({ ev: "consume_rejected", requestId, cost, reason: "insufficient_quota", tick });
      return {
        result: { status: "rejected", requestId, cost, reason: "insufficient_quota", replay: false },
        outbox: [],
      };
    }

    const allocations: Allocation[] = [];
    let left = cost;
    for (const local of candidates) {
      if (left === 0) break;
      const take = Math.min(local.remaining, left);
      local.remaining -= take;
      left -= take;
      allocations.push({ grantId: local.grant.grantId, amount: take });
    }
    this.requests.set(requestId, { requestId, cost, tick, status: "accepted", allocations });
    this.ledger.append({ ev: "consume_accepted", requestId, cost, allocations, tick });
    return {
      result: { status: "accepted", requestId, cost, allocations, replay: false },
      outbox: [
        {
          to: "center",
          msg: { kind: "ConsumeReport", nodeId: this.nodeId, requestId, cost, tick, allocations },
        },
      ],
    };
  }

  /** Expire grants whose expiryTick < tick and return their unconsumed remainder. */
  sweep(tick: Tick): StepOutput {
    const outbox: StepOutput["outbox"] = [];
    for (const local of this.grants.values()) {
      if (local.expired || local.grant.expiryTick >= tick) continue;
      local.expired = true;
      this.ledger.append({
        ev: "grant_expired",
        grantId: local.grant.grantId,
        remaining: local.remaining,
        tick,
      });
      if (local.remaining > 0 && !local.returned) {
        local.returned = true;
        outbox.push({ to: "center", msg: this.makeReturn(local.grant.grantId, local.remaining, tick) });
      }
    }
    return { outbox };
  }

  /** Retransmit unacked returns and unanswered borrows; loss is expected. */
  retransmit(tick: Tick): StepOutput {
    const outbox: StepOutput["outbox"] = [];
    for (const [returnId, pending] of this.pendingReturns) {
      if (tick - pending.lastSentTick >= RETRANSMIT_INTERVAL) {
        pending.lastSentTick = tick;
        outbox.push({
          to: "center",
          msg: { kind: "ReturnReport", returnId, nodeId: this.nodeId, grantId: pending.grantId, amount: pending.amount },
        });
      }
    }
    for (const [requestId, pending] of this.pendingBorrows) {
      if (tick - pending.lastSentTick >= RETRANSMIT_INTERVAL) {
        pending.lastSentTick = tick;
        outbox.push({
          to: "center",
          msg: { kind: "BorrowRequest", requestId, nodeId: this.nodeId, amount: pending.amount },
        });
      }
    }
    return { outbox };
  }

  receive(msg: Message, tick: Tick): StepOutput {
    switch (msg.kind) {
      case "GrantMsg":
        return this.onGrant(msg.grant, msg.borrowRequestId, tick);
      case "BorrowReject":
        this.pendingBorrows.delete(msg.requestId);
        this.ledger.append({ ev: "borrow_rejected", requestId: msg.requestId, reason: msg.reason, tick });
        return { outbox: [] };
      case "ReturnAck": {
        const pending = this.pendingReturns.get(msg.returnId);
        if (pending) {
          this.pendingReturns.delete(msg.returnId);
          const local = this.grants.get(pending.grantId);
          if (local) local.acked = true;
        }
        this.ledger.append({ ev: "return_acked", returnId: msg.returnId, grantId: msg.grantId, tick });
        return { outbox: [] };
      }
      default:
        return { outbox: [] };
    }
  }

  private onGrant(grant: Grant, borrowRequestId: string, tick: Tick): StepOutput {
    this.pendingBorrows.delete(borrowRequestId);
    if (this.grants.has(grant.grantId)) {
      this.ledger.append({ ev: "grant_duplicate", grantId: grant.grantId, tick });
      return { outbox: [] };
    }
    if (grant.expiryTick < tick) {
      // Late grant: expired budget must not be revived; hand it straight back.
      this.ledger.append({ ev: "late_grant", grant, borrowRequestId, tick });
      this.grants.set(grant.grantId, {
        grant,
        remaining: 0,
        expired: true,
        returned: true,
        acked: false,
      });
      return { outbox: [{ to: "center", msg: this.makeReturn(grant.grantId, grant.amount, tick) }] };
    }
    this.grants.set(grant.grantId, {
      grant,
      remaining: grant.amount,
      expired: false,
      returned: false,
      acked: false,
    });
    this.ledger.append({ ev: "grant_received", grant, borrowRequestId, tick });
    return { outbox: [] };
  }

  private makeReturn(grantId: string, amount: number, tick: Tick): Message {
    const returnId = `${this.nodeId}-r${this.returnSeq++}`;
    this.pendingReturns.set(returnId, { grantId, amount, lastSentTick: tick });
    this.ledger.append({ ev: "return_sent", returnId, grantId, amount, tick });
    return { kind: "ReturnReport", returnId, nodeId: this.nodeId, grantId, amount };
  }

  private apply(event: LedgerEvent): void {
    switch (event.ev) {
      case "grant_received": {
        const grant = event.grant as Grant;
        this.grants.set(grant.grantId, {
          grant,
          remaining: grant.amount,
          expired: false,
          returned: false,
          acked: false,
        });
        break;
      }
      case "late_grant": {
        const grant = event.grant as Grant;
        this.grants.set(grant.grantId, {
          grant,
          remaining: 0,
          expired: true,
          returned: true,
          acked: false,
        });
        break;
      }
      case "consume_accepted": {
        const allocations = event.allocations as Allocation[];
        for (const alloc of allocations) {
          const local = this.grants.get(alloc.grantId);
          if (local) local.remaining -= alloc.amount;
        }
        this.requests.set(event.requestId as string, {
          requestId: event.requestId as string,
          cost: event.cost as number,
          tick: event.tick,
          status: "accepted",
          allocations,
        });
        break;
      }
      case "consume_rejected":
        this.requests.set(event.requestId as string, {
          requestId: event.requestId as string,
          cost: event.cost as number,
          tick: event.tick,
          status: "rejected",
          allocations: [],
          reason: event.reason as string,
        });
        break;
      case "grant_expired": {
        const local = this.grants.get(event.grantId as string);
        if (local) local.expired = true;
        break;
      }
      case "return_sent": {
        const returnId = event.returnId as string;
        const grantId = event.grantId as string;
        this.pendingReturns.set(returnId, {
          grantId,
          amount: event.amount as number,
          lastSentTick: event.tick,
        });
        const local = this.grants.get(grantId);
        if (local) local.returned = true;
        const seq = Number(returnId.split("-r")[1]) + 1;
        if (Number.isFinite(seq)) this.returnSeq = Math.max(this.returnSeq, seq);
        break;
      }
      case "return_acked": {
        const pending = this.pendingReturns.get(event.returnId as string);
        if (pending) {
          this.pendingReturns.delete(event.returnId as string);
          const local = this.grants.get(pending.grantId);
          if (local) local.acked = true;
        }
        break;
      }
      case "borrow_sent":
        this.pendingBorrows.set(event.requestId as string, {
          amount: event.amount as number,
          lastSentTick: event.tick,
        });
        break;
      case "borrow_rejected":
        this.pendingBorrows.delete(event.requestId as string);
        break;
    }
  }
}
