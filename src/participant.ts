import type {
  Decision,
  Message,
  ParticipantStatus,
  Vote,
} from "./types.ts";
import type { Transport, Endpoint } from "./transport.ts";
import { Wal } from "./wal.ts";

type ParticipantWalRecord =
  | { op: "PREPARED"; txnId: string }
  | { op: "APPLIED"; txnId: string; decision: Decision };

/**
 * 2PC participant with its own WAL so it survives crashes.
 * All handlers are idempotent: duplicate PREPARE / DECISION / QUERY
 * messages never corrupt state.
 */
export class Participant implements Endpoint {
  readonly id: string;
  private online = true;
  private wal: Wal<ParticipantWalRecord>;
  private prepared = new Set<string>();
  private applied = new Map<string, Decision>();
  readonly appliedLog: Array<{ txnId: string; decision: Decision }> = [];
  private readonly transport: Transport;
  private readonly voteFor: (txnId: string) => Vote;

  constructor(
    id: string,
    transport: Transport,
    walPath: string,
    voteFor: (txnId: string) => Vote = () => "YES",
  ) {
    this.id = id;
    this.transport = transport;
    this.voteFor = voteFor;
    this.wal = new Wal(walPath);
    this.transport.register(this);
    this.replayWal();
  }

  private replayWal(): void {
    for (const rec of this.wal.replay()) {
      if (rec.op === "PREPARED") this.prepared.add(rec.txnId);
      else this.applied.set(rec.txnId, rec.decision);
    }
  }

  isOnline(): boolean {
    return this.online;
  }

  crash(): void {
    this.online = false;
  }

  restart(): void {
    this.prepared.clear();
    this.applied.clear();
    this.replayWal();
    this.online = true;
  }

  statusOf(txnId: string): ParticipantStatus {
    const decision = this.applied.get(txnId);
    if (decision) return decision === "COMMIT" ? "COMMITTED" : "ABORTED";
    if (this.prepared.has(txnId)) return "PREPARED";
    return "UNKNOWN";
  }

  receive(from: string, msg: Message): void {
    switch (msg.type) {
      case "PREPARE": {
        if (this.applied.has(msg.txnId)) break; // already resolved; stay silent
        if (!this.prepared.has(msg.txnId)) {
          const vote = this.voteFor(msg.txnId);
          if (vote === "YES") {
            this.wal.append({ op: "PREPARED", txnId: msg.txnId });
            this.prepared.add(msg.txnId);
          }
          this.transport.send(this.id, from, { type: "VOTE", txnId: msg.txnId, vote });
        } else {
          // duplicate PREPARE: resend the same YES vote
          this.transport.send(this.id, from, { type: "VOTE", txnId: msg.txnId, vote: "YES" });
        }
        break;
      }
      case "DECISION": {
        if (!this.applied.has(msg.txnId)) {
          this.wal.append({ op: "APPLIED", txnId: msg.txnId, decision: msg.decision });
          this.applied.set(msg.txnId, msg.decision);
          this.prepared.delete(msg.txnId);
          this.appliedLog.push({ txnId: msg.txnId, decision: msg.decision });
        }
        this.transport.send(this.id, from, { type: "ACK", txnId: msg.txnId });
        break;
      }
      case "QUERY": {
        this.transport.send(this.id, from, {
          type: "STATUS",
          txnId: msg.txnId,
          status: this.statusOf(msg.txnId),
        });
        break;
      }
      default:
        break; // participants ignore VOTE/ACK/STATUS
    }
  }
}
