/**
 * Durable transaction log (WAL) plus fault injection.
 *
 * The log itself models durable storage: it survives crashes.
 * Everything else in the system is volatile and rebuilt by recovery.
 */

export interface Notification {
  /** Deterministic, derived from the order event id -> stable across retries. */
  notificationId: string;
  eventId: string;
  orderId: string;
  amount: number;
}

export type LogRecord =
  | { kind: 'BEGIN'; txId: number }
  | { kind: 'ORDER_APPLY'; txId: number; orderId: string; amount: number }
  | { kind: 'EVENT_MARK'; txId: number; eventId: string }
  | { kind: 'OUTBOX_ADD'; txId: number; notification: Notification }
  | { kind: 'COMMIT'; txId: number }
  // Ack records are single-record appends, hence atomic; idempotent by id.
  | { kind: 'OUTBOX_ACK'; notificationId: string };

export class CrashError extends Error {
  constructor(public readonly durableRecords: number) {
    super(`simulated crash with ${durableRecords} durable log record(s)`);
    this.name = 'CrashError';
  }
}

/**
 * One-shot crash trigger. "Crash at boundary k" means: exactly k log records
 * are durable, the (k+1)-th append never reaches the log, and all volatile
 * state is lost. Enumerating k in 0..totalAppends covers every persistence
 * boundary of a run.
 */
export class FaultInjector {
  private crashAtDurableCount: number | null = null;

  armCrashWhenDurableCountReaches(n: number): void {
    this.crashAtDurableCount = n;
  }

  beforeAppend(durableCount: number): void {
    if (this.crashAtDurableCount !== null && durableCount === this.crashAtDurableCount) {
      this.crashAtDurableCount = null; // one-shot
      throw new CrashError(durableCount);
    }
  }
}

export class TransactionLog {
  private records: LogRecord[] = [];

  constructor(private readonly injector: FaultInjector) {}

  append(record: LogRecord): void {
    this.injector.beforeAppend(this.records.length);
    this.records.push(record);
  }

  get size(): number {
    return this.records.length;
  }

  replay(): readonly LogRecord[] {
    return this.records;
  }
}
