import type {
  BoundaryCheck,
  CausalMessage,
  GapHandlerOptions,
  GapProof,
  GapReason,
  PendingRecord,
  ReceiveResult,
  RecoveryReport,
  RecoveryStep,
  ResetBoundary,
} from "./types.ts";

/**
 * Causal delivery buffer with gap recovery.
 *
 * Messages are delivered only after all of their causal predecessors. A
 * predecessor that expires or is deleted would leave its successors pending
 * forever; to recover, the node records a {@link GapProof} at removal time,
 * folds the proofs into a {@link ResetBoundary} snapshot, and installs that
 * snapshot. Only then may old, proven-gone predecessors be skipped — and
 * never predecessors at or after the boundary watermark, so dependencies
 * created by events that interleave with the reset are still awaited.
 */
export class CausalGapHandler<T = unknown> {
  private logicalNow: number;
  private boundarySeq = 0;

  private readonly deliveredOrder: CausalMessage<T>[] = [];
  private readonly deliveredIds = new Set<string>();
  private readonly knownClocks = new Map<string, number>();
  private readonly pending = new Map<string, PendingRecord>();
  private readonly gapProofs = new Map<string, GapProof>();
  private installedBoundary: ResetBoundary | null = null;

  constructor(options: GapHandlerOptions = {}) {
    const clockSource = options.now;
    this.logicalNow = clockSource ? clockSource() : 0;
  }

  /** Advance the logical clock to at least `clock` (monotonic). */
  observe(clock: number): void {
    if (clock > this.logicalNow) this.logicalNow = clock;
  }

  now(): number {
    return this.logicalNow;
  }

  /**
   * Ingest a message. Delivers it (and any successors it unblocks) when all
   * of its predecessors are delivered or provably skippable; otherwise the
   * message is buffered as pending.
   */
  receive(message: CausalMessage<T>): ReceiveResult {
    if (this.deliveredIds.has(message.id) || this.pending.has(message.id)) {
      return { status: "duplicate", missing: [] };
    }
    for (const dep of message.deps) {
      if (message.clock <= this.clockOf(dep)) {
        throw new Error(
          `message ${message.id} (clock ${message.clock}) must have a clock ` +
            `greater than its dep ${dep} (clock ${this.clockOf(dep)})`,
        );
      }
    }
    this.observe(message.clock);
    this.knownClocks.set(message.id, message.clock);

    const missing = this.unresolvedDeps(message);
    if (missing.length > 0) {
      this.pending.set(message.id, {
        message,
        missing: new Set(missing),
        status: "pending",
      });
      return { status: "pending", missing };
    }

    this.deliver(message, []);
    return { status: "delivered", missing: [] };
  }

  /**
   * Mark a message as permanently gone (TTL expiry or deletion) and record a
   * gap proof for it. The proof is the only thing that can later justify
   * skipping this message as a predecessor.
   *
    * `clock` optionally records the removed message's logical clock when the
    * tombstone carries it; a proof whose clock is at or after a boundary's
    * watermark can never justify a skip under that boundary.
   */
  expire(
    messageId: string,
    reason: GapReason = "expired",
    clock?: number,
  ): GapProof {
    return this.recordGap(messageId, reason, clock);
  }

  /** Alias of {@link expire} for explicit deletions. */
  delete(messageId: string, clock?: number): GapProof {
    return this.recordGap(messageId, "deleted", clock);
  }

  /** All gap proofs recorded so far, oldest first. */
  proofs(): readonly GapProof[] {
    return [...this.gapProofs.values()];
  }

  /**
   * Take a reset boundary snapshot at the current logical time, capturing
   * the delivered frontier and every gap proof recorded so far.
   */
  createResetBoundary(id?: string): ResetBoundary {
    this.boundarySeq += 1;
    return {
      id: id ?? `boundary-${this.boundarySeq}@${this.logicalNow}`,
      watermark: this.logicalNow,
      delivered: this.deliveredOrder.map((m) => m.id),
      gapProofs: this.proofs(),
    };
  }

  /**
   * Validate a boundary snapshot before installing it: gap proofs must be
   * well formed and must not reach past the watermark. A proof for a message
   * that is also in the delivered set is fine: it was delivered, then
   * expired.
   */
  validateBoundary(boundary: ResetBoundary): BoundaryCheck {
    const errors: string[] = [];
    const seen = new Set<string>();
    for (const proof of boundary.gapProofs) {
      if (seen.has(proof.messageId)) {
        errors.push(`duplicate gap proof for ${proof.messageId}`);
      }
      seen.add(proof.messageId);
      if (proof.removedAt > boundary.watermark) {
        errors.push(
          `gap proof for ${proof.messageId} (removedAt ${proof.removedAt}) ` +
            `is newer than the boundary watermark ${boundary.watermark}`,
        );
      }
      if (proof.clock >= boundary.watermark) {
        errors.push(
          `gap proof for ${proof.messageId} (clock ${proof.clock}) is not ` +
            `older than the boundary watermark ${boundary.watermark}`,
        );
      }
    }
    return { ok: errors.length === 0, errors };
  }

