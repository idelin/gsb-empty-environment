/**
 * Causal gap handling with reset boundaries.
 *
 * Problem: a successor message buffered while waiting for a causal
 * predecessor must not hang forever when that predecessor has expired
 * (deleted by retention) before it was ever observed.
 *
 * Solution: a reset boundary carrying gap proofs. A gap proof attests
 * that a specific predecessor is unrecoverable as of a logical horizon.
 * Old predecessors may only be skipped after the boundary snapshot has
 * been installed. Events newer than the boundary horizon are never
 * skipped by the boundary, so a reset interleaved with new events cannot
 * incorrectly release new dependencies.
 */

export interface CausalMessage {
  id: string;
  /** Logical sequence number; monotonically increasing per origin. */
  seq: number;
  /** Ids of causal predecessors that must be delivered first. */
  predecessors: string[];
  payload?: unknown;
}

export interface GapProof {
  /** The expired predecessor this proof covers. */
  predecessorId: string;
  /** Logical horizon at which the predecessor was known to be expired. */
  expiredAtSeq: number;
  /** Pending messages waiting on this predecessor at proof time. */
  affectedPending: string[];
  reason: string;
}

export interface ResetBoundary {
  id: string;
  /** Logical horizon: only messages with seq <= horizon are "old". */
  horizon: number;
  gapProofs: GapProof[];
  installed: boolean;
}

export type ReceiveStatus =
  | "delivered"
  | "buffered"
  | "delivered-with-skipped-predecessors";

export interface ReceiveResult {
  status: ReceiveStatus;
  missingPredecessors: string[];
  skippedPredecessors: string[];
}

export type AffectedStatus =
  | "awaiting-predecessor"
  | "awaiting-boundary-install"
  | "released-by-boundary"
  | "blocked-new-dependency";

export interface AffectedMessage {
  messageId: string;
  seq: number;
  missingPredecessors: string[];
  expiredPredecessors: string[];
  status: AffectedStatus;
  recovery: string[];
}

interface PendingEntry {
  message: CausalMessage;
  missing: Set<string>;
}

export interface InstallResult {
  released: string[];
  stillPending: string[];
}

export class CausalGapHandler {
  private delivered = new Set<string>();
  private expired = new Map<string, { atSeq: number; reason: string }>();
  private pending = new Map<string, PendingEntry>();
  private releasedByBoundary = new Map<string, { seq: number; skipped: string[] }>();
  private boundary: ResetBoundary | null = null;
  private horizon = 0;
  private boundaryCounter = 0;

  /** Observe a message. Buffers it if causal predecessors are missing. */
  receive(message: CausalMessage): ReceiveResult {
    if (this.delivered.has(message.id)) {
      return { status: "delivered", missingPredecessors: [], skippedPredecessors: [] };
    }
    this.horizon = Math.max(this.horizon, message.seq);

    const missing = message.predecessors.filter((p) => !this.delivered.has(p));
    if (missing.length === 0) {
      this.deliver(message, []);
      return { status: "delivered", missingPredecessors: [], skippedPredecessors: [] };
    }

    const skippable = this.skippableByInstalledBoundary(message, missing);
    if (skippable) {
      this.deliver(message, missing);
      return {
        status: "delivered-with-skipped-predecessors",
        missingPredecessors: [],
        skippedPredecessors: missing,
      };
    }

    this.pending.set(message.id, { message, missing: new Set(missing) });
    return { status: "buffered", missingPredecessors: missing, skippedPredecessors: [] };
  }

  /** Mark a predecessor as expired (deleted before ever being observed). */
  expire(predecessorId: string, reason = "retention-expired"): void {
    if (this.delivered.has(predecessorId)) return;
    this.expired.set(predecessorId, { atSeq: this.horizon, reason });
  }

  /**
   * Create a reset boundary snapshot with gap proofs for every currently
   * expired predecessor. Nothing is skipped until the boundary is installed.
   */
  createResetBoundary(): ResetBoundary {
    this.boundaryCounter += 1;
    const gapProofs: GapProof[] = [];
    for (const [predecessorId, info] of this.expired) {
      const affectedPending: string[] = [];
      for (const [id, entry] of this.pending) {
        if (entry.missing.has(predecessorId)) affectedPending.push(id);
      }
      gapProofs.push({
        predecessorId,
        expiredAtSeq: info.atSeq,
        affectedPending,
        reason: info.reason,
      });
    }
    return {
      id: `boundary-${this.boundaryCounter}`,
      horizon: this.horizon,
      gapProofs,
      installed: false,
    };
  }

  /**
   * Install a boundary snapshot. Only now may old predecessors covered by
   * gap proofs be skipped, and only for messages at or below the horizon.
   */
  installBoundary(boundary: ResetBoundary): InstallResult {
    boundary.installed = true;
    this.boundary = boundary;

    const released: string[] = [];
    const stillPending: string[] = [];
    for (const [id, entry] of this.pending) {
      const missing = [...entry.missing];
      if (this.skippableByInstalledBoundary(entry.message, missing)) {
        this.pending.delete(id);
        this.deliver(entry.message, missing);
        released.push(id);
      } else {
        stillPending.push(id);
      }
    }
    return { released, stillPending };
  }

