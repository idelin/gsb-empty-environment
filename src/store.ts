/**
 * Transactional store with cross-field invariants.
 *
 * Design notes:
 * - No reactive framework: a small explicit dependency tracker records which
 *   fields each invariant actually reads while it runs on a state snapshot.
 * - Dependencies are dynamic: a rule like `if (mode === 'strict') ...` reads
 *   different fields per branch, so dependencies are re-tracked on the
 *   transaction draft at every commit and only persisted on success.
 * - A commit validates only affected rules (old deps intersect changed
 *   fields). This is sound for pure rules: a rule whose old dependencies are
 *   untouched takes the same branches and reads the same fields.
 * - Rejected commits are atomic: the draft and any freshly tracked dependency
 *   edges are discarded; state, history and subscribers are untouched.
 * - Readers only ever see the last committed (frozen) snapshot, never a draft.
 */

export type Field = string;
export type Value = unknown;
export type State = Record<Field, Value>;

export interface SetOp {
  readonly kind: 'set';
  readonly field: Field;
  readonly value: Value;
}
export interface DeleteOp {
  readonly kind: 'delete';
  readonly field: Field;
}
/** Operations are plain data so they can be logged, minimized and replayed. */
export type Operation = SetOp | DeleteOp;

export interface ReadCtx {
  /** Tracked read: the field is added to the rule's actual dependency set. */
  get(field: Field): Value;
  /** Shared pure computation, memoized once per validation pass. */
  compute(name: string): Value;
}

/** Returns undefined/void when the invariant holds, a message when violated. May throw. */
export type CheckFn = (ctx: ReadCtx) => string | void;
/** Pure function of tracked reads; shared between invariants within one pass. */
export type ComputedFn = (read: (field: Field) => Value) => Value;

export interface Violation {
  readonly rule: string;
  readonly message: string;
  /** Fields the rule actually read on the draft when it failed. */
  readonly fields: readonly Field[];
  /** Present when the rule threw instead of returning a message. */
  readonly error?: unknown;
}

export interface Counterexample {
  /** 1-minimal subset of the transaction's operations that still fails. */
  readonly ops: readonly Operation[];
  /** Names of the invariants the counterexample reproduces. */
  readonly violates: readonly string[];
}

export interface CommitOk {
  readonly ok: true;
  readonly state: Readonly<State>;
}

export interface CommitFailure {
  readonly ok: false;
  readonly violations: readonly Violation[];
  /** Union of fields involved in all violations. */
  readonly fields: readonly Field[];
  readonly counterexample: Counterexample;
  /** Re-applies the counterexample against the current committed state. */
  replay(): CommitResult;
}

export type CommitResult = CommitOk | CommitFailure;

export interface DryRunResult {
  readonly ok: boolean;
  readonly violations: readonly Violation[];
}

interface RuleState {
  readonly name: string;
  readonly check: CheckFn;
  deps: ReadonlySet<Field> | null;
}

interface MemoEntry {
  readonly value: Value;
  readonly fields: ReadonlySet<Field>;
}

interface EvalOutcome {
  readonly failed: boolean;
  readonly message?: string;
  readonly error?: unknown;
  readonly fields: Set<Field>;
}

function intersects(a: ReadonlySet<Field>, b: ReadonlySet<Field>): boolean {
  for (const item of a) if (b.has(item)) return true;
  return false;
}

function applyOps(base: State, ops: readonly Operation[]): State {
  const draft: State = { ...base };
  for (const op of ops) {
    if (op.kind === 'set') draft[op.field] = op.value;
    else delete draft[op.field];
  }
  return draft;
}

function changedFields(base: State, draft: State, ops: readonly Operation[]): Set<Field> {
  const touched = new Set<Field>();
  for (const op of ops) touched.add(op.field);
  const changed = new Set<Field>();
  for (const field of touched) {
    if (!Object.is(base[field], draft[field])) changed.add(field);
  }
  return changed;
}

export class Store {
  #state: State;
  readonly #rules = new Map<string, RuleState>();
  readonly #computeds = new Map<string, ComputedFn>();
  readonly #history: readonly Operation[][] = [];
  readonly #subscribers = new Set<(snapshot: Readonly<State>) => void>();
  #notifying = false;

