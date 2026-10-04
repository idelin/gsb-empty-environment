import { ColdStore, LocalStore, copyValid } from './store';
import type { Receipt } from './store';
import { Journal } from './journal';
import type { Decision } from './journal';
import { Migrator, MigState } from './migrator';
import type { Scheduler } from './migrator';
import { chance, mulberry32, randInt } from './prng';
import type { Prng } from './prng';

export interface FaultConfig {
  loseReceiptProb: number; // upload receipt lost in transit
  replayOldReceipts: boolean; // replay last receipt after an update (stale)
  corruptAfterUploadProb: number; // target bit-rot between upload and verify
  corruptAfterDecisionProb: number; // target bit-rot between decision and reclaim
  journalFlushFailProb: number; // decision flush fails (crash before fsync)
}

export const noFaults: FaultConfig = {
  loseReceiptProb: 0,
  replayOldReceipts: false,
  corruptAfterUploadProb: 0,
  corruptAfterDecisionProb: 0,
  journalFlushFailProb: 0,
};

export interface WorldConfig {
  seed: number;
  faults: FaultConfig;
  uploadLatency: number;
  receiptLatency: number;
  receiptTimeout: number;
  verifyDelay: number;
  reclaimPoll: number;
  retryBackoff: number;
  maxRounds: number;
  verbose?: boolean;
}

export const defaultTiming = {
  uploadLatency: 5,
  receiptLatency: 2,
  receiptTimeout: 8,
  verifyDelay: 1,
  reclaimPoll: 3,
  retryBackoff: 6,
  maxRounds: 6,
};

export interface Tracked {
  migrator: Migrator | null;
  latestVersion: number;
  latestDigest: string;
  abortReasons: string[];
  reclaimTime: number | null;
  lastReceipt: Receipt | null;
}

interface SimEvent {
  t: number;
  seq: number;
  fn: () => void;
}

// Discrete-event simulator: owns both stores, the decision journal, fault
// injection, and checks the safety invariant after every single event:
// at least one valid copy of the latest version must exist at all times.
export class World implements Scheduler {
  now = 0;
  readonly local = new LocalStore();
  readonly cold = new ColdStore();
  readonly journal = new Journal();
  readonly rng: Prng;
  readonly logs: string[] = [];
  readonly objects = new Map<string, Tracked>();
  readonly onDecisionHooks: Array<(d: Decision, m: Migrator) => void> = [];
  private queue: SimEvent[] = [];
  private seq = 0;

  constructor(readonly cfg: WorldConfig) {
    this.rng = mulberry32(cfg.seed);
  }

  get faults(): FaultConfig {
    return this.cfg.faults;
  }

  log(msg: string): void {
    this.logs.push(msg);
    if (this.cfg.verbose) console.log(msg);
  }

  after(delay: number, fn: () => void): void {
    this.queue.push({ t: this.now + delay, seq: this.seq++, fn });
  }

  track(objectId: string): Tracked {
    let tr = this.objects.get(objectId);
    if (!tr) {
      tr = {
        migrator: null,
        latestVersion: 0,
        latestDigest: '',
        abortReasons: [],
        reclaimTime: null,
        lastReceipt: null,
      };
      this.objects.set(objectId, tr);
    }
    return tr;
  }

  // All writes go through here so the invariant checker knows the latest version.
  writeObject(objectId: string, bytes: string): void {
    const copy = this.local.write(objectId, bytes);
    const tr = this.track(objectId);
    tr.latestVersion = copy.version;
    tr.latestDigest = copy.digest;
    this.log(`[t=${this.now}] ${objectId} updated to v${copy.version} (${copy.digest})`);
    if (this.faults.replayOldReceipts && tr.lastReceipt && tr.lastReceipt.version < copy.version) {
      const stale = tr.lastReceipt;
      this.after(1, () => {
        this.log(`[t=${this.now}] ${objectId} replaying old receipt v${stale.version} against v${copy.version}`);
        tr.migrator?.deliverReceipt(stale);
      });
    }
  }

  migrate(objectId: string): Migrator {
    const tr = this.track(objectId);
    const migrator = new Migrator(objectId, {
      local: this.local,
      cold: this.cold,
      journal: this.journal,
      scheduler: this,
      config: {
        uploadLatency: this.cfg.uploadLatency,
        receiptTimeout: this.cfg.receiptTimeout,
        verifyDelay: this.cfg.verifyDelay,
        reclaimPoll: this.cfg.reclaimPoll,
      },
      log: (msg) => this.log(msg),
      onUploadAck: (receipt, m) => this.handleUploadAck(receipt, m),
      onDecision: (decision, m) => {
        for (const hook of this.onDecisionHooks) hook(decision, m);
        if (chance(this.rng, this.faults.corruptAfterDecisionProb)) {
          this.after(1, () => {
            if (this.cold.corrupt(objectId, this.rng)) {
              this.log(`[t=${this.now}] ${objectId} FAULT: target corrupted after decision`);
            }
          });
        }
      },
      onSettled: (m) => this.handleSettled(m),
    });
    tr.migrator = migrator;
    migrator.start();
    return migrator;
  }

