import { digestOf } from './digest.ts';
import type { Decision, Receipt } from './types.ts';
import { MigState } from './types.ts';
import type { LocalStore } from './stores.ts';
import type { DecisionLog } from './decisionLog.ts';

/** Async services the state machine needs from the outside world. */
export interface MigrationEnv {
  upload(objectId: string, version: number, bytes: string): void;
  readBack(objectId: string, seq: number): void;
  persistDecision(decision: Decision): void;
  after(delay: number, name: string, cb: () => void): void;
  clock(): number;
  trace(msg: string): void;
}

export const RECEIPT_TIMEOUT = 10;

/**
 * Per-object migration state machine.
 *
 * Reclaim rule (all must hold):
 *   1. target read-back digest == digest of the migrating version, and
 *   2. the migration decision for (object, version, digest) is persisted, and
 *   3. no local read handles are open, and
 *   4. the local version is still the migrating version (no update raced in).
 *
 * Receipts only confirm the version they were issued for: a receipt for an
 * older version can never confirm a newer object.
 */
export class Migrator {
  state: MigState = MigState.LocalOnly;
  reclaimed = false;

  private migratingVersion = 0;
  private migratingDigest = '';
  private receipt: Receipt | null = null;
  private readBackSeq = 0;

  private readonly objectId: string;
  private readonly env: MigrationEnv;
  private readonly local: LocalStore;
  private readonly decisions: DecisionLog;
  private readonly unsafeFastReclaim: boolean;

  constructor(
    objectId: string,
    env: MigrationEnv,
    local: LocalStore,
    decisions: DecisionLog,
    unsafeFastReclaim = false,
  ) {
    this.objectId = objectId;
    this.env = env;
    this.local = local;
    this.decisions = decisions;
    this.unsafeFastReclaim = unsafeFastReclaim;
  }

  /** Start migrating if the object is local-only. */
  kick(): void {
    if (this.state === MigState.LocalOnly && this.local.has(this.objectId)) {
      this.beginUpload();
    }
  }

  private beginUpload(): void {
    const version = this.local.versionOf(this.objectId);
    const bytes = this.local.bytesOf(this.objectId);
    if (version === null || bytes === null) return;
    this.migratingVersion = version;
    this.migratingDigest = digestOf(bytes);
    this.receipt = null;
    this.state = MigState.Uploading;
    this.env.trace(`${this.objectId}: uploading v${version} (digest ${this.migratingDigest})`);
    this.env.upload(this.objectId, version, bytes);
  }

  /** The upload was applied at the target; a receipt may or may not arrive. */
  onUploadApplied(version: number): void {
    if (this.state !== MigState.Uploading || version !== this.migratingVersion) return;
    this.state = MigState.AwaitingReceipt;
    this.env.after(RECEIPT_TIMEOUT, 'receipt-timeout', () => {
      if (this.state === MigState.AwaitingReceipt) {
        this.env.trace(`${this.objectId}: receipt timeout (lost in transit) — probing target directly`);
        this.beginVerify();
      }
    });
  }

  onReceipt(receipt: Receipt): void {
    if (receipt.version !== this.migratingVersion || receipt.digest !== this.migratingDigest) {
      this.env.trace(
        `${this.objectId}: STALE receipt for v${receipt.version} rejected ` +
          `(migrating v${this.migratingVersion}) — old receipts cannot confirm new versions`,
      );
      return;
    }
    if (this.state !== MigState.AwaitingReceipt) return;
    this.receipt = receipt;
    if (this.unsafeFastReclaim) {
      // Naive policy, for comparison only: trust the receipt and delete now.
      this.env.trace(`${this.objectId}: UNSAFE reclaim on receipt alone (no verify, no decision)`);
      this.forceReclaim();
      return;
    }
    this.beginVerify();
  }

  private beginVerify(): void {
    this.state = MigState.Verifying;
    this.readBackSeq += 1;
    this.env.readBack(this.objectId, this.readBackSeq);
  }

  onReadBack(seq: number, remoteDigest: string | null): void {
    if (this.state !== MigState.Verifying || seq !== this.readBackSeq) return;
    if (remoteDigest === this.migratingDigest) {
      this.state = MigState.PersistingDecision;
      this.env.persistDecision({
        objectId: this.objectId,
        version: this.migratingVersion,
        digest: this.migratingDigest,
        decidedAt: this.env.clock(),
      });
    } else {
      this.env.trace(
        `${this.objectId}: read-back digest mismatch (target corrupt/stale) — re-uploading v${this.migratingVersion}`,
      );
      this.beginUpload();
    }
  }

  onDecisionPersisted(decision: Decision): void {
    if (this.state !== MigState.PersistingDecision) return;
    if (decision.version !== this.migratingVersion || decision.digest !== this.migratingDigest) return;
    this.attemptReclaim();
  }

  private attemptReclaim(): void {
    if (this.local.handleCount(this.objectId) > 0) {
      this.state = MigState.ReclaimBlocked;
      this.env.trace(
        `${this.objectId}: reclaim DEFERRED — ${this.local.handleCount(this.objectId)} local handle(s) still open`,
      );
      return;
    }
    this.doReclaim();
  }

  private doReclaim(): void {
    if (!this.decisions.has(this.objectId, this.migratingVersion, this.migratingDigest)) {
      throw new Error(`${this.objectId}: reclaim without a persisted decision — refused`);
    }
    if (this.local.versionOf(this.objectId) !== this.migratingVersion) {
      throw new Error(`${this.objectId}: local version changed before reclaim — refused`);
    }
    if (!this.local.delete(this.objectId)) {
      throw new Error(`${this.objectId}: local store refused delete (handles open)`);
    }
    this.state = MigState.Reclaimed;
    this.reclaimed = true;
    this.env.trace(`${this.objectId}: local copy of v${this.migratingVersion} RECLAIMED`);
  }

  /** Unsafe demo path: delete with no verification and no persisted decision. */
  private forceReclaim(): void {
    if (this.local.handleCount(this.objectId) > 0) {
      this.state = MigState.ReclaimBlocked;
      return;
    }
    this.local.delete(this.objectId);
    this.state = MigState.Reclaimed;
    this.reclaimed = true;
  }

  onHandlesDrained(): void {
    if (this.state !== MigState.ReclaimBlocked) return;
    if (this.local.handleCount(this.objectId) > 0) return; // other handles still open
    this.env.trace(`${this.objectId}: handles drained — resuming reclaim`);
    if (this.unsafeFastReclaim) this.forceReclaim();
    else this.doReclaim();
  }

  /** A local write created a new version; any in-flight migration is void. */
  onLocalUpdate(version: number): void {
    if (this.state !== MigState.LocalOnly) {
      this.env.trace(
        `${this.objectId}: UPDATE RACE — v${version} written during ${this.state}; ` +
          `aborting migration of v${this.migratingVersion}`,
      );
    }
    this.state = MigState.LocalOnly;
    this.receipt = null;
    this.reclaimed = false;
    this.kick();
  }
}
