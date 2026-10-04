import type { ColdStore, Copy, LocalStore, Receipt } from './store';
import { copyValid } from './store';
import type { Decision, Journal } from './journal';

export enum MigState {
  Idle = 'IDLE',
  Uploading = 'UPLOADING',
  AwaitingReceipt = 'AWAITING_RECEIPT',
  Verifying = 'VERIFYING',
  Releasing = 'RELEASING', // decision persisted, waiting for local handles to drain
  Reclaimed = 'RECLAIMED',
  Aborted = 'ABORTED',
}

export interface Scheduler {
  now: number;
  after(delay: number, fn: () => void): void;
}

export interface MigratorConfig {
  uploadLatency: number;
  receiptTimeout: number;
  verifyDelay: number;
  reclaimPoll: number;
}

export interface MigratorDeps {
  local: LocalStore;
  cold: ColdStore;
  journal: Journal;
  scheduler: Scheduler;
  config: MigratorConfig;
  log?: (msg: string) => void;
  // The network/simulator decides whether (and when) the receipt is delivered.
  onUploadAck?: (receipt: Receipt, m: Migrator) => void;
  onDecision?: (decision: Decision, m: Migrator) => void;
  onSettled?: (m: Migrator) => void; // terminal: RECLAIMED or ABORTED
}

// Per-object migration state machine.
//
// Safety rules:
//  1. Upload success (or a receipt) NEVER authorizes local deletion by itself.
//  2. The target must be read back: recomputed digest + version must match the
//     current local copy. A receipt is only a hint that triggers verification.
//  3. A RECLAIM_LOCAL decision must be persisted to the journal first.
//  4. Reclaim revalidates everything at the last moment: decision still
//     persisted, local version/digest unchanged since the decision, target
//     still healthy, and zero open local handles.
export class Migrator {
  state = MigState.Idle;
  round = 0;
  decision: Decision | null = null;
  abortReason: string | null = null;
  private uploadedVersion = -1;
  private uploadedDigest = '';

  constructor(readonly objectId: string, private deps: MigratorDeps) {}

  private log(msg: string): void {
    this.deps.log?.(`[t=${this.deps.scheduler.now}] ${this.objectId}#r${this.round} ${msg}`);
  }

  start(): void {
    if (this.state !== MigState.Idle && this.state !== MigState.Aborted) return;
    const local = this.deps.local.read(this.objectId);
    if (!local) {
      this.log('no local copy; nothing to migrate');
      return;
    }
    this.round += 1;
    this.decision = null;
    this.abortReason = null;
    this.state = MigState.Uploading;
    const snapshot: Copy = { ...local };
    this.uploadedVersion = snapshot.version;
    this.uploadedDigest = snapshot.digest;
    this.log(`uploading v${snapshot.version} (${snapshot.digest})`);
    const round = this.round;
    this.deps.scheduler.after(this.deps.config.uploadLatency, () =>
      this.onUploadDone(round, snapshot),
    );
  }

  private onUploadDone(round: number, snapshot: Copy): void {
    if (round !== this.round || this.state !== MigState.Uploading) return;
    const receipt = this.deps.cold.put(this.objectId, snapshot, this.deps.scheduler.now);
    this.state = MigState.AwaitingReceipt;
    this.log(`uploaded v${snapshot.version}; awaiting receipt (timeout in ${this.deps.config.receiptTimeout})`);
    this.deps.scheduler.after(this.deps.config.receiptTimeout, () => this.onReceiptTimeout(round));
    this.deps.onUploadAck?.(receipt, this);
  }

