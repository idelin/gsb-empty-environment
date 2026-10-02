import type { Rule, ValidationContext, Values } from "./rules.ts";

export interface FieldSchema {
  /** Initial value, defaults to "" (checkboxes typically use false). */
  initial?: unknown;
  rules?: readonly Rule[];
}

export type FormSchema = Record<string, FieldSchema>;

export interface FieldState {
  readonly value: unknown;
  /** Error messages from the currently failing rules (empty when valid). */
  readonly errors: readonly string[];
  readonly valid: boolean;
  /** True once the field has been set at least once. */
  readonly dirty: boolean;
  /** True after explicit user interaction or a full-form validation. */
  readonly touched: boolean;
}

export interface FieldChangeEvent {
  type: "field";
  field: string;
  state: FieldState;
}

export interface FormChangeEvent {
  type: "form";
  valid: boolean;
  errors: Record<string, readonly string[]>;
}

export type FormEvent = FieldChangeEvent | FormChangeEvent;
export type FormListener = (event: FormEvent) => void;

interface InternalField {
  value: unknown;
  errors: string[];
  dirty: boolean;
  touched: boolean;
}

/**
 * Reactive form validation engine.
 *
 * - Builds a dependency graph from each rule's `dependsOn` (including the
 *   fields read by dynamic `when` predicates).
 * - `setValue()` re-validates the edited field AND every field that depends
 *   on it, so cross-field rules (confirm password, conditional required, ...)
 *   stay in sync instantly.
 * - `validateAll()` performs a one-shot full-form validation.
 * - `subscribe()` streams per-field and per-form change events.
 */
export class FormValidator {
  private readonly schema: FormSchema;
  private readonly fields = new Map<string, InternalField>();
  /** field -> set of fields that must be re-validated when it changes. */
  private readonly dependents = new Map<string, Set<string>>();
  private readonly listeners = new Set<FormListener>();

  constructor(schema: FormSchema) {
    this.schema = schema;
    for (const [name, fieldSchema] of Object.entries(schema)) {
      this.fields.set(name, {
        value: fieldSchema.initial ?? "",
        errors: [],
        dirty: false,
        touched: false,
      });
    }
    this.buildDependencyGraph();
    // Initial pass so dynamic rules (e.g. checkbox defaults) are reflected.
    for (const name of this.fields.keys()) {
      this.validateField(name);
    }
  }

  private buildDependencyGraph(): void {
    for (const [name, fieldSchema] of Object.entries(this.schema)) {
      for (const rule of fieldSchema.rules ?? []) {
        for (const dep of rule.dependsOn ?? []) {
          if (dep === name) continue;
          if (!this.fields.has(dep)) {
            throw new Error(
              `Field "${name}" depends on unknown field "${dep}"`,
            );
          }
          let bucket = this.dependents.get(dep);
          if (!bucket) {
            bucket = new Set();
            this.dependents.set(dep, bucket);
          }
          bucket.add(name);
        }
      }
    }
  }

  /** Names of fields transitively affected when `field` changes. */
  private affectedBy(field: string): string[] {
    const seen = new Set<string>();
    const queue = [field];
    while (queue.length > 0) {
      const current = queue.shift() as string;
      for (const next of this.dependents.get(current) ?? []) {
        if (!seen.has(next)) {
          seen.add(next);
          queue.push(next);
        }
      }
    }
    return [...seen];
  }

  private validateField(name: string): boolean {
    const field = this.fields.get(name);
    if (!field) throw new Error(`Unknown field "${name}"`);
    const ctx: ValidationContext = { values: this.getValues(), field: name };
    const errors: string[] = [];
    for (const rule of this.schema[name].rules ?? []) {
      if (rule.when && !rule.when(ctx.values)) continue;
      const error = rule.validate(field.value, ctx);
      if (error !== null) errors.push(error);
    }
    const changed =
      errors.length !== field.errors.length ||
      errors.some((message, index) => message !== field.errors[index]);
    field.errors = errors;
    return changed;
  }

  private snapshot(name: string): FieldState {
    const field = this.fields.get(name);
    if (!field) throw new Error(`Unknown field "${name}"`);
    return {
      value: field.value,
      errors: [...field.errors],
      valid: field.errors.length === 0,
      dirty: field.dirty,
      touched: field.touched,
    };
  }

  private emit(event: FormEvent): void {
    for (const listener of this.listeners) listener(event);
  }

  private emitForm(): void {
    this.emit({ type: "form", valid: this.isValid(), errors: this.getErrors() });
  }

  /**
   * Update a field value and reactively re-validate it plus every field that
   * (transitively) depends on it. Emits a `field` event per re-validated
   * field whose errors changed, then one `form` event.
   */
  setValue(name: string, value: unknown): void {
    const field = this.fields.get(name);
    if (!field) throw new Error(`Unknown field "${name}"`);
    field.value = value;
    field.dirty = true;
    field.touched = true;

    const targets = [name, ...this.affectedBy(name)];
    for (const target of targets) {
      if (this.validateField(target)) {
        this.emit({ type: "field", field: target, state: this.snapshot(target) });
      }
    }
    this.emitForm();
  }

  /** Mark a field as touched (e.g. on blur) and re-validate it. */
  touch(name: string): void {
    const field = this.fields.get(name);
    if (!field) throw new Error(`Unknown field "${name}"`);
    field.touched = true;
    if (this.validateField(name)) {
      this.emit({ type: "field", field: name, state: this.snapshot(name) });
    }
    this.emitForm();
  }

  /** One-shot full-form validation. Marks every field touched. */
  validateAll(): boolean {
    for (const [name, field] of this.fields) {
      field.touched = true;
      if (this.validateField(name)) {
        this.emit({ type: "field", field: name, state: this.snapshot(name) });
      }
    }
    this.emitForm();
    return this.isValid();
  }

  isValid(): boolean {
    for (const field of this.fields.values()) {
      if (field.errors.length > 0) return false;
    }
    return true;
  }

  getState(name: string): FieldState {
    return this.snapshot(name);
  }

  getValue<T = unknown>(name: string): T {
    const field = this.fields.get(name);
    if (!field) throw new Error(`Unknown field "${name}"`);
    return field.value as T;
  }

  getValues(): Values {
    const values: Values = {};
    for (const [name, field] of this.fields) values[name] = field.value;
    return values;
  }

  /** field -> current error messages (only fields with errors). */
  getErrors(): Record<string, readonly string[]> {
    const errors: Record<string, readonly string[]> = {};
    for (const [name, field] of this.fields) {
      if (field.errors.length > 0) errors[name] = [...field.errors];
    }
    return errors;
  }

  /** Reset all values/state back to the schema initials. */
  reset(): void {
    for (const [name, field] of this.fields) {
      field.value = this.schema[name].initial ?? "";
      field.dirty = false;
      field.touched = false;
    }
    for (const name of this.fields.keys()) {
      if (this.validateField(name)) {
        this.emit({ type: "field", field: name, state: this.snapshot(name) });
      }
    }
    this.emitForm();
  }

  subscribe(listener: FormListener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
}