  /** List messages affected by gaps, with per-message recovery steps. */
  getAffectedMessages(): AffectedMessage[] {
    const affected: AffectedMessage[] = [];
    for (const [id, info] of this.releasedByBoundary) {
      affected.push({
        messageId: id,
        seq: info.seq,
        missingPredecessors: [],
        expiredPredecessors: info.skipped,
        status: "released-by-boundary",
        recovery: [`no action needed; skipped expired predecessors: ${info.skipped.join(", ")}`],
      });
    }
    for (const [id, entry] of this.pending) {
      const missing = [...entry.missing];
      const expiredMissing = missing.filter((p) => this.expired.has(p));
      const waitingMissing = missing.filter((p) => !this.expired.has(p));
      const recovery: string[] = [];
      let status: AffectedStatus;

      if (this.boundary && entry.message.seq > this.boundary.horizon) {
        status = "blocked-new-dependency";
        recovery.push(
          "message is newer than the installed boundary horizon; the boundary must not skip its dependencies",
        );
      } else if (expiredMissing.length > 0) {
        status = "awaiting-boundary-install";
        recovery.push(
          `create and install a reset boundary with gap proofs for: ${expiredMissing.join(", ")}`,
        );
      } else {
        status = "awaiting-predecessor";
      }

      for (const p of waitingMissing) {
        recovery.push(`retransmit or fetch missing predecessor ${p} from a peer`);
      }
      for (const p of expiredMissing) {
        if (status !== "awaiting-boundary-install") {
          recovery.push(
            `predecessor ${p} is expired; requires a new reset boundary created after this message's horizon`,
          );
        }
      }

      affected.push({
        messageId: id,
        seq: entry.message.seq,
        missingPredecessors: missing,
        expiredPredecessors: expiredMissing,
        status,
        recovery,
      });
    }
    return affected.sort((a, b) => a.messageId.localeCompare(b.messageId));
  }

  /** Ordered recovery steps for the whole handler. */
  getRecoveryPlan(): string[] {
    const steps: string[] = [];
    const affected = this.getAffectedMessages().filter(
      (a) => a.status !== "released-by-boundary",
    );
    if (affected.length === 0) {
      steps.push("no pending gaps; nothing to recover");
      return steps;
    }
    const needsBoundary = affected.filter((a) => a.status === "awaiting-boundary-install");
    if (needsBoundary.length > 0) {
      steps.push(
        `1. create a reset boundary (createResetBoundary) covering expired predecessors: ${[
          ...new Set(needsBoundary.flatMap((a) => a.expiredPredecessors)),
        ].join(", ")}`,
      );
      steps.push(
        `2. install the boundary (installBoundary) to release: ${needsBoundary
          .map((a) => a.messageId)
          .join(", ")}`,
      );
    }
    const needsFetch = affected.filter((a) =>
      a.missingPredecessors.some((p) => !this.expired.has(p)),
    );
    for (const a of needsFetch) {
      const fetchable = a.missingPredecessors.filter((p) => !this.expired.has(p));
      steps.push(
        `fetch missing predecessors ${fetchable.join(", ")} for message ${a.messageId} from a peer`,
      );
    }
    const blockedNew = affected.filter((a) => a.status === "blocked-new-dependency");
    for (const a of blockedNew) {
      steps.push(
        `message ${a.messageId} arrived after the boundary horizon; resolve its dependencies normally or create a newer boundary`,
      );
    }
    return steps;
  }

  isPending(messageId: string): boolean {
    return this.pending.has(messageId);
  }

  isDelivered(messageId: string): boolean {
    return this.delivered.has(messageId);
  }

  private skippableByInstalledBoundary(message: CausalMessage, missing: string[]): boolean {
    const boundary = this.boundary;
    if (!boundary || !boundary.installed) return false;
    // New events (seq above the boundary horizon) are never skipped.
    if (message.seq > boundary.horizon) return false;
    const proven = new Set(boundary.gapProofs.map((g) => g.predecessorId));
    return missing.every((p) => proven.has(p));
  }

  private deliver(message: CausalMessage, skipped: string[]): void {
    this.delivered.add(message.id);
    if (skipped.length > 0) {
      this.releasedByBoundary.set(message.id, { seq: message.seq, skipped });
    }
    // Cascading delivery of buffered successors.
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (const [id, entry] of this.pending) {
        for (const p of [...entry.missing]) {
          if (this.delivered.has(p)) entry.missing.delete(p);
        }
        if (entry.missing.size === 0) {
          this.pending.delete(id);
          this.delivered.add(id);
          progressed = true;
        }
      }
    }
  }
}
