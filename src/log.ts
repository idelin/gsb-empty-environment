import { digestOf } from './hash.ts';
import { StateStore, type JsonValue, type Patch } from './state.ts';

export const GENESIS = 'GENESIS';

export type RecordType =
  | 'branch' | 'switch'
  | 'tx_begin' | 'tx_commit' | 'tx_abort'
  | 'op'
  | 'effect' | 'effect_ack'
  | 'checkpoint';

export interface LogRecord {
  seq: number;
  branch: string;
  opId: string;
  type: RecordType;
  payload: Record<string, unknown>;
  prevHash: string;
  hash: string;
}

export class LogCorruptionError extends Error {
  readonly seq: number;
  constructor(seq: number, detail: string) {
    super(`log corrupt at seq ${seq}: ${detail}`);
    this.name = 'LogCorruptionError';
    this.seq = seq;
  }
}

export class MissingOperationError extends Error {
  readonly seq: number;
  constructor(seq: number) {
    super(`missing operation at seq ${seq}; gaps cannot be skipped`);
    this.name = 'MissingOperationError';
    this.seq = seq;
  }
}

export class CheckpointSpliceError extends Error {
  constructor(detail: string) {
    super(`checkpoint splice rejected: ${detail}`);
    this.name = 'CheckpointSpliceError';
  }
}

export function computeHash(r: Omit<LogRecord, 'hash'>): string {
  return digestOf({
    seq: r.seq, branch: r.branch, opId: r.opId,
    type: r.type, payload: r.payload, prevHash: r.prevHash,
  });
}

export type SideEffectHandler = (effectId: string, action: JsonValue) => JsonValue;

/**
 * Builds an append-only, hash-chained operation log while applying the same
 * operations to a live StateStore (the "original run"). Inverse patches are
 * captured at append time so any prefix can be reverse-stepped later.
 */
export class LogBuilder {
  private records: LogRecord[] = [];
  private branch = 'main';
  private counter = 0;
  readonly state = new StateStore();
  private preview: StateStore | null = null; // committed + buffered tx ops
  private txId: string | null = null;
  private txPatches: Patch[] = []; // buffered forward patches of the open tx

  private nextOpId(): string {
    return `op-${this.counter++}`;
  }

  private append(type: RecordType, payload: Record<string, unknown>, opId?: string): LogRecord {
    const rec: LogRecord = {
      seq: this.records.length,
      branch: this.branch,
      opId: opId ?? this.nextOpId(),
      type,
      payload,
      prevHash: this.records.length === 0 ? GENESIS : this.records[this.records.length - 1].hash,
      hash: '',
    };
    rec.hash = computeHash(rec);
    this.records.push(rec);
    return rec;
  }

  createBranch(name: string): void {
    this.append('branch', { name });
  }

  switchBranch(name: string): void {
    this.branch = name;
    this.append('switch', { branch: name });
  }

  begin(txId: string): void {
    if (this.txId !== null) throw new Error('nested transactions are not supported');
    this.txId = txId;
    this.preview = StateStore.from(this.state.snapshot());
    this.txPatches = [];
    this.append('tx_begin', { txId });
  }

  commit(): void {
    if (this.txId === null) throw new Error('no open transaction');
    this.append('tx_commit', { txId: this.txId });
    for (const p of this.txPatches) this.state.apply(p);
    this.txId = null;
    this.preview = null;
    this.txPatches = [];
  }

  abort(): void {
    if (this.txId === null) throw new Error('no open transaction');
    this.append('tx_abort', { txId: this.txId });
    this.txId = null;
    this.preview = null;
    this.txPatches = [];
  }

  /** Append a state mutation. Inside a tx it is buffered (invisible to readers). */
  mutate(patch: Patch, opId?: string): void {
    const basis = this.preview ?? this.state;
    const inverse = basis.invert(patch);
    this.append('op', { patch, inverse, txId: this.txId }, opId);
    basis.apply(patch);
    if (this.txId !== null) this.txPatches.push(patch);
  }

  set(key: string, value: JsonValue, opId?: string): void {
    this.mutate([{ op: 'set', key, value }], opId);
  }

  del(key: string, opId?: string): void {
    this.mutate([{ op: 'del', key }], opId);
  }

  /**
   * External action: executes `handler` now and records the ack so that any
   * future replay sees the confirmation and never re-triggers the action.
   */
  effect(effectId: string, action: JsonValue, handler: SideEffectHandler): JsonValue {
    this.append('effect', { effectId, action });
    const receipt = handler(effectId, action);
    this.append('effect_ack', { effectId, receipt });
    return receipt;
  }

  /** Checkpoint bound to the current prefix digest and committed state digest. */
  checkpoint(label?: string): void {
    this.append('checkpoint', {
      label: label ?? null,
      stateDigest: this.state.digest(),
      prefixDigest: this.records[this.records.length - 1].hash,
    });
  }

  get length(): number {
    return this.records.length;
  }

  getRecords(): LogRecord[] {
    return this.records.map((r) => structuredClone(r));
  }

  serialize(): string {
    return this.records.map((r) => JSON.stringify(r)).join('\n') + '\n';
  }
}

/**
 * Parse and verify a serialized log. Hash chain and sequence contiguity are
 * checked record by record; the first failure reports its exact seq and the
 * parse fails as a whole — a corrupt record can never be silently skipped.
 */
export function parseAndVerify(serialized: string): LogRecord[] {
  const lines = serialized.split('\n').filter((l) => l.length > 0);
  const records: LogRecord[] = [];
  let prevHash = GENESIS;
  for (let i = 0; i < lines.length; i++) {
    let rec: LogRecord;
    try {
      rec = JSON.parse(lines[i]) as LogRecord;
    } catch {
      throw new LogCorruptionError(i, 'record is not valid JSON');
    }
    if (typeof rec.seq !== 'number' || typeof rec.hash !== 'string' ||
        typeof rec.prevHash !== 'string' || typeof rec.type !== 'string') {
      throw new LogCorruptionError(i, 'record is missing required fields');
    }
    if (rec.seq !== i) throw new MissingOperationError(i);
    if (rec.prevHash !== prevHash) {
      throw new LogCorruptionError(i, 'prevHash does not match chain');
    }
    if (computeHash(rec) !== rec.hash) {
      throw new LogCorruptionError(i, 'record hash mismatch');
    }
    records.push(rec);
    prevHash = rec.hash;
  }
  return records;
}