  private handleUploadAck(receipt: Receipt, migrator: Migrator): void {
    const tr = this.track(receipt.objectId);
    tr.lastReceipt = receipt;
    if (chance(this.rng, this.faults.loseReceiptProb)) {
      this.log(`[t=${this.now}] ${receipt.objectId} FAULT: receipt v${receipt.version} lost in transit`);
      return;
    }
    this.after(this.cfg.receiptLatency, () => migrator.deliverReceipt(receipt));
    if (chance(this.rng, this.faults.corruptAfterUploadProb)) {
      this.after(this.cfg.receiptLatency / 2, () => {
        if (this.cold.corrupt(receipt.objectId, this.rng)) {
          this.log(`[t=${this.now}] ${receipt.objectId} FAULT: target corrupted after upload`);
        }
      });
    }
    if (chance(this.rng, this.faults.journalFlushFailProb)) {
      this.journal.failNextFlush = true;
      this.log(`[t=${this.now}] ${receipt.objectId} FAULT: next journal flush will fail`);
    }
  }

  private handleSettled(migrator: Migrator): void {
    const tr = this.track(migrator.objectId);
    if (migrator.state === MigState.Reclaimed) {
      tr.reclaimTime = this.now;
      return;
    }
    tr.abortReasons.push(migrator.abortReason ?? 'unknown');
    if (migrator.round < this.cfg.maxRounds && this.local.read(migrator.objectId)) {
      this.log(`[t=${this.now}] ${migrator.objectId} scheduling retry (round ${migrator.round + 1})`);
      this.after(this.cfg.retryBackoff, () => migrator.start());
    }
  }

  // A reader pins the local copy for `hold` time units.
  openReader(objectId: string, hold: number): boolean {
    if (!this.local.read(objectId)) return false;
    this.local.openHandle(objectId);
    this.log(`[t=${this.now}] ${objectId} reader opened handle (hold=${hold})`);
    this.after(hold, () => {
      this.local.closeHandle(objectId);
      this.log(`[t=${this.now}] ${objectId} reader closed handle`);
    });
    return true;
  }

  private assert(cond: boolean, msg: string): void {
    if (!cond) throw new Error(`INVARIANT VIOLATED at t=${this.now}: ${msg}`);
  }

  // Safety invariant, checked after every event:
  //  - at least one valid copy of the latest version exists (local or remote);
  //  - if the local copy is gone, a persisted decision for the latest
  //    version+digest must exist and the remote copy must be valid;
  //  - open handles imply the local copy still exists.
  checkInvariants(): void {
    for (const [id, tr] of this.objects) {
      const local = this.local.read(id);
      const remote = this.cold.read(id);
      const localValid = !!local && copyValid(local) && local.version === tr.latestVersion;
      const remoteValid = !!remote && copyValid(remote) && remote.version === tr.latestVersion;
      this.assert(
        localValid || remoteValid,
        `${id}: no valid copy of latest version v${tr.latestVersion}`,
      );
      if (!local) {
        this.assert(
          !!this.journal.find(id, tr.latestVersion, tr.latestDigest),
          `${id}: local reclaimed without a persisted decision for v${tr.latestVersion}`,
        );
        this.assert(remoteValid, `${id}: local reclaimed but remote copy invalid`);
      }
      if (this.local.openHandles(id) > 0) {
        this.assert(!!local, `${id}: open handles but local copy is gone`);
      }
    }
  }

  run(maxT = 5000): void {
    while (this.queue.length > 0) {
      let best = 0;
      for (let i = 1; i < this.queue.length; i++) {
        const e = this.queue[i];
        const b = this.queue[best];
        if (e.t < b.t || (e.t === b.t && e.seq < b.seq)) best = i;
      }
      const [ev] = this.queue.splice(best, 1);
      if (ev.t > maxT) break;
      this.now = ev.t;
      ev.fn();
      this.checkInvariants();
    }
    this.checkInvariants();
  }

  randomBytes(len: number): string {
    let out = '';
    for (let i = 0; i < len; i++) out += String.fromCharCode(97 + randInt(this.rng, 0, 25));
    return out;
  }
}