  /**
   * Install a reset boundary snapshot. Pending messages whose remaining
   * missing predecessors are all covered by the boundary's gap proofs are
   * released and delivered. Missing predecessors that are not proven gone —
   * including any dependency created after the watermark — keep the message
   * pending. Returns the affected messages and the recovery steps taken.
   */
  installBoundary(boundary: ResetBoundary): RecoveryReport {
    const check = this.validateBoundary(boundary);
    if (!check.ok) {
      throw new Error(`invalid reset boundary: ${check.errors.join("; ")}`);
    }
    this.installedBoundary = boundary;
    this.observe(boundary.watermark);
    for (const proof of boundary.gapProofs) {
      this.gapProofs.set(proof.messageId, proof);
      this.knownClocks.set(proof.messageId, proof.clock);
    }

    const steps: RecoveryStep[] = [];
    for (const record of [...this.pending.values()]) {
      if (!this.pending.has(record.message.id)) continue;
      const before = new Set(record.missing);
      const missing = this.unresolvedDeps(record.message);
      const skipped = [...before].filter((dep) => !missing.includes(dep));
      if (skipped.length === 0) continue;

      if (missing.length === 0) {
        this.pending.delete(record.message.id);
        record.status = "released";
        steps.push({
          messageId: record.message.id,
          action: "released",
          skippedDeps: skipped,
          remainingDeps: [],
          detail:
            `all remaining predecessors are proven gone by boundary ` +
            `${boundary.id}; released from the causal gap`,
        });
        this.deliver(record.message, steps);
      } else {
        record.missing = new Set(missing);
        steps.push({
          messageId: record.message.id,
          action: "still-pending",
          skippedDeps: skipped,
          remainingDeps: missing,
          detail:
            `skipped proven-gone predecessors, but ${missing.join(", ")} ` +
            `is not covered by a gap proof and must still be awaited`,
        });
      }
    }
    return { boundaryId: boundary.id, steps };
  }

  /**
   * Audit the currently pending messages against the installed boundary (if
   * any) without changing any state: which messages are stuck, which
   * predecessors could be skipped, and what the recovery steps would be.
   */
  audit(): RecoveryReport {
    const steps: RecoveryStep[] = [];
    for (const record of this.pending.values()) {
      const skippable: string[] = [];
      const remaining: string[] = [];
      for (const dep of record.missing) {
        (this.isSkippable(dep) ? skippable : remaining).push(dep);
      }
      const wouldRelease = remaining.length === 0 && skippable.length > 0;
      steps.push({
        messageId: record.message.id,
        action: wouldRelease ? "released" : "still-pending",
        skippedDeps: skippable,
        remainingDeps: remaining,
        detail: this.installedBoundary
          ? wouldRelease
            ? `would be released under installed boundary ${this.installedBoundary.id}`
            : `would stay pending: no gap proof covers ${remaining.join(", ")}`
          : `no reset boundary installed; ${record.missing.size} predecessor(s) ` +
            `cannot be skipped, install a boundary snapshot to recover`,
      });
    }
    return {
      boundaryId: this.installedBoundary ? this.installedBoundary.id : null,
      steps,
    };
  }

  /** Snapshot of a pending message's state, or undefined if not pending. */
  pendingState(
    messageId: string,
  ): { missing: readonly string[]; status: string } | undefined {
    const record = this.pending.get(messageId);
    if (!record) return undefined;
    return { missing: [...record.missing], status: record.status };
  }

  pendingCount(): number {
    return this.pending.size;
  }

  delivered(): readonly CausalMessage<T>[] {
    return this.deliveredOrder;
  }

  isDelivered(messageId: string): boolean {
    return this.deliveredIds.has(messageId);
  }

  private recordGap(
    messageId: string,
    reason: GapReason,
    clock?: number,
  ): GapProof {
    const existing = this.gapProofs.get(messageId);
    if (existing) return existing;
    if (this.pending.has(messageId)) {
      throw new Error(
        `cannot expire ${messageId}: it is a pending successor, not a predecessor`,
      );
    }
    if (clock !== undefined) {
      this.observe(clock);
      this.knownClocks.set(messageId, clock);
    }
    this.logicalNow += 1;
    const proof: GapProof = {
      messageId,
      reason,
      clock: this.clockOf(messageId),
      removedAt: this.logicalNow,
    };
    this.gapProofs.set(messageId, proof);
    this.knownClocks.set(messageId, proof.clock);
    return proof;
  }

  private unresolvedDeps(message: CausalMessage<T>): string[] {
    const missing: string[] = [];
    for (const dep of message.deps) {
      if (this.deliveredIds.has(dep)) continue;
      if (this.isSkippable(dep)) continue;
      missing.push(dep);
    }
    return missing;
  }

  /**
   * A predecessor may be skipped only when a reset boundary is installed and
   * the boundary carries a gap proof for it that predates the watermark.
   * Dependencies from events at or after the watermark are never skippable.
   */
  private isSkippable(dep: string): boolean {
    const boundary = this.installedBoundary;
    if (!boundary) return false;
    const proof = this.gapProofs.get(dep);
    if (!proof) return false;
    return (
      proof.clock < boundary.watermark && proof.removedAt <= boundary.watermark
    );
  }

  private clockOf(messageId: string): number {
    return this.knownClocks.get(messageId) ?? 0;
  }

  /** Deliver a message, then cascade to any successors it unblocks. */
  private deliver(message: CausalMessage<T>, steps: RecoveryStep[]): void {
    if (this.deliveredIds.has(message.id)) return;
    this.deliveredIds.add(message.id);
    this.deliveredOrder.push(message);
    this.knownClocks.set(message.id, message.clock);

    for (const record of [...this.pending.values()]) {
      if (!record.missing.has(message.id)) continue;
      const missing = this.unresolvedDeps(record.message);
      if (missing.length > 0) {
        record.missing = new Set(missing);
        continue;
      }
      this.pending.delete(record.message.id);
      record.status = "delivered";
      steps.push({
        messageId: record.message.id,
        action: "delivered",
        skippedDeps: [],
        remainingDeps: [],
        detail: `unblocked by delivery of ${message.id}`,
      });
      this.deliver(record.message, steps);
    }
  }
}
