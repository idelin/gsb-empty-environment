import { deepClone, deepEqual } from "./deep-equal.ts";

export type FieldValues = Record<string, unknown>;

export type SubmissionResult =
  | { ok: true; confirmedValues?: FieldValues }
  | { ok: false; fieldErrors?: Record<string, string>; formErrors?: string[] };

export interface SubmitContext {
  submissionId: number;
  signal: AbortSignal;
}

export type Submitter = (
  snapshot: Readonly<FieldValues>,
  context: SubmitContext,
) => Promise<SubmissionResult>;

export type SubmissionStatus =
  | "pending"
  | "confirmed"
  | "rejected"
  | "cancelled"
  | "failed";

export interface SubmissionRecord {
  readonly id: number;
  /** Frozen copy of the field values at submit time. */
  readonly snapshot: Readonly<FieldValues>;
  status: SubmissionStatus;
  /** Field errors returned by the server, kept verbatim on the record. */
  fieldErrors: Record<string, string>;
  /** Names of fields whose live error was actually applied. */
  appliedFieldErrors: string[];
  /** Non-field errors, kept only on the record. */
  formErrors: string[];
  /** Transport/adapter error, when the submitter itself threw. */
  error?: unknown;
}

export interface SubmitHandle {
  id: number;
  promise: Promise<SubmissionRecord>;
}

export class SubmissionInFlightError extends Error {
  readonly pendingSubmissionId: number;

  constructor(pendingSubmissionId: number) {
    super(`submission #${pendingSubmissionId} is still in flight`);
    this.name = "SubmissionInFlightError";
    this.pendingSubmissionId = pendingSubmissionId;
  }
}

interface LiveFieldError {
  message: string;
  submissionId: number;
}

export interface FormStoreOptions {
  initialValues?: FieldValues;
  submitter: Submitter;
}

export class FormStore {
  #values: FieldValues;
  #baseValues: FieldValues;
  #liveErrors = new Map<string, LiveFieldError>();
  #submitter: Submitter;
  #submissions: SubmissionRecord[] = [];
  #nextSubmissionId = 1;
  #abortControllers = new Map<number, AbortController>();

  constructor(options: FormStoreOptions) {
    this.#values = deepClone(options.initialValues ?? {});
    this.#baseValues = deepClone(options.initialValues ?? {});
    this.#submitter = options.submitter;
  }

