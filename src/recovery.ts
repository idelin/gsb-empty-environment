import { digestOf } from './hash.ts';
import { StateStore, type JsonValue, type Patch } from './state.ts';
import {
  CheckpointSpliceError,
  GENESIS,
  parseAndVerify,
  type LogRecord,
  type RecordType,
  type SideEffectHandler,
} from './log.ts';

export class RecoveryCancelledError extends Error {
  constructor() {
    super('recovery session was cancelled; call reset() before stepping again');
    this.name = 'RecoveryCancelledError';
  }
}

export interface Checkpoint {
  /** Index of the last applied record (-1 = empty prefix). */
  seq: number;
  /** Digest of the committed state at `seq`. */
  stateDigest: string;
  /** Hash-chain digest of the log prefix ending at `seq`. */
  prefixDigest: string;
  /** Frozen committed-state snapshot at `seq`. */
  snapshot: Record<string, JsonValue>;
  label: string | null;
}

export type StepOutcome =
  | { done: true }
  | { done: false; seq: number; type: RecordType; note?: string };

const noopHandler: SideEffectHandler = () => null;

/**
 * Incremental recovery engine. Replays a verified operation log record by
 * record (step), supports reverse stepping via stored inverse patches
 * (rollback), aborting the session (cancel), and checkpoint save/restore
 * bound to prefix digests with reclamation of superseded checkpoints.
 *
 * Readers only ever see committed state: getState() exposes a frozen
 * snapshot that never includes buffered (uncommitted) transaction ops.
 */
export class RecoveryEngine {
  private records: LogRecord[] = [];
  private cursor = -1;
  private committed = new StateStore();
  private txBuffer: { txId: string; patches: Patch[]; inverses: Patch[] } | null = null;
  private undoStack: Array<() => void> = [];
  private seen = new Set<string>();
  /** effectId -> receipt; confirmed acks from the log plus session receipts. */
  private acks = new Map<string, JsonValue>();
  private executedEffects: string[] = [];
  private checkpoints = new Map<string, Checkpoint>();
  private handler: SideEffectHandler;
  private cancelled = false;
  private baseline: Record<string, JsonValue> = {};

  constructor(opts?: { handler?: SideEffectHandler }) {
    this.handler = opts?.handler ?? noopHandler;
  }

  /** Parse, verify and load a serialized log. Throws LogCorruptionError /
   *  MissingOperationError carrying the exact seq of the first bad record. */
  load(serialized: string): number {
    this.records = parseAndVerify(serialized);
    this.cursor = -1;
    this.committed = new StateStore();
    this.txBuffer = null;
    this.undoStack = [];
    this.seen.clear();
    this.executedEffects = [];
    this.cancelled = false;
    this.baseline = {};
    // Pre-scan confirmations: an acked external action is never re-triggered,
    // no matter where the ack sits relative to the replay cursor.
    this.acks = new Map();
    for (const rec of this.records) {
      if (rec.type === 'effect_ack') {
        this.acks.set(rec.payload.effectId as string, rec.payload.receipt as JsonValue);
      }
    }
    return this.records.length;
  }

  get recordCount(): number {
    return this.records.length;
  }

  get cursorSeq(): number {
    return this.cursor;
  }

  get checkpointCount(): number {
    return this.checkpoints.size;
  }

  get effectsExecutedByRecovery(): readonly string[] {
    return this.executedEffects;
  }

  private assertActive(): void {
    if (this.cancelled) throw new RecoveryCancelledError();
  }

  /** Apply exactly one record. */
  step(): StepOutcome {
    this.assertActive();
    if (this.cursor + 1 >= this.records.length) return { done: true };
    const rec = this.records[this.cursor + 1];
    let note: string | undefined;

    switch (rec.type) {
      case 'op': {
        if (this.seen.has(rec.opId)) {
          note = `duplicate opId ${rec.opId}: skipped`;
          this.undoStack.push(() => {});
          break;
        }
        this.seen.add(rec.opId);
        const patch = rec.payload.patch as Patch;
        const inverse = rec.payload.inverse as Patch;
        if (this.txBuffer) {
          const buf = this.txBuffer;
          buf.patches.push(patch);
          buf.inverses.push(inverse);
          this.undoStack.push(() => {
            buf.patches.pop();
            buf.inverses.pop();
            this.seen.delete(rec.opId);
          });
        } else {
          this.committed.apply(patch);
          this.undoStack.push(() => {
            this.committed.apply(inverse);
            this.seen.delete(rec.opId);
          });
        }
        break;
      }
      case 'tx_begin': {
        this.txBuffer = { txId: rec.payload.txId as string, patches: [], inverses: [] };
        this.undoStack.push(() => {
          this.txBuffer = null;
        });
        break;
      }
      case 'tx_commit': {
        const buf = this.txBuffer!;
        for (const p of buf.patches) this.committed.apply(p);
        const inverses = [...buf.inverses];
        this.undoStack.push(() => {
          for (let i = inverses.length - 1; i >= 0; i--) this.committed.apply(inverses[i]);
          this.txBuffer = buf;
        });
        this.txBuffer = null;
        break;
      }
      case 'tx_abort': {
        const buf = this.txBuffer!;
        this.undoStack.push(() => {
          this.txBuffer = buf;
        });
        this.txBuffer = null;
        note = `transaction ${buf.txId} aborted: buffered ops discarded`;
        break;
      }
      case 'effect': {
        const effectId = rec.payload.effectId as string;
        if (this.acks.has(effectId)) {
          note = `effect ${effectId} already confirmed: not re-triggered`;
          this.undoStack.push(() => {});
        } else {
          const receipt = this.handler(effectId, rec.payload.action as JsonValue);
          this.acks.set(effectId, receipt);
          this.executedEffects.push(effectId);
          note = `effect ${effectId} executed (no ack in log)`;
          // The external world cannot be un-executed; rollback only detaches
          // the local marker so a re-step consults the ack table again.
          this.undoStack.push(() => {});
        }
        break;
      }
      case 'checkpoint': {
        const expectedPrefix = this.cursor >= 0 ? this.records[this.cursor].hash : GENESIS;
        if (rec.payload.prefixDigest !== expectedPrefix) {
          throw new CheckpointSpliceError(
            `checkpoint record at seq ${rec.seq} binds prefix ${rec.payload.prefixDigest}, expected ${expectedPrefix}`,
          );
        }
        if (rec.payload.stateDigest !== this.committed.digest()) {
          throw new CheckpointSpliceError(
            `checkpoint record at seq ${rec.seq} binds foreign state digest`,
          );
        }
        this.undoStack.push(() => {});
        break;
      }
      default: // branch, switch, effect_ack: no committed-state change
        this.undoStack.push(() => {});
        break;
    }

    this.cursor++;
    return { done: false, seq: rec.seq, type: rec.type, note };
  }

