import { CycleError, findCycle, topoOrder } from './graph.ts';
import type {
  FieldRules,
  FieldSnapshot,
  FormSchema,
  RuleContext,
  ValidateResult,
} from './types.ts';

export { CycleError };

export class UnknownFieldError extends Error {
  constructor(name: string) {
    super(`unknown field: ${name}`);
    this.name = 'UnknownFieldError';
  }
}

interface FieldRuntime {
  readonly name: string;
  value: unknown;
  valueVersion: number;
  rulesVersion: number;
  validationSeq: number;
  visible: boolean;
  errors: string[];
  pending: number;
  rules: Required<Pick<FieldRules, 'deps'>> & FieldRules;
}

function normalizeRules(rules: FieldRules): FieldRuntime['rules'] {
  return { ...rules, deps: [...new Set(rules.deps ?? [])] };
}

function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

function isThenable(value: unknown): value is PromiseLike<ValidateResult> {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as { then?: unknown }).then === 'function'
  );
}

/**
 * A dynamic-form state store.
 *
 * Semantics:
 * - Fields may depend on other fields' values and visibility via rules.
 * - Hidden fields keep their value but are excluded from validation;
 *   their errors are cleared as soon as they become hidden.
 * - Every mutation is propagated synchronously to a stable point: a full
 *   topological recompute of visibility, then a full recompute of
 *   validation for all visible fields.
 * - Rule updates are atomic: if the updated dependency graph contains a
 *   cycle, the whole update is rejected and previous rules stay in effect.
 * - Async validation results commit only when the field's validation
 *   sequence still matches the one captured when the validator started,
 *   so stale results (older value, switched rules, hidden field) are
 *   discarded.
 */
export class FormStore {
  readonly #fields = new Map<string, FieldRuntime>();
  readonly #listeners = new Set<(store: FormStore) => void>();
  readonly #pending = new Set<Promise<void>>();
  readonly #ctx: RuleContext;

  constructor(schema: FormSchema, initialValues: Record<string, unknown> = {}) {
    for (const [name, rules] of Object.entries(schema)) {
      this.#fields.set(name, {
        name,
        value: name in initialValues ? initialValues[name] : undefined,
        valueVersion: 0,
        rulesVersion: 0,
        validationSeq: 0,
        visible: true,
        errors: [],
        pending: 0,
        rules: normalizeRules(rules),
      });
    }
    this.#ctx = {
      value: (name) => this.#field(name).value,
      visible: (name) => this.#field(name).visible,
    };
    this.#assertAcyclic(this.#currentGraph());
    this.#stabilize();
  }

  snapshot(name: string): FieldSnapshot {
    const field = this.#field(name);
    return {
      value: field.value,
      visible: field.visible,
      errors: [...field.errors],
      validating: field.pending > 0,
    };
  }

  setValue(name: string, value: unknown): void {
    const field = this.#field(name);
    if (Object.is(field.value, value)) return;
    field.value = value;
    field.valueVersion++;
    this.#stabilize();
    this.#emit();
  }

  /**
   * Applies a batch of rule updates atomically. Passing null removes a
   * field's rules. Throws CycleError (and changes nothing) when the
   * resulting dependency graph contains a cycle.
   */
  updateRules(updates: Record<string, FieldRules | null>): void {
    const candidate = new Map<string, FieldRuntime['rules']>();
    for (const [name, field] of this.#fields) candidate.set(name, field.rules);
    for (const [name, rules] of Object.entries(updates)) {
      this.#field(name);
      candidate.set(name, normalizeRules(rules ?? {}));
    }
    this.#assertAcyclic(this.#graphOf(candidate));

    for (const name of Object.keys(updates)) {
      const field = this.#fields.get(name)!;
      field.rules = candidate.get(name)!;
      field.rulesVersion++;
    }
    this.#stabilize();
    this.#emit();
  }

  subscribe(listener: (store: FormStore) => void): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  /** Resolves once every currently in-flight async validation has settled. */
  async whenSettled(): Promise<void> {
    await Promise.all([...this.#pending]);
  }

  #field(name: string): FieldRuntime {
    const field = this.#fields.get(name);
    if (!field) throw new UnknownFieldError(name);
    return field;
  }

  #currentGraph(): Map<string, readonly string[]> {
    return this.#graphOf(new Map([...this.#fields].map(([n, f]) => [n, f.rules])));
  }

  #graphOf(
    rules: ReadonlyMap<string, FieldRuntime['rules']>,
  ): Map<string, readonly string[]> {
    const graph = new Map<string, readonly string[]>();
    for (const [name, rule] of rules) graph.set(name, rule.deps);
    return graph;
  }

  #assertAcyclic(graph: ReadonlyMap<string, readonly string[]>): void {
    for (const [name, deps] of graph) {
      for (const dep of deps) {
        if (!this.#fields.has(dep)) {
          throw new UnknownFieldError(`${dep} (dependency of ${name})`);
        }
      }
    }
    const cycle = findCycle(graph);
    if (cycle) throw new CycleError(cycle);
  }

  /** Propagates the current values to a stable point, synchronously. */
  #stabilize(): void {
    const order = topoOrder(this.#currentGraph());

    for (const name of order) {
      const field = this.#fields.get(name)!;
      const visible = field.rules.visibleWhen
        ? Boolean(field.rules.visibleWhen(this.#ctx))
        : true;
      field.visible = visible;
      if (!visible) field.errors = [];
    }

    for (const name of order) {
      const field = this.#fields.get(name)!;
      if (field.visible) this.#validate(field);
    }
  }

  #validate(field: FieldRuntime): void {
    field.validationSeq++;
    const validate = field.rules.validate;
    if (!validate) {
      field.errors = [];
      return;
    }
    const seq = field.validationSeq;

    let result: ReturnType<NonNullable<FieldRules['validate']>>;
    try {
      result = validate(field.value, this.#ctx);
    } catch (err) {
      field.errors = [errorMessage(err)];
      return;
    }

    if (!isThenable(result)) {
      field.errors = result == null ? [] : [String(result)];
      return;
    }

    field.pending++;
    const tracked: Promise<void> = Promise.resolve(result).then(
      (message) => this.#commit(field, seq, message == null ? [] : [String(message)]),
      (err) => this.#commit(field, seq, [errorMessage(err)]),
    );
    const cleanup = tracked.finally(() => {
      field.pending--;
      this.#pending.delete(cleanup);
      this.#emit();
    });
    this.#pending.add(cleanup);
  }

  /** Commits an async result only if it still matches the current run. */
  #commit(field: FieldRuntime, seq: number, errors: string[]): void {
    if (field.validationSeq !== seq) return; // stale: value/rules changed
    if (!field.visible) return; // stale: field hidden meanwhile
    field.errors = errors;
  }

  #emit(): void {
    for (const listener of this.#listeners) listener(this);
  }
}
