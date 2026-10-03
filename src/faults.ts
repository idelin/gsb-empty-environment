/** Injection points for hostile-condition simulation. */
export type FaultHook =
  | 'wal.flush' // disk full while persisting WAL records
  | 'wal.powerLoss' // power cut mid-flush: torn WAL tail
  | 'storage.writePage' // disk full while writing a data page
  | 'storage.tornWrite' // power cut mid page write: torn page
  | 'channel.send' // network packet loss (data)
  | 'channel.ack' // network packet loss (ack)
  | 'recovery.undo'; // power cut in the middle of undo

export class DiskFullError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'DiskFullError';
  }
}

export class PowerLossError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PowerLossError';
  }
}

export class CrashError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CrashError';
  }
}

export class TornPageError extends Error {
  readonly pageId: number;
  constructor(pageId: number) {
    super(`page ${pageId} is torn (checksum mismatch)`);
    this.name = 'TornPageError';
    this.pageId = pageId;
  }
}

/**
 * Deterministic / seeded-probabilistic fault injector.
 * Scripted faults (`arm`) fire exactly the given number of times;
 * `assertNoPendingFaults` guarantees every scripted fault was exercised,
 * so tests cannot silently pass without hitting the fault path.
 */
export class FaultInjector {
  private readonly scripted = new Map<FaultHook, number>();
  private readonly probabilistic = new Map<FaultHook, number>();
  private rngState: number;
  /** Every fault that actually fired, in order (for strict assertions). */
  readonly fired: FaultHook[] = [];

  constructor(seed = 0x9e3779b9) {
    this.rngState = seed >>> 0 || 1;
  }

  private nextRandom(): number {
    let x = this.rngState;
    x ^= x << 13;
    x ^= x >>> 17;
    x ^= x << 5;
    this.rngState = x >>> 0 || 1;
    return this.rngState / 0x100000000;
  }

  arm(hook: FaultHook, times = 1): void {
    this.scripted.set(hook, (this.scripted.get(hook) ?? 0) + times);
  }

  armProbabilistic(hook: FaultHook, probability: number): void {
    this.probabilistic.set(hook, probability);
  }

  disarm(hook: FaultHook): void {
    this.scripted.delete(hook);
    this.probabilistic.delete(hook);
  }

  /** Returns true when a fault fires at this hook. */
  check(hook: FaultHook): boolean {
    const remaining = this.scripted.get(hook);
    if (remaining !== undefined && remaining > 0) {
      if (remaining === 1) this.scripted.delete(hook);
      else this.scripted.set(hook, remaining - 1);
      this.fired.push(hook);
      return true;
    }
    const p = this.probabilistic.get(hook);
    if (p !== undefined && this.nextRandom() < p) {
      this.fired.push(hook);
      return true;
    }
    return false;
  }

  pendingFaults(): number {
    let total = 0;
    for (const n of this.scripted.values()) total += n;
    return total;
  }

  assertNoPendingFaults(): void {
    const pending = [...this.scripted.entries()].filter(([, n]) => n > 0);
    if (pending.length > 0) {
      const desc = pending.map(([h, n]) => `${h} x${n}`).join(', ');
      throw new Error(`scripted faults never fired (test did not exercise them): ${desc}`);
    }
  }
}
