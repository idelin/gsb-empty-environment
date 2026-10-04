/**
 * Fault injectors for hostile-environment testing: sudden power loss
 * (torn writes, crashed processes), disk full (ENOSPC) and network
 * packet loss / duplication.  Injectors wrap the store's FileIO so the
 * production code paths under test stay unchanged.
 */

import { nodeFileIO } from './store.ts';
import type { FileIO } from './store.ts';

export function enospcError(): Error {
  const err = new Error('ENOSPC: no space left on device');
  (err as NodeJS.ErrnoException).code = 'ENOSPC';
  return err;
}

export function crashError(): Error {
  const err = new Error('ECRASH: simulated sudden power loss');
  (err as NodeJS.ErrnoException).code = 'ECRASH';
  return err;
}

type AppendPlan =
  | { kind: 'fail'; error: Error }
  | { kind: 'torn'; fraction: number; crash: boolean }
  | { kind: 'ok-then-crash' };

/**
 * FileIO wrapper with scriptable failures.  When `crashed` is set, every
 * subsequent IO operation fails with ECRASH until {@link reset} is
 * called (simulating a machine that lost power and is later rebooted).
 */
export class FaultInjector implements FileIO {
  private appendPlan: AppendPlan | null = null;
  private writePlan: { error: Error } | null = null;
  private crashed = false;

  private readonly inner: FileIO;

  constructor(inner: FileIO = nodeFileIO) {
    this.inner = inner;
  }

  /** Next appendFile fails with the given error (default ENOSPC). */
  failNextAppend(error: Error = enospcError()): void {
    this.appendPlan = { kind: 'fail', error };
  }

  /**
   * Next appendFile writes only a fraction of the bytes (torn write),
   * then the machine "loses power": all further IO throws ECRASH.
   */
  powerLossDuringAppend(fraction = 0.5): void {
    this.appendPlan = { kind: 'torn', fraction, crash: true };
  }

  /** Next appendFile succeeds, then the machine "loses power". */
  powerLossAfterAppend(): void {
    this.appendPlan = { kind: 'ok-then-crash' };
  }

  /** Next writeFile (e.g. checkpoint snapshot) fails (default ENOSPC). */
  failNextWrite(error: Error = enospcError()): void {
    this.writePlan = { error };
  }

  /** Simulate reboot: IO works again. */
  reset(): void {
    this.crashed = false;
    this.appendPlan = null;
    this.writePlan = null;
  }

  private checkCrash(): void {
    if (this.crashed) throw crashError();
  }

  async appendFile(p: string, data: string | Buffer): Promise<void> {
    this.checkCrash();
    const plan = this.appendPlan;
    this.appendPlan = null;
    if (plan?.kind === 'fail') throw plan.error;
    if (plan?.kind === 'torn') {
      const buf = Buffer.isBuffer(data) ? data : Buffer.from(data);
      const cut = Math.max(1, Math.floor(buf.length * plan.fraction));
      await this.inner.appendFile(p, buf.subarray(0, cut));
      if (plan.crash) this.crashed = true;
      throw crashError();
    }
    await this.inner.appendFile(p, data);
    if (plan?.kind === 'ok-then-crash') this.crashed = true;
  }

  async writeFile(p: string, data: string | Buffer): Promise<void> {
    this.checkCrash();
    const plan = this.writePlan;
    this.writePlan = null;
    if (plan) throw plan.error;
    return this.inner.writeFile(p, data);
  }

  readFile(p: string): Promise<Buffer> {
    this.checkCrash();
    return this.inner.readFile(p);
  }
  rename(a: string, b: string): Promise<void> {
    this.checkCrash();
    return this.inner.rename(a, b);
  }
  unlink(p: string): Promise<void> {
    this.checkCrash();
    return this.inner.unlink(p);
  }
  readdir(p: string): Promise<string[]> {
    this.checkCrash();
    return this.inner.readdir(p);
  }
  mkdir(p: string): Promise<void> {
    this.checkCrash();
    return this.inner.mkdir(p);
  }
  sync(p: string): Promise<void> {
    this.checkCrash();
    return this.inner.sync(p);
  }
  truncate(p: string, len: number): Promise<void> {
    this.checkCrash();
    return this.inner.truncate(p, len);
  }
  stat(p: string): Promise<{ size: number }> {
    this.checkCrash();
    return this.inner.stat(p);
  }
  exists(p: string): Promise<boolean> {
    this.checkCrash();
    return this.inner.exists(p);
  }
}

/* ------------------------------------------------------------------ */
/* deterministic RNG for reproducible packet loss                      */
/* ------------------------------------------------------------------ */

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface ChannelMessage {
  kind: string;
  [key: string]: unknown;
}

type MessageHandler = (msg: ChannelMessage) => void;

/**
 * Unreliable datagram channel: drops, duplicates and reorders messages
 * according to a seeded RNG (fully deterministic for tests).
 */
export class LossyChannel {
  dropRate: number;
  duplicateRate: number;
  private rng: () => number;
  private peer: MessageHandler = () => {};
  private queue: ChannelMessage[] = [];
  sent = 0;
  dropped = 0;
  duplicated = 0;

  constructor(opts: { dropRate?: number; duplicateRate?: number; seed?: number } = {}) {
    this.dropRate = opts.dropRate ?? 0.3;
    this.duplicateRate = opts.duplicateRate ?? 0.1;
    this.rng = mulberry32(opts.seed ?? 42);
  }

  connect(handler: MessageHandler): void {
    this.peer = handler;
  }

  send(msg: ChannelMessage): void {
    this.sent++;
    if (this.rng() < this.dropRate) {
      this.dropped++;
      return;
    }
    this.queue.push(msg);
    if (this.rng() < this.duplicateRate) {
      this.duplicated++;
      this.queue.push(msg);
    }
  }

  /** Deliver queued messages (in random order) to the peer. */
  pump(): void {
    const pending = this.queue;
    this.queue = [];
    // reorder deterministically
    for (let i = pending.length - 1; i > 0; i--) {
      const j = Math.floor(this.rng() * (i + 1));
      [pending[i], pending[j]] = [pending[j], pending[i]];
    }
    for (const msg of pending) this.peer(msg);
  }

  get pending(): number {
    return this.queue.length;
  }
}