  // Called by the network layer when a receipt arrives (possibly late,
  // duplicated, or replayed from an older generation).
  deliverReceipt(receipt: Receipt): void {
    if (this.state !== MigState.AwaitingReceipt) {
      this.log(`ignoring receipt v${receipt.version} in state ${this.state}`);
      return;
    }
    if (receipt.version !== this.uploadedVersion || receipt.digest !== this.uploadedDigest) {
      this.log(`ignoring receipt v${receipt.version}: not for current upload v${this.uploadedVersion}`);
      return;
    }
    const local = this.deps.local.read(this.objectId);
    if (!local) return;
    if (local.version !== receipt.version || local.digest !== receipt.digest) {
      // The object was updated after this upload: the old receipt must not
      // confirm the new version.
      this.log(
        `rejecting stale receipt v${receipt.version}: local is now v${local.version}; old receipts cannot confirm new versions`,
      );
      this.abort('stale-receipt');
      return;
    }
    this.state = MigState.Verifying;
    this.log(`receipt v${receipt.version} accepted as hint; verifying via read-back`);
    const round = this.round;
    this.deps.scheduler.after(this.deps.config.verifyDelay, () => this.verify(round));
  }

  private onReceiptTimeout(round: number): void {
    if (round !== this.round || this.state !== MigState.AwaitingReceipt) return;
    // Receipt loss only costs progress, never safety: verify by read-back.
    this.state = MigState.Verifying;
    this.log('receipt lost; verifying via direct read-back');
    this.deps.scheduler.after(this.deps.config.verifyDelay, () => this.verify(round));
  }

  private verify(round: number): void {
    if (round !== this.round || this.state !== MigState.Verifying) return;
    const local = this.deps.local.read(this.objectId);
    if (!local) {
      this.abort('local-copy-missing');
      return;
    }
    const remote = this.deps.cold.read(this.objectId);
    const ok =
      remote !== undefined &&
      copyValid(remote) &&
      remote.version === local.version &&
      remote.digest === local.digest;
    if (!ok) {
      const why = !remote
        ? 'target-missing'
        : !copyValid(remote)
          ? 'target-corrupted'
          : 'version-or-digest-mismatch';
      this.log(`read-back verification failed: ${why}; local copy retained`);
      this.abort(why);
      return;
    }
    // Read-back matched. Persist the migration decision BEFORE any reclaim.
    const decision = this.deps.journal.append(
      this.objectId,
      local.version,
      local.digest,
      this.deps.scheduler.now,
    );
    if (!decision) {
      this.log('migration decision could not be persisted; local copy retained');
      this.abort('decision-not-persisted');
      return;
    }
    this.decision = decision;
    this.state = MigState.Releasing;
    this.log(`decision #${decision.seq} persisted for v${decision.version}; waiting for handles to drain`);
    this.deps.onDecision?.(decision, this);
    this.deps.scheduler.after(this.deps.config.reclaimPoll, () => this.attemptReclaim(round));
  }

  private attemptReclaim(round: number): void {
    if (round !== this.round || this.state !== MigState.Releasing || !this.decision) return;
    const decision = this.decision;
    const local = this.deps.local.read(this.objectId);
    if (!local || local.version !== decision.version || local.digest !== decision.digest) {
      // Updated after the decision: the old decision must not reclaim the new version.
      this.log('object updated after decision; old decision cannot reclaim new version');
      this.abort('superseded-by-update');
      return;
    }
    if (!this.deps.journal.find(decision.objectId, decision.version, decision.digest)) {
      this.abort('decision-lost');
      return;
    }
    // Final revalidation: the target must still be healthy right now.
    const remote = this.deps.cold.read(this.objectId);
    if (
      !remote ||
      !copyValid(remote) ||
      remote.version !== decision.version ||
      remote.digest !== decision.digest
    ) {
      this.log('final revalidation failed: target unhealthy; local copy retained');
      this.abort('target-unhealthy-before-reclaim');
      return;
    }
    const open = this.deps.local.openHandles(this.objectId);
    if (open > 0) {
      this.log(`reclaim delayed: ${open} local handle(s) open`);
      this.deps.scheduler.after(this.deps.config.reclaimPoll, () => this.attemptReclaim(round));
      return;
    }
    this.deps.local.remove(this.objectId);
    this.state = MigState.Reclaimed;
    this.log(`local copy of v${decision.version} reclaimed; remote copy is authoritative`);
    this.deps.onSettled?.(this);
  }

  private abort(reason: string): void {
    this.state = MigState.Aborted;
    this.abortReason = reason;
    this.log(`round aborted: ${reason}`);
    this.deps.onSettled?.(this);
  }
}
