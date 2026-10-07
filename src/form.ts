export type Values = Record<string, unknown>;

export interface SubmitResponse {
  ok: boolean;
  fieldErrors?: Record<string, string>;
  message?: string;
}

export type Transport = (snapshot: Readonly<Values>) => Promise<SubmitResponse>;

export type SubmissionStatus = 'pending' | 'confirmed' | 'failed' | 'cancelled';

export interface SubmissionRecord {
  id: number;
  /** Frozen copy of the form values at submit time. */
  readonly snapshot: Readonly<Values>;
  status: SubmissionStatus;
  /** Server field errors that were applied to live fields. */
  readonly appliedErrors: Record<string, string>;
  /**
   * Server field errors kept only as a submission record, because the field
   * was edited or deleted after the snapshot was taken.
   */
  readonly unappliedErrors: Record<string, string>;
  /** Non-field error message from the server or transport. */
  message?: string;
  /** Resolves with this record once the submission reaches a final status. */
  readonly settled: Promise<SubmissionRecord>;
}

function isEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) {
    return false;
  }
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const aKeys = Object.keys(a as Values);
  const bKeys = Object.keys(b as Values);
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every(
    (key) =>
      Object.prototype.hasOwnProperty.call(b, key) &&
      isEqual((a as Values)[key], (b as Values)[key]),
  );
}

export class FormStore {
  private readonly transport: Transport;
  private readonly values: Values;
  private readonly confirmedValues: Values;
  private readonly fieldErrors: Record<string, string> = {};
  private readonly settlers = new Map<number, (record: SubmissionRecord) => void>();
  private nextId = 1;

  /** The in-flight submission, if any. */
  pending: SubmissionRecord | null = null;
  /** History of every submission, oldest first. */
  readonly submissions: SubmissionRecord[] = [];

  constructor(transport: Transport, initial: Values = {}) {
    this.transport = transport;
    this.values = { ...initial };
    this.confirmedValues = { ...initial };
  }

  getValue(key: string): unknown {
    return this.values[key];
  }

  hasField(key: string): boolean {
    return key in this.values;
  }

  currentValues(): Values {
    return { ...this.values };
  }

  confirmedValue(key: string): unknown {
    return this.confirmedValues[key];
  }

  setValue(key: string, value: unknown): void {
    this.values[key] = value;
    delete this.fieldErrors[key];
  }

  deleteField(key: string): void {
    delete this.values[key];
    delete this.fieldErrors[key];
  }

  isDirty(key?: string): boolean {
    if (key !== undefined) {
      if (!(key in this.values) && !(key in this.confirmedValues)) return false;
      return !isEqual(this.values[key], this.confirmedValues[key]);
    }
    const keys = new Set([...Object.keys(this.values), ...Object.keys(this.confirmedValues)]);
    for (const k of keys) {
      if (this.isDirty(k)) return true;
    }
    return false;
  }

  getError(key: string): string | undefined {
    return this.fieldErrors[key];
  }

  /**
   * Submits a frozen snapshot of the current values. While a submission is
   * in flight, additional calls are idempotent and return the same record.
   */
  submit(): SubmissionRecord {
    if (this.pending) return this.pending;

    const id = this.nextId++;
    const snapshot: Readonly<Values> = Object.freeze({ ...this.values });
    let settle!: (record: SubmissionRecord) => void;
    const settled = new Promise<SubmissionRecord>((resolve) => {
      settle = resolve;
    });
    const record: SubmissionRecord = {
      id,
      snapshot,
      status: 'pending',
      appliedErrors: {},
      unappliedErrors: {},
      settled,
    };
    this.settlers.set(id, settle);
    this.pending = record;
    this.submissions.push(record);

    this.transport(snapshot).then(
      (response) => {
        if (record.status === 'pending') this.applyResponse(record, response);
      },
      (error: unknown) => {
        if (record.status === 'pending') {
          record.status = 'failed';
          record.message = error instanceof Error ? error.message : String(error);
        }
      },
    ).finally(() => this.finish(record));

    return record;
  }

  /**
   * Cancels the in-flight submission. A late response for it is ignored and
   * confirms nothing. Returns false when nothing is pending.
   */
  cancel(): boolean {
    const record = this.pending;
    if (!record) return false;
    record.status = 'cancelled';
    this.finish(record);
    return true;
  }

  private finish(record: SubmissionRecord): void {
    if (this.pending === record) this.pending = null;
    const settle = this.settlers.get(record.id);
    if (settle) {
      this.settlers.delete(record.id);
      settle(record);
    }
  }

  private applyResponse(record: SubmissionRecord, response: SubmitResponse): void {
    if (response.ok) {
      record.status = 'confirmed';
      // Confirm only the submitted version: fields edited or deleted since
      // the snapshot keep their local state and stay dirty.
      for (const key of Object.keys(record.snapshot)) {
        if (key in this.values && isEqual(this.values[key], record.snapshot[key])) {
          this.confirmedValues[key] = record.snapshot[key];
          delete this.fieldErrors[key];
        }
      }
      return;
    }

    record.status = 'failed';
    record.message = response.message;
    for (const [key, error] of Object.entries(response.fieldErrors ?? {})) {
      const stillSubmittedValue =
        key in this.values &&
        key in record.snapshot &&
        isEqual(this.values[key], record.snapshot[key]);
      if (stillSubmittedValue) {
        this.fieldErrors[key] = error;
        record.appliedErrors[key] = error;
      } else {
        record.unappliedErrors[key] = error;
      }
    }
  }
}
