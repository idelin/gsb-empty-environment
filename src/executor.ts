import type { FileState, FileStore } from './store.ts';
import { ReceiptLostError, type PublishReceipt, type PublishRequest } from './types.ts';

export type Delay = number | ((req: PublishRequest) => number);

export interface ExecutorFaults {
  /** Fail the pre-commit version check. */
  check?: (req: PublishRequest) => boolean;
  /** Fail before the commit is applied (nothing is persisted). */
  beforeCommit?: (req: PublishRequest) => boolean;
  /** Apply the commit but lose the acknowledgement. */
  receipt?: (req: PublishRequest) => boolean;
}

export interface ExecutorDelays {
  check?: Delay;
  beforeCommit?: Delay;
  /** Delay after the commit is applied, before the receipt is delivered. */
  receipt?: Delay;
}

export interface ExecutorOptions {
  delays?: ExecutorDelays;
  faults?: ExecutorFaults;
  now?: () => number;
  sleep?: (ms: number) => Promise<void>;
}

const defaultSleep = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

/**
 * Wraps a FileStore with injectable latency and failure points so tests can
 * drive races, crashes and lost receipts deterministically.
 */
export class Executor {
  private readonly store: FileStore;
  private readonly delays: ExecutorDelays;
  private readonly faults: ExecutorFaults;
  private readonly now: () => number;
  private readonly sleep: (ms: number) => Promise<void>;

  constructor(store: FileStore, options: ExecutorOptions = {}) {
    this.store = store;
    this.delays = options.delays ?? {};
    this.faults = options.faults ?? {};
    this.now = options.now ?? (() => Date.now());
    this.sleep = options.sleep ?? defaultSleep;
  }

  async check(req: PublishRequest): Promise<FileState | undefined> {
    await this.sleepFor(this.delays.check, req);
    if (this.faults.check?.(req)) {
      throw new Error(`injected check failure for ${req.path}`);
    }
    return this.store.read(req.path);
  }

  /**
   * The confirmation point. Once the compare-and-swap returns, the publish is
   * committed and can no longer be cancelled — even if the receipt is then lost.
   */
  async commit(req: PublishRequest): Promise<PublishReceipt> {
    await this.sleepFor(this.delays.beforeCommit, req);
    if (this.faults.beforeCommit?.(req)) {
      throw new Error(`injected pre-commit failure for ${req.path}`);
    }
    const committed = await this.store.compareAndSwap(
      req.path,
      req.expectedVersion,
      req.digest,
    );
    await this.sleepFor(this.delays.receipt, req);
    if (this.faults.receipt?.(req)) {
      throw new ReceiptLostError(req.path);
    }
    return {
      path: req.path,
      digest: committed.digest,
      version: committed.version,
      committedAt: this.now(),
      idempotencyKey: req.idempotencyKey,
    };
  }

  private sleepFor(delay: Delay | undefined, req: PublishRequest): Promise<void> {
    if (delay === undefined) return Promise.resolve();
    const ms = typeof delay === 'function' ? delay(req) : delay;
    return ms > 0 ? this.sleep(ms) : Promise.resolve();
  }
}
