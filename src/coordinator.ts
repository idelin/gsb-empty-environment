import type {
  Decision,
  Message,
  TxnState,
  Vote,
  WalRecord,
} from "./types.ts";
import type { Transport, Endpoint } from "./transport.ts";
import type { Scheduler } from "./scheduler.ts";
import { Wal } from "./wal.ts";
import { AuditLog } from "./audit.ts";

export interface CoordinatorOptions {
  prepareTimeout?: number;
  retryInterval?: number;
  maxRetries?: number;
  onEvent?: (line: string) => void;
}

interface TxnContext {
  txnId: string;
  participants: string[];
  state: TxnState;
  votes: Map<string, Vote>;
  acks: Set<string>;
  decision: Decision | null;
  retries: Map<string, number>;
  audited: Set<string>;
}

/**
 * Recoverable two-phase commit coordinator.
 *
 * Every state transition is written to the WAL before any message is
 * sent, so a crash at any point is recoverable by replaying the log:
 *  - decision record present  -> re-enter COMMITTING/ABORTING, QUERY
 *    participants for their status and drive the txn to completion;
 *  - no decision record       -> presumptive abort (no participant can
 *    have committed, so aborting is always safe).
 * Participants that never respond after `maxRetries` are recorded as
 * PENDING_PARTICIPANT audit events; the txn stays pending (never
 * forgotten) so a later recovery pass can finish it — no orphans.
 */
export class Coordinator implements Endpoint {
  readonly id: string;
  private online = true;
  private wal: Wal<WalRecord>;
  private txns = new Map<string, TxnContext>();
  private readonly prepareTimeout: number;
  private readonly retryInterval: number;
  private readonly maxRetries: number;
  private readonly onEvent: (line: string) => void;
  private readonly transport: Transport;
  private readonly scheduler: Scheduler;
  private readonly audit: AuditLog;

  constructor(
    id: string,
    transport: Transport,
    scheduler: Scheduler,
    walPath: string,
    audit: AuditLog,
    opts: CoordinatorOptions = {},
  ) {
    this.id = id;
    this.transport = transport;
    this.scheduler = scheduler;
    this.audit = audit;
    this.prepareTimeout = opts.prepareTimeout ?? 10;
    this.retryInterval = opts.retryInterval ?? 5;
    this.maxRetries = opts.maxRetries ?? 3;
    this.onEvent = opts.onEvent ?? (() => {});
    this.wal = new Wal(walPath);
    this.transport.register(this);
  }

  isOnline(): boolean {
    return this.online;
  }

  stateOf(txnId: string): TxnState | undefined {
    return this.txns.get(txnId)?.state;
  }

  // ---------- normal operation ----------

  beginTransaction(txnId: string, participants: string[]): void {
    if (this.txns.has(txnId)) throw new Error(`duplicate txnId ${txnId}`);
    this.wal.append({ op: "BEGIN", txnId, participants: [...participants] });
    const ctx: TxnContext = {
      txnId,
      participants: [...participants],
      state: "PREPARING",
      votes: new Map(),
      acks: new Set(),
      decision: null,
      retries: new Map(),
      audited: new Set(),
    };
    this.txns.set(txnId, ctx);
    this.log(`txn=${txnId} BEGIN participants=[${participants}]`);
    for (const p of participants) {
      this.transport.send(this.id, p, { type: "PREPARE", txnId });
    }
    this.scheduler.schedule(this.prepareTimeout, () =>
      this.onPrepareTimeout(txnId),
    );
  }

  private onPrepareTimeout(txnId: string): void {
    if (!this.online) return;
    const ctx = this.txns.get(txnId);
    if (!ctx || ctx.state !== "PREPARING") return;
    const missing = ctx.participants.filter((p) => !ctx.votes.has(p));
    this.log(`txn=${txnId} prepare timeout, missing votes from [${missing}]`);
    this.decide(ctx, "ABORT");
  }

  private decide(ctx: TxnContext, decision: Decision): void {
    this.wal.append({ op: "DECISION", txnId: ctx.txnId, decision });
    ctx.decision = decision;
    ctx.state = decision === "COMMIT" ? "COMMITTING" : "ABORTING";
    this.log(`txn=${ctx.txnId} DECISION ${decision}`);
    for (const p of ctx.participants) {
      this.transport.send(this.id, p, { type: "DECISION", txnId: ctx.txnId, decision });
    }
    this.scheduler.schedule(this.retryInterval, () => this.onRetryRound(ctx.txnId));
  }

  private onRetryRound(txnId: string): void {
    if (!this.online) return;
    const ctx = this.txns.get(txnId);
    if (!ctx || (ctx.state !== "COMMITTING" && ctx.state !== "ABORTING")) return;
    let needsMore = false;
    for (const p of ctx.participants) {
      if (ctx.acks.has(p)) continue;
      const count = (ctx.retries.get(p) ?? 0) + 1;
      ctx.retries.set(p, count);
      if (count > this.maxRetries) {
        if (!ctx.audited.has(p)) {
          ctx.audited.add(p);
          this.audit.record({
            event: "PENDING_PARTICIPANT",
            txnId,
            participant: p,
            retries: this.maxRetries,
            at: this.scheduler.now,
          });
          this.log(`txn=${txnId} participant ${p} unresponsive after ${this.maxRetries} retries -> PENDING audit`);
        }
        continue;
      }
      needsMore = true;
      this.transport.send(this.id, p, {
        type: "DECISION",
        txnId,
        decision: ctx.decision!,
      });
    }
    if (needsMore) {
      this.scheduler.schedule(this.retryInterval, () => this.onRetryRound(txnId));
    }
  }