  constructor(initial: State = {}) {
    this.#state = Object.freeze({ ...initial });
  }

  /** Last committed snapshot. Frozen; drafts are never exposed. */
  getState(): Readonly<State> {
    return this.#state;
  }

  /** Committed transactions, as replayable operation lists. */
  history(): readonly (readonly Operation[])[] {
    return this.#history;
  }

  defineComputed(name: string, fn: ComputedFn): this {
    if (this.#computeds.has(name)) throw new Error(`computed "${name}" already defined`);
    this.#computeds.set(name, fn);
    return this;
  }

  /**
   * Registers a cross-field invariant. The rule must hold on the current
   * committed state; its initial dependency set is tracked immediately.
   */
  registerInvariant(name: string, check: CheckFn): this {
    if (this.#rules.has(name)) throw new Error(`invariant "${name}" already registered`);
    const outcome = this.#evaluate(this.#state, check, new Map());
    if (outcome.failed) {
      throw new Error(`invariant "${name}" does not hold on current state: ${outcome.message}`);
    }
    this.#rules.set(name, { name, check, deps: outcome.fields });
    return this;
  }

  begin(): Transaction {
    return new Transaction(this);
  }

  /** Notified once per successful commit with the frozen snapshot. */
  subscribe(listener: (snapshot: Readonly<State>) => void): () => void {
    this.#subscribers.add(listener);
    return () => this.#subscribers.delete(listener);
  }

  /**
   * Validates and atomically commits an operation list.
   *
   * Only rules whose tracked dependencies intersect the changed fields are
   * re-evaluated, and they are re-tracked on the transaction draft so
   * conditional branches pick up their actual (new) dependencies. On failure
   * nothing is persisted: no state, no history, no new dependency edges.
   */
  commitOps(ops: readonly Operation[]): CommitResult {
    if (this.#notifying) {
      throw new Error('cannot commit from within a subscriber notification');
    }
    if (ops.length === 0) return { ok: true, state: this.#state };

    const draft = applyOps(this.#state, ops);
    const changed = changedFields(this.#state, draft, ops);
    const { violations, depsUpdates } = this.#validate(draft, changed, false);

    if (violations.length > 0) {
      const counterexample = this.#minimize(ops, violations);
      const fields = [...new Set(violations.flatMap((v) => v.fields))];
      return {
        ok: false,
        violations,
        fields,
        counterexample,
        replay: () => this.commitOps(counterexample.ops),
      };
    }

    for (const [name, deps] of depsUpdates) {
      const rule = this.#rules.get(name);
      if (rule) rule.deps = deps;
    }
    this.#state = Object.freeze(draft);
    this.#history.push(Object.freeze([...ops]));
    this.#notify();
    return { ok: true, state: this.#state };
  }

  /**
   * Validates an operation list without committing. `incremental` uses
   * dependency-based pruning (the commit fast path); `full` re-checks every
   * rule. Comparing the two is the differential oracle used by the tests.
   */
  dryRun(ops: readonly Operation[], mode: 'incremental' | 'full' = 'incremental'): DryRunResult {
    const draft = applyOps(this.#state, ops);
    const changed = changedFields(this.#state, draft, ops);
    const { violations } = this.#validate(draft, changed, mode === 'full');
    return { ok: violations.length === 0, violations };
  }

  /** Current dependency edges (for tests and diagnostics). */
  depsOf(name: string): readonly Field[] | null {
    const rule = this.#rules.get(name);
    if (!rule || rule.deps === null) return null;
    return [...rule.deps].sort();
  }

  /** Freshly tracked dependencies of every rule on the committed state. */
  freshDeps(): Record<string, readonly Field[]> {
    const out: Record<string, readonly Field[]> = {};
    for (const rule of this.#rules.values()) {
      const outcome = this.#evaluate(this.#state, rule.check, new Map());
      out[rule.name] = [...outcome.fields].sort();
    }
    return out;
  }

  /** Replays committed transactions onto a fresh store. */
  static replay(
    history: readonly (readonly Operation[])[],
    configure?: (store: Store) => void,
    initial: State = {},
  ): Store {
    const store = new Store(initial);
    configure?.(store);
    history.forEach((ops, index) => {
      const result = store.commitOps(ops);
      if (!result.ok) {
        const names = result.violations.map((v) => v.rule).join(', ');
        throw new Error(`replay failed at transaction ${index}: ${names}`);
      }
    });
    return store;
  }

  #evaluate(state: State, check: CheckFn, memo: Map<string, MemoEntry>): EvalOutcome {
    const fields = new Set<Field>();
    const read = (field: Field): Value => {
      fields.add(field);
      return state[field];
    };
    const ctx: ReadCtx = {
      get: read,
      compute: (name: string): Value => {
        const cached = memo.get(name);
        if (cached !== undefined) {
          // A memo hit still contributes the computed's dependencies.
          for (const field of cached.fields) fields.add(field);
          return cached.value;
        }
        const fn = this.#computeds.get(name);
        if (fn === undefined) throw new Error(`unknown computed "${name}"`);
        const computedFields = new Set<Field>();
        const computedRead = (field: Field): Value => {
          computedFields.add(field);
          return read(field);
        };
        const value = fn(computedRead);
        memo.set(name, { value, fields: computedFields });
        return value;
      },
    };
    try {
      const message = check(ctx);
      if (typeof message === 'string') return { failed: true, message, fields };
      return { failed: false, fields };
    } catch (error) {
      return {
        failed: true,
        message: error instanceof Error ? error.message : String(error),
        error,
        fields,
      };
    }
  }

  #validate(draft: State, changed: ReadonlySet<Field>, forceAll: boolean) {
    // One memo per validation pass: invariants evaluated in the same pass
    // share pure computations.
    const memo = new Map<string, MemoEntry>();
    const violations: Violation[] = [];
    const depsUpdates = new Map<string, Set<Field>>();
    for (const rule of this.#rules.values()) {
      const affected =
        forceAll || rule.deps === null || intersects(rule.deps, changed);
      if (!affected) continue;
      const outcome = this.#evaluate(draft, rule.check, memo);
      depsUpdates.set(rule.name, outcome.fields);
      if (outcome.failed) {
        violations.push({
          rule: rule.name,
          message: outcome.message ?? 'violated',
          fields: Object.freeze([...outcome.fields].sort()),
          error: outcome.error,
        });
      }
    }
    return { violations, depsUpdates };
  }

  /**
   * Greedy 1-minimal reduction: returns a subset of the transaction's
   * operations such that removing any single remaining operation no longer
   * reproduces one of the original violations.
   */
  #minimize(ops: readonly Operation[], violations: readonly Violation[]): Counterexample {
    const violated = new Set(violations.map((v) => v.rule));
    const stillFails = (candidate: readonly Operation[]): boolean => {
      if (candidate.length === 0) return false;
      const draft = applyOps(this.#state, candidate);
      const result = this.#validate(draft, new Set(), true);
      return result.violations.some((v) => violated.has(v.rule));
    };
    let current: Operation[] = [...ops];
    let index = 0;
    while (index < current.length) {
      const candidate = [...current.slice(0, index), ...current.slice(index + 1)];
      if (stillFails(candidate)) current = candidate;
      else index += 1;
    }
    return {
      ops: Object.freeze(current),
      violates: Object.freeze([...violated].sort()),
    };
  }

  #notify(): void {
    this.#notifying = true;
    try {
      for (const listener of [...this.#subscribers]) listener(this.#state);
    } finally {
      this.#notifying = false;
    }
  }
}

export class Transaction {
  readonly #store: Store;
  readonly #ops: Operation[] = [];
  #closed = false;

  constructor(store: Store) {
    this.#store = store;
  }

  set(field: Field, value: Value): this {
    this.#ops.push({ kind: 'set', field, value });
    return this;
  }

  delete(field: Field): this {
    this.#ops.push({ kind: 'delete', field });
    return this;
  }

  get ops(): readonly Operation[] {
    return this.#ops;
  }

  commit(): CommitResult {
    if (this.#closed) throw new Error('transaction already committed');
    this.#closed = true;
    return this.#store.commitOps(this.#ops);
  }
}
