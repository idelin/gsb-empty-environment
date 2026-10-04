import { LocalStore, RemoteStore } from './stores.ts';
import { DecisionLog } from './decisionLog.ts';
import { Migrator } from './migrator.ts';
import type { MigrationEnv } from './migrator.ts';
import { digestOf } from './digest.ts';
import { mulberry32 } from './prng.ts';
import type { Decision } from './types.ts';

export interface SimOptions {
  seed: number;
  dataDir: string;
  /** Probability that an upload receipt is lost in transit. */
  receiptLossProb?: number;
  /** Naive policy: reclaim on receipt alone (used to demonstrate data loss). */
  unsafe?: boolean;
  verbose?: boolean;
}

interface SimEvent {
  t: number;
  seq: number;
  name: string;
  run: () => void;
}

const UPLOAD_LATENCY = 5;
const RECEIPT_LATENCY = 2;
const READBACK_LATENCY = 3;
const PERSIST_LATENCY = 1;

/**
 * Discrete-event simulator. Owns both stores, injects faults, and checks the
 * safety invariant after every single event:
 *
 *   INVARIANT: for every object, at least one VALID copy of the current
 *   version exists — locally, or at the target (digest matches ground truth).
 */
export class Simulator {
  now = 0;

  readonly local = new LocalStore();
  readonly remote = new RemoteStore();
  readonly decisions: DecisionLog;
  readonly migrators = new Map<string, Migrator>();
  /** Ground truth: latest version + expected digest per object. */
  readonly truth = new Map<string, { version: number; digest: string }>();
  readonly violations: string[] = [];
  readonly traceLog: string[] = [];
  readonly env: MigrationEnv;

  private readonly opts: SimOptions;
  private readonly rng: () => number;
  private q: SimEvent[] = [];
  private seq = 0;

  constructor(opts: SimOptions) {
    this.opts = opts;
    this.decisions = new DecisionLog(opts.dataDir);
    this.rng = mulberry32(opts.seed);
    this.env = {
      upload: (id, version, bytes) => this.envUpload(id, version, bytes),
      readBack: (id, seq) => this.envReadBack(id, seq),
      persistDecision: (d) => this.envPersistDecision(d),
      after: (delay, name, cb) => this.schedule(delay, name, cb),
      clock: () => this.now,
      trace: (msg) => this.trace(msg),
    };
  }

  // --- MigrationEnv implementation (async edges + fault injection) ---

  private envUpload(objectId: string, version: number, bytes: string): void {
    this.schedule(UPLOAD_LATENCY, 'upload', () => {
      this.remote.put(objectId, version, bytes);
      this.migrators.get(objectId)?.onUploadApplied(version);
      const receipt = { objectId, version, digest: digestOf(bytes), issuedAt: this.now };
      if (this.rng() < (this.opts.receiptLossProb ?? 0)) {
        this.trace(`receipt for ${objectId} v${version} LOST in transit`);
        return;
      }
      this.schedule(RECEIPT_LATENCY, 'receipt', () => {
        this.migrators.get(objectId)?.onReceipt(receipt);
      });
    });
  }

  private envReadBack(objectId: string, seq: number): void {
    this.schedule(READBACK_LATENCY, 'readback', () => {
      this.migrators.get(objectId)?.onReadBack(seq, this.remote.digest(objectId));
    });
  }

  private envPersistDecision(decision: Decision): void {
    this.schedule(PERSIST_LATENCY, 'persist', () => {
      this.decisions.persist(decision); // durable before the migrator is notified
      this.migrators.get(decision.objectId)?.onDecisionPersisted(decision);
    });
  }

  // --- World actions ---

  writeLocal(id: string, bytes: string): void {
    const version = this.local.write(id, bytes);
    this.truth.set(id, { version, digest: digestOf(bytes) });
    this.trace(`${id}: local write -> v${version}`);
    this.migratorFor(id).onLocalUpdate(version);
  }

  acquireHandle(id: string): boolean {
    const ok = this.local.acquire(id);
    if (ok) this.trace(`${id}: read handle acquired (${this.local.handleCount(id)} open)`);
    return ok;
  }

  releaseHandle(id: string): void {
    this.local.release(id);
    this.trace(`${id}: read handle released (${this.local.handleCount(id)} open)`);
    this.migrators.get(id)?.onHandlesDrained();
  }

  corruptRemote(id: string): void {
    if (this.remote.corrupt(id)) this.trace(`${id}: TARGET CORRUPTED (bit flip)`);
  }

  migratorFor(id: string): Migrator {
    let m = this.migrators.get(id);
    if (!m) {
      m = new Migrator(id, this.env, this.local, this.decisions, this.opts.unsafe ?? false);
      this.migrators.set(id, m);
    }
    return m;
  }

  // --- Event loop ---

  schedule(delay: number, name: string, run: () => void): void {
    this.q.push({ t: this.now + delay, seq: this.seq++, name, run });
  }

  scheduleAt(t: number, name: string, run: () => void): void {
    this.q.push({ t, seq: this.seq++, name, run });
  }

  step(): boolean {
    if (this.q.length === 0) return false;
    let idx = 0;
    for (let i = 1; i < this.q.length; i++) {
      const a = this.q[i];
      const b = this.q[idx];
      if (a.t < b.t || (a.t === b.t && a.seq < b.seq)) idx = i;
    }
    const [ev] = this.q.splice(idx, 1);
    this.now = ev.t;
    ev.run();
    this.checkInvariant();
    return true;
  }

  run(maxSteps = 100_000): void {
    let n = 0;
    while (n++ < maxSteps && this.step()) {
      /* drain */
    }
  }

  trace(msg: string): void {
    const line = `[t=${String(this.now).padStart(4)}] ${msg}`;
    this.traceLog.push(line);
    if (this.opts.verbose) console.log(line);
  }

  /** Checked after every event: at least one valid copy of each object. */
  private checkInvariant(): void {
    for (const [id, t] of this.truth) {
      const localOk = this.local.digest(id) === t.digest;
      const remoteOk = this.remote.digest(id) === t.digest;
      if (!localOk && !remoteOk) {
        const msg =
          `INVARIANT VIOLATION @t=${this.now}: ${id} v${t.version} has NO valid copy ` +
          `(local=${this.local.digest(id) ?? 'none'}, remote=${this.remote.digest(id) ?? 'none'}, want=${t.digest})`;
        this.violations.push(msg);
        this.trace(msg);
      }
    }
  }
}