  /** Current editable values (shallow copy). */
  get values(): Readonly<FieldValues> {
    return { ...this.#values };
  }

  /** Last confirmed values; dirty is computed against these. */
  get baseValues(): Readonly<FieldValues> {
    return { ...this.#baseValues };
  }

  get submissions(): readonly SubmissionRecord[] {
    return [...this.#submissions];
  }

  get lastSubmission(): SubmissionRecord | undefined {
    return this.#submissions[this.#submissions.length - 1];
  }

  get pendingSubmission(): SubmissionRecord | undefined {
    return this.#submissions.find((s) => s.status === "pending");
  }

  get submitting(): boolean {
    return this.pendingSubmission !== undefined;
  }

  hasField(name: string): boolean {
    return Object.prototype.hasOwnProperty.call(this.#values, name);
  }

  getValue(name: string): unknown {
    return this.#values[name];
  }

  /** Live server error currently shown for a field, if any. */
  fieldError(name: string): string | undefined {
    return this.#liveErrors.get(name)?.message;
  }

  /** Existing fields whose current value differs from the base value. */
  get dirtyFields(): string[] {
    return Object.keys(this.#values).filter(
      (name) =>
        !Object.prototype.hasOwnProperty.call(this.#baseValues, name) ||
        !deepEqual(this.#values[name], this.#baseValues[name]),
    );
  }

  /** Base fields that have been deleted from the form. */
  get deletedFields(): string[] {
    return Object.keys(this.#baseValues).filter(
      (name) => !Object.prototype.hasOwnProperty.call(this.#values, name),
    );
  }

  get isDirty(): boolean {
    return this.dirtyFields.length > 0 || this.deletedFields.length > 0;
  }

  isFieldDirty(name: string): boolean {
    if (!this.hasField(name)) return false;
    return this.dirtyFields.includes(name);
  }

  setValue(name: string, value: unknown): void {
    this.#values[name] = value;
    // A user edit supersedes any server error shown on that field.
    this.#liveErrors.delete(name);
  }

  deleteField(name: string): void {
    delete this.#values[name];
    this.#liveErrors.delete(name);
  }

  /**
   * Freezes a snapshot of the current values and sends it to the submitter.
   * Only one submission may be in flight at a time.
   */
  submit(): SubmitHandle {
    const pending = this.pendingSubmission;
    if (pending) {
      throw new SubmissionInFlightError(pending.id);
    }
    const id = this.#nextSubmissionId++;
    const snapshot = Object.freeze(deepClone(this.#values));
    const record: SubmissionRecord = {
      id,
      snapshot,
      status: "pending",
      fieldErrors: {},
      appliedFieldErrors: [],
      formErrors: [],
    };
    this.#submissions.push(record);

    const controller = new AbortController();
    this.#abortControllers.set(id, controller);

    let outcome: Promise<SubmissionResult>;
    try {
      outcome = Promise.resolve(
        this.#submitter(snapshot, { submissionId: id, signal: controller.signal }),
      );
    } catch (error) {
      outcome = Promise.reject(error);
    }

    const promise = outcome
      .then(
        (result) => this.#settle(record, result),
        (error) => this.#fail(record, error),
      )
      .then(() => {
        this.#abortControllers.delete(id);
        return record;
      });

    return { id, promise };
  }

  /**
   * Cancels a pending submission (the in-flight one when no id is given).
   * A late response for a cancelled submission is ignored.
   */
  cancelSubmit(id?: number): boolean {
    const record =
      id === undefined
        ? this.pendingSubmission
        : this.#submissions.find((s) => s.id === id);
    if (!record || record.status !== "pending") return false;
    record.status = "cancelled";
    this.#abortControllers.get(record.id)?.abort();
    return true;
  }

  #settle(record: SubmissionRecord, result: SubmissionResult): void {
    if (record.status !== "pending") return; // cancelled meanwhile: ignore
    if (result.ok) {
      this.#confirm(record, result.confirmedValues);
    } else {
      this.#reject(record, result);
    }
  }

  #confirm(record: SubmissionRecord, confirmedValues?: FieldValues): void {
    record.status = "confirmed";
    const confirmed = confirmedValues ?? record.snapshot;
    // Only the confirmed version becomes the new base, and only for fields
    // that still exist. Edits made while in flight keep differing from the
    // new base, so they stay dirty.
    for (const name of Object.keys(confirmed)) {
      if (Object.prototype.hasOwnProperty.call(this.#values, name)) {
        this.#baseValues[name] = deepClone(confirmed[name]);
      }
    }
    for (const name of Object.keys(this.#baseValues)) {
      if (!Object.prototype.hasOwnProperty.call(this.#values, name)) {
        delete this.#baseValues[name];
      }
    }
    // Errors applied by this submission are resolved now.
    for (const [name, error] of this.#liveErrors) {
      if (error.submissionId === record.id) {
        this.#liveErrors.delete(name);
      }
    }
  }

  #reject(
    record: SubmissionRecord,
    result: { fieldErrors?: Record<string, string>; formErrors?: string[] },
  ): void {
    record.status = "rejected";
    record.fieldErrors = { ...result.fieldErrors };
    record.formErrors = [...(result.formErrors ?? [])];
    for (const [name, message] of Object.entries(result.fieldErrors ?? {})) {
      // Apply a server field error only when the field still exists and the
      // user has not changed it since the snapshot was taken. Otherwise the
      // error stays on the submission record only.
      if (!Object.prototype.hasOwnProperty.call(this.#values, name)) continue;
      if (!deepEqual(this.#values[name], record.snapshot[name])) continue;
      this.#liveErrors.set(name, { message, submissionId: record.id });
      record.appliedFieldErrors.push(name);
    }
  }

  #fail(record: SubmissionRecord, error: unknown): void {
    if (record.status !== "pending") return; // cancelled meanwhile: ignore
    if (error instanceof Error && error.name === "AbortError") {
      record.status = "cancelled";
      return;
    }
    record.status = "failed";
    record.error = error;
  }
}
