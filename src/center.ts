import type { Grant, Message, NodeId, Tick, WindowId } from "./types.ts";
import { Ledger, type LedgerEvent } from "./ledger.ts";

interface GrantBook {
  grant: Grant;
  unreturned: number;
}

type BorrowOutcome =
  | { kind: "granted"; grantId: string }
  | { kind: "rejected"; reason: string };

/**
 * Quota issuer. Safety invariant: for every window w,
 *   spent(w) = issued(w) - confirmedReturned(w) <= windowQuota
 * at all times. Quota is freed ONLY by a confirmed ReturnReport;
 * the center never reclaims quota on timeout, because a missing
 * report does not imply the quota was unused.
 */
export class Center {
  readonly windowQuota: number;
  readonly windowLength: number;
  private ledger: Ledger;
  private grantSeq = 0;
  private grants = new Map<string, GrantBook>();
  private spentByWindow = new Map<WindowId, number>();
  private borrowIndex = new Map<string, BorrowOutcome>();
  private returns = new Map<string, number>();
  private consumedByWindow = new Map<WindowId, number>();
  private seenConsumes = new Set<string>();

  constructor(opts: { windowQuota: number; windowLength: number; ledger: Ledger }) {
    this.windowQuota = opts.windowQuota;
    this.windowLength = opts.windowLength;
    this.ledger = opts.ledger;
  }

  static restore(path: string, opts: { windowQuota: number; windowLength: number }): Center {
    const center = new Center({ ...opts, ledger: new Ledger(path) });
    for (const event of Ledger.read(path)) center.apply(event);
    return center;
  }

  windowOf(tick: Tick): WindowId {
    return Math.floor(tick / this.windowLength);
  }

  spent(windowId: WindowId): number {
    return this.spentByWindow.get(windowId) ?? 0;
  }

  consumed(windowId: WindowId): number {
    return this.consumedByWindow.get(windowId) ?? 0;
  }

  receive(msg: Message, tick: Tick): Message[] {
    switch (msg.kind) {
      case "BorrowRequest":
        return [this.handleBorrow(msg, tick)];
      case "ReturnReport":
        return [this.handleReturn(msg, tick)];
      case "ConsumeReport":
        this.handleConsume(msg, tick);
        return [];
      default:
        return [];
    }
  }

  private handleBorrow(
    msg: { requestId: string; nodeId: NodeId; amount: number },
    tick: Tick,
  ): Message {
    const existing = this.borrowIndex.get(msg.requestId);
    if (existing) {
      this.ledger.append({
        ev: "borrow_request_duplicate",
        requestId: msg.requestId,
        nodeId: msg.nodeId,
        tick,
      });
      if (existing.kind === "granted") {
        const book = this.grants.get(existing.grantId);
        if (book) return { kind: "GrantMsg", grant: book.grant, borrowRequestId: msg.requestId };
      }
      if (existing.kind === "rejected") {
        return { kind: "BorrowReject", requestId: msg.requestId, nodeId: msg.nodeId, reason: existing.reason };
      }
    }

    this.ledger.append({
      ev: "borrow_request",
      requestId: msg.requestId,
      nodeId: msg.nodeId,
      amount: msg.amount,
      tick,
    });

    const windowId = this.windowOf(tick);
    if (this.spent(windowId) + msg.amount > this.windowQuota) {
      this.borrowIndex.set(msg.requestId, { kind: "rejected", reason: "window_quota_exhausted" });
      this.ledger.append({
        ev: "borrow_rejected",
        requestId: msg.requestId,
        nodeId: msg.nodeId,
        reason: "window_quota_exhausted",
        tick,
      });
      return {
        kind: "BorrowReject",
        requestId: msg.requestId,
        nodeId: msg.nodeId,
        reason: "window_quota_exhausted",
      };
    }

    const grant: Grant = {
      grantId: `g${this.grantSeq++}`,
      windowId,
      nodeId: msg.nodeId,
      amount: msg.amount,
      issuedAtTick: tick,
      expiryTick: (windowId + 1) * this.windowLength - 1,
    };
    this.grants.set(grant.grantId, { grant, unreturned: grant.amount });
    this.spentByWindow.set(windowId, this.spent(windowId) + grant.amount);
    this.borrowIndex.set(msg.requestId, { kind: "granted", grantId: grant.grantId });
    this.ledger.append({ ev: "grant_issued", grant, borrowRequestId: msg.requestId, tick });
    return { kind: "GrantMsg", grant, borrowRequestId: msg.requestId };
  }

