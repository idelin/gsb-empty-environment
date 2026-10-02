import { SchemaGraph } from './schema.ts';
import { TargetDatabase, encodeKey, type Row } from './target-db.ts';

export type CdcOperation = 'insert' | 'update' | 'delete';

export interface CdcEvent {
  id: string;
  table: string;
  op: CdcOperation;
  row?: Row;
  key?: string;
}

export interface AppliedRecord {
  eventId: string;
  table: string;
  op: CdcOperation;
  key: string;
  applyOrder: number;
}

export interface SuspendedRecord {
  eventId: string;
  table: string;
  op: CdcOperation;
  key: string;
  waitingFor: Array<{ table: string; key: string }>;
}

export interface ReplayReport {
  applied: AppliedRecord[];
  maxConcurrency: number;
}

export class ReplayDeadlockError extends Error {
  readonly suspended: SuspendedRecord[];

  constructor(suspended: SuspendedRecord[]) {
    const detail = suspended
      .map((s) => `${s.table}(${s.eventId}) waiting for ${s.waitingFor.map((w) => `${w.table}${w.key}`).join(', ')}`)
      .join('; ');
    super(`Replay deadlock: ${suspended.length} event(s) can never become ready: ${detail}`);
    this.name = 'ReplayDeadlockError';
    this.suspended = suspended;
  }
}

export interface SchedulerOptions {
  concurrency?: number;
  applyDelayMs?: number;
  onSuspend?: (record: SuspendedRecord) => void;
  onRelease?: (record: SuspendedRecord) => void;
}

interface PendingEvent {
  event: CdcEvent;
  key: string;
  rank: number;
  sequence: number;
  wasSuspended: boolean;
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

export class ReplayScheduler {
  private readonly graph: SchemaGraph;
  private readonly db: TargetDatabase;
  private readonly concurrency: number;
  private readonly applyDelayMs: number;
  private readonly onSuspend?: (record: SuspendedRecord) => void;
  private readonly onRelease?: (record: SuspendedRecord) => void;

  constructor(graph: SchemaGraph, db: TargetDatabase, options: SchedulerOptions = {}) {
    this.graph = graph;
    this.db = db;
    this.concurrency = Math.max(1, options.concurrency ?? 4);
    this.applyDelayMs = options.applyDelayMs ?? 0;
    this.onSuspend = options.onSuspend;
    this.onRelease = options.onRelease;
  }

  async replay(events: CdcEvent[]): Promise<ReplayReport> {
    const pending: PendingEvent[] = events.map((event, sequence) => this.wrap(event, sequence));
    const inFlight = new Set<Promise<void>>();
    const applied: AppliedRecord[] = [];
    const errors: unknown[] = [];
    let applyOrder = 0;
    let activeCount = 0;
    let maxConcurrency = 0;

    const dispatch = (item: PendingEvent): void => {
      activeCount += 1;
      maxConcurrency = Math.max(maxConcurrency, activeCount);
      const run = async (): Promise<void> => {
        try {
          await sleep(this.applyDelayMs);
          this.apply(item.event);
          applyOrder += 1;
          applied.push({
            eventId: item.event.id,
            table: item.event.table,
            op: item.event.op,
            key: item.key,
            applyOrder,
          });
        } catch (error) {
          errors.push(error);
        } finally {
          activeCount -= 1;
        }
      };
      const task = run().finally(() => {
        inFlight.delete(task);
      });
      inFlight.add(task);
    };

    while (pending.length > 0 || inFlight.size > 0) {
      let dispatchedAny = false;
      while (inFlight.size < this.concurrency) {
        pending.sort((a, b) => a.rank - b.rank || a.sequence - b.sequence);
        const index = pending.findIndex((item) => this.isReady(item.event));
        if (index === -1) {
          break;
        }
        const [item] = pending.splice(index, 1);
        if (item.wasSuspended && this.onRelease) {
          this.onRelease(this.describe(item.event));
        }
        dispatch(item);
        dispatchedAny = true;
      }
      if (errors.length > 0 && inFlight.size === 0) {
        throw errors[0];
      }
      if (pending.length > 0 && !dispatchedAny && inFlight.size === 0) {
        throw new ReplayDeadlockError(pending.map((item) => this.describe(item.event)));
      }
      for (const item of pending) {
        if (!item.wasSuspended) {
          item.wasSuspended = true;
          if (this.onSuspend) {
            this.onSuspend(this.describe(item.event));
          }
        }
      }
      if (inFlight.size > 0) {
        await Promise.race(inFlight);
      }
    }

    if (errors.length > 0) {
      throw errors[0];
    }
    applied.sort((a, b) => a.applyOrder - b.applyOrder);
    return { applied, maxConcurrency };
  }

  private wrap(event: CdcEvent, sequence: number): PendingEvent {
    const key = this.eventKey(event);
    const level = this.graph.insertLevel(event.table);
    const rank = event.op === 'delete' ? -level : level;
    return { event, key, rank, sequence, wasSuspended: false };
  }

  private eventKey(event: CdcEvent): string {
    if (event.op === 'delete') {
      if (event.key !== undefined) {
        return event.key;
      }
      if (event.row !== undefined) {
        return this.db.keyOf(event.table, event.row);
      }
      throw new Error(`Delete event ${event.id} on ${event.table} needs key or row`);
    }
    if (event.row === undefined) {
      throw new Error(`${event.op} event ${event.id} on ${event.table} needs row`);
    }
    return this.db.keyOf(event.table, event.row);
  }

  private isReady(event: CdcEvent): boolean {
    if (event.op === 'delete') {
      const key = this.eventKey(event);
      return this.db.childrenReferencing(event.table, key).length === 0;
    }
    return this.parentRefs(event).every((ref) => this.db.has(ref.table, ref.key));
  }

  private parentRefs(event: CdcEvent): Array<{ table: string; key: string }> {
    if (event.row === undefined) {
      return [];
    }
    const refs: Array<{ table: string; key: string }> = [];
    for (const fk of this.graph.foreignKeysOf(event.table)) {
      refs.push({
        table: fk.references.table,
        key: encodeKey(fk.columns.map((column) => event.row![column])),
      });
    }
    return refs;
  }

  private describe(event: CdcEvent): SuspendedRecord {
    const key = this.eventKey(event);
    const waitingFor =
      event.op === 'delete'
        ? this.db.childrenReferencing(event.table, key)
        : this.parentRefs(event).filter((ref) => !this.db.has(ref.table, ref.key));
    return { eventId: event.id, table: event.table, op: event.op, key, waitingFor };
  }

  private apply(event: CdcEvent): void {
    switch (event.op) {
      case 'insert':
        this.db.insert(event.table, event.row!);
        return;
      case 'update':
        this.db.update(event.table, event.row!);
        return;
      case 'delete':
        this.db.delete(event.table, this.eventKey(event));
        return;
    }
  }
}