  /** Reverse-step exactly one record using stored inverse patches. */
  rollback(): StepOutcome {
    this.assertActive();
    if (this.cursor < 0) return { done: true };
    const rec = this.records[this.cursor];
    const undo = this.undoStack.pop()!;
    undo();
    this.cursor--;
    return { done: false, seq: rec.seq, type: rec.type };
  }

  /** Abort the recovery session: state returns to its pre-recovery baseline
   *  and further stepping is rejected until reset(). */
  cancel(): void {
    this.committed = StateStore.from(this.baseline);
    this.txBuffer = null;
    this.undoStack = [];
    this.seen.clear();
    this.cursor = -1;
    this.cancelled = true;
  }

  reset(): void {
    this.cancelled = false;
  }

  runToEnd(): void {
    while (!this.step().done) { /* step to end */ }
  }

  /** Capture a checkpoint bound to the current prefix digest. */
  checkpoint(label?: string): Checkpoint {
    this.assertActive();
    const cp: Checkpoint = {
      seq: this.cursor,
      stateDigest: this.committed.digest(),
      prefixDigest: this.cursor >= 0 ? this.records[this.cursor].hash : GENESIS,
      snapshot: this.committed.snapshot(),
      label: label ?? null,
    };
    this.checkpoints.set(`${cp.seq}:${cp.prefixDigest}`, cp);
    return cp;
  }

  /**
   * Restore from a checkpoint. The checkpoint's prefix digest must match the
   * hash chain of the loaded log at the same seq, and its snapshot must match
   * its state digest — otherwise the splice is rejected.
   */
  restore(cp: Checkpoint): void {
    this.assertActive();
    if (cp.seq < -1 || cp.seq >= this.records.length) {
      throw new CheckpointSpliceError(`seq ${cp.seq} is outside the loaded log`);
    }
    const expectedPrefix = cp.seq >= 0 ? this.records[cp.seq].hash : GENESIS;
    if (cp.prefixDigest !== expectedPrefix) {
      throw new CheckpointSpliceError(
        `prefix digest mismatch at seq ${cp.seq}: checkpoint belongs to a different log prefix`,
      );
    }
    if (digestOf(cp.snapshot) !== cp.stateDigest) {
      throw new CheckpointSpliceError('snapshot does not match its state digest');
    }
    // Rebuild position and undo history by re-stepping the prefix, then
    // prove the rebuilt state equals the checkpoint's bound snapshot.
    this.committed = new StateStore();
    this.txBuffer = null;
    this.undoStack = [];
    this.seen.clear();
    this.cursor = -1;
    while (this.cursor < cp.seq) this.step();
    if (this.committed.digest() !== cp.stateDigest) {
      throw new CheckpointSpliceError('replayed prefix does not match checkpoint state digest');
    }
  }

  /** Reclaim all but the newest checkpoint. Returns the reclaimed count. */
  reclaimCheckpoints(): number {
    const all = [...this.checkpoints.entries()].sort((a, b) => {
      const [seqA] = a[0].split(':');
      const [seqB] = b[0].split(':');
      return Number(seqA) - Number(seqB);
    });
    let reclaimed = 0;
    for (let i = 0; i < all.length - 1; i++) {
      this.checkpoints.delete(all[i][0]);
      reclaimed++;
    }
    return reclaimed;
  }

  /** Frozen committed state. Uncommitted transaction ops are never visible. */
  getState(): Record<string, JsonValue> {
    return this.committed.snapshot();
  }

  digest(): string {
    return this.committed.digest();
  }
}