  // ---------- message handling ----------

  receive(from: string, msg: Message): void {
    switch (msg.type) {
      case "VOTE":
        this.onVote(from, msg.txnId, msg.vote);
        break;
      case "ACK":
        this.onAck(from, msg.txnId);
        break;
      case "STATUS":
        this.onStatus(from, msg.txnId, msg.status);
        break;
      default:
        break;
    }
  }

  private onVote(from: string, txnId: string, vote: Vote): void {
    const ctx = this.txns.get(txnId);
    if (!ctx || ctx.state !== "PREPARING") return; // late/duplicate vote
    if (ctx.votes.has(from)) return; // duplicate vote from same participant
    this.wal.append({ op: "VOTE", txnId, participant: from, vote });
    ctx.votes.set(from, vote);
    this.log(`txn=${txnId} VOTE ${vote} from ${from}`);
    if (vote === "NO") {
      this.decide(ctx, "ABORT");
      return;
    }
    if (ctx.participants.every((p) => ctx.votes.get(p) === "YES")) {
      this.decide(ctx, "COMMIT");
    }
  }

  private onAck(from: string, txnId: string): void {
    const ctx = this.txns.get(txnId);
    if (!ctx || (ctx.state !== "COMMITTING" && ctx.state !== "ABORTING")) return;
    if (ctx.acks.has(from)) return; // duplicate ACK
    this.wal.append({ op: "ACK", txnId, participant: from });
    ctx.acks.add(from);
    this.log(`txn=${txnId} ACK from ${from}`);
    this.maybeFinish(ctx);
  }

  private onStatus(
    from: string,
    txnId: string,
    status: "PREPARED" | "COMMITTED" | "ABORTED" | "UNKNOWN",
  ): void {
    const ctx = this.txns.get(txnId);
    if (!ctx || (ctx.state !== "COMMITTING" && ctx.state !== "ABORTING")) return;
    if (ctx.acks.has(from)) return;
    this.log(`txn=${txnId} STATUS ${status} from ${from}`);
    const resolved =
      (status === "COMMITTED" && ctx.decision === "COMMIT") ||
      (status === "ABORTED" && ctx.decision === "ABORT");
    if (resolved) {
      this.wal.append({ op: "ACK", txnId, participant: from });
      ctx.acks.add(from);
      this.maybeFinish(ctx);
    } else {
      // participant has not applied the decision yet: (re)send it
      this.transport.send(this.id, from, {
        type: "DECISION",
        txnId,
        decision: ctx.decision!,
      });
    }
  }

  private maybeFinish(ctx: TxnContext): void {
    if (ctx.participants.every((p) => ctx.acks.has(p))) {
      this.wal.append({ op: "END", txnId: ctx.txnId });
      ctx.state = ctx.decision === "COMMIT" ? "COMMITTED" : "ABORTED";
      this.log(`txn=${ctx.txnId} END (${ctx.state})`);
    }
  }

  // ---------- crash & recovery ----------

  crash(): void {
    this.online = false;
    this.log("coordinator CRASH");
  }

  /**
   * Replay the WAL and resume every incomplete transaction.
   * Safe to call on a fresh instance constructed over the same WAL file.
   */
  recover(): void {
    this.txns.clear();
    for (const rec of this.wal.replay()) {
      switch (rec.op) {
        case "BEGIN":
          this.txns.set(rec.txnId, {
            txnId: rec.txnId,
            participants: [...rec.participants],
            state: "PREPARING",
            votes: new Map(),
            acks: new Set(),
            decision: null,
            retries: new Map(),
            audited: new Set(),
          });
          break;
        case "VOTE":
          this.txns.get(rec.txnId)?.votes.set(rec.participant, rec.vote);
          break;
        case "DECISION": {
          const ctx = this.txns.get(rec.txnId);
          if (ctx) {
            ctx.decision = rec.decision;
            ctx.state = rec.decision === "COMMIT" ? "COMMITTING" : "ABORTING";
          }
          break;
        }
        case "ACK":
          this.txns.get(rec.txnId)?.acks.add(rec.participant);
          break;
        case "END": {
          const ctx = this.txns.get(rec.txnId);
          if (ctx) ctx.state = ctx.decision === "COMMIT" ? "COMMITTED" : "ABORTED";
          break;
        }
      }
    }
    this.online = true;
    this.log("coordinator RECOVER: WAL replayed");
    for (const ctx of this.txns.values()) {
      if (ctx.state === "COMMITTED" || ctx.state === "ABORTED") continue;
      if (ctx.decision === null) {
        // crashed before deciding: presumptive abort
        this.log(`txn=${ctx.txnId} no decision in WAL -> presumptive ABORT`);
        this.decide(ctx, "ABORT");
      } else {
        // crashed after deciding: query participants and finish the txn
        this.log(`txn=${ctx.txnId} resume ${ctx.state}, querying participants`);
        for (const p of ctx.participants) {
          if (!ctx.acks.has(p)) {
            this.transport.send(this.id, p, { type: "QUERY", txnId: ctx.txnId });
          }
        }
        this.scheduler.schedule(this.retryInterval, () =>
          this.onRetryRound(ctx.txnId),
        );
      }
    }
  }

  private log(line: string): void {
    this.onEvent(`[t=${this.scheduler.now}] ${line}`);
  }
}