  private handleReturn(
    msg: { returnId: string; nodeId: NodeId; grantId: string; amount: number },
    tick: Tick,
  ): Message {
    const previous = this.returns.get(msg.returnId);
    if (previous !== undefined) {
      this.ledger.append({
        ev: "return_received",
        returnId: msg.returnId,
        nodeId: msg.nodeId,
        grantId: msg.grantId,
        amount: msg.amount,
        duplicate: true,
        tick,
      });
      return {
        kind: "ReturnAck",
        returnId: msg.returnId,
        nodeId: msg.nodeId,
        grantId: msg.grantId,
        acceptedAmount: previous,
      };
    }

    const book = this.grants.get(msg.grantId);
    let accepted = 0;
    if (book && book.grant.nodeId === msg.nodeId) {
      accepted = Math.min(msg.amount, book.unreturned);
      book.unreturned -= accepted;
      const windowId = book.grant.windowId;
      this.spentByWindow.set(windowId, this.spent(windowId) - accepted);
    }
    this.returns.set(msg.returnId, accepted);
    this.ledger.append({
      ev: "return_received",
      returnId: msg.returnId,
      nodeId: msg.nodeId,
      grantId: msg.grantId,
      amount: msg.amount,
      duplicate: false,
      tick,
    });
    this.ledger.append({
      ev: "return_confirmed",
      returnId: msg.returnId,
      grantId: msg.grantId,
      acceptedAmount: accepted,
      tick,
    });
    return {
      kind: "ReturnAck",
      returnId: msg.returnId,
      nodeId: msg.nodeId,
      grantId: msg.grantId,
      acceptedAmount: accepted,
    };
  }

  private handleConsume(
    msg: { nodeId: NodeId; requestId: string; cost: number; tick: Tick },
    tick: Tick,
  ): void {
    const key = `${msg.nodeId}/${msg.requestId}`;
    const duplicate = this.seenConsumes.has(key);
    if (!duplicate) {
      this.seenConsumes.add(key);
      const windowId = this.windowOf(msg.tick);
      this.consumedByWindow.set(windowId, this.consumed(windowId) + msg.cost);
    }
    this.ledger.append({
      ev: "consume_reported",
      nodeId: msg.nodeId,
      requestId: msg.requestId,
      cost: msg.cost,
      consumeTick: msg.tick,
      duplicate,
      tick,
    });
  }

  private apply(event: LedgerEvent): void {
    switch (event.ev) {
      case "grant_issued": {
        const grant = event.grant as Grant;
        this.grants.set(grant.grantId, { grant, unreturned: grant.amount });
        this.spentByWindow.set(grant.windowId, this.spent(grant.windowId) + grant.amount);
        this.borrowIndex.set(event.borrowRequestId as string, {
          kind: "granted",
          grantId: grant.grantId,
        });
        const seq = Number(grant.grantId.slice(1)) + 1;
        if (Number.isFinite(seq)) this.grantSeq = Math.max(this.grantSeq, seq);
        break;
      }
      case "borrow_rejected":
        this.borrowIndex.set(event.requestId as string, {
          kind: "rejected",
          reason: event.reason as string,
        });
        break;
      case "return_confirmed": {
        const book = this.grants.get(event.grantId as string);
        const accepted = event.acceptedAmount as number;
        if (book) {
          book.unreturned -= accepted;
          this.spentByWindow.set(
            book.grant.windowId,
            this.spent(book.grant.windowId) - accepted,
          );
        }
        this.returns.set(event.returnId as string, accepted);
        break;
      }
      case "consume_reported": {
        const key = `${event.nodeId}/${event.requestId}`;
        if (!event.duplicate) {
          const windowId = this.windowOf(event.consumeTick as number);
          this.consumedByWindow.set(windowId, this.consumed(windowId) + (event.cost as number));
        }
        this.seenConsumes.add(key);
        break;
      }
    }
  }
}
