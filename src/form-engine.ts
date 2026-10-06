export type FieldValue = unknown;

export interface RuleContext {
  value(field: string): FieldValue;
  visible(field: string): boolean;
}

export interface FieldRule {
  visibleWhen?: (ctx: RuleContext) => boolean;
  validate?: (value: FieldValue, ctx: RuleContext) => string | null;
  validateAsync?: (value: FieldValue, ctx: RuleContext) => Promise<string | null>;
}

export type FieldRules = Record<string, FieldRule>;

export interface FieldState {
  readonly value: FieldValue;
  readonly visible: boolean;
  readonly errors: readonly string[];
  readonly pending: boolean;
}

export type SetRulesResult =
  | { readonly ok: true }
  | { readonly ok: false; readonly error: string };

export class CycleError extends Error {
  readonly fields: readonly string[];

  constructor(fields: readonly string[]) {
    super(`circular dependency among fields: ${fields.join(', ')}`);
    this.name = 'CycleError';
    this.fields = fields;
  }
}

export class UnknownFieldError extends Error {
  constructor(field: string) {
    super(`unknown field: ${field}`);
    this.name = 'UnknownFieldError';
  }
}

interface FieldRecord {
  value: FieldValue;
  valueVersion: number;
  visible: boolean;
  syncError: string | null;
  asyncError: string | null;
  inFlight: Set<number>;
  nextRunId: number;
  asyncDeps: string[] | null;
  asyncSignature: string | null;
}

interface FieldSnapshot {
  visible: boolean;
  syncError: string | null;
  asyncError: string | null;
  inFlight: Set<number>;
  nextRunId: number;
  asyncDeps: string[] | null;
  asyncSignature: string | null;
}

interface Snapshot {
  rules: FieldRules;
  rulesVersion: number;
  graph: Map<string, Set<string>>;
  fields: Map<string, FieldSnapshot>;
}

const MAX_STABILIZE_ITERATIONS = 100;

function setsEqual(a: Set<string>, b: Set<string>): boolean {
  if (a.size !== b.size) return false;
  for (const item of a) if (!b.has(item)) return false;
  return true;
}

function failureMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export class FormEngine {
  private readonly records = new Map<string, FieldRecord>();
  private rules: FieldRules = {};
  private graph = new Map<string, Set<string>>();
  private rulesVersion = 0;
  private readonly listeners = new Set<() => void>();
  private settleWaiters: Array<() => void> = [];

  constructor(
    fields: ReadonlyArray<string | { name: string; value?: FieldValue }>,
    rules: FieldRules = {},
  ) {
    for (const spec of fields) {
      const name = typeof spec === 'string' ? spec : spec.name;
      if (this.records.has(name)) throw new Error(`duplicate field: ${name}`);
      this.records.set(name, {
        value: typeof spec === 'string' ? undefined : spec.value,
        valueVersion: 0,
        visible: true,
        syncError: null,
        asyncError: null,
        inFlight: new Set(),
        nextRunId: 0,
        asyncDeps: null,
        asyncSignature: null,
      });
      this.graph.set(name, new Set());
    }
    this.rules = this.normalizeRules(rules);
    this.stabilize();
  }

  setValue(field: string, value: FieldValue): void {
    const rec = this.record(field);
    rec.value = value;
    rec.valueVersion += 1;
    this.stabilize();
    this.emit();
  }

  setRules(rules: FieldRules): SetRulesResult {
    let normalized: FieldRules;
    try {
      normalized = this.normalizeRules(rules);
    } catch (err) {
      return { ok: false, error: failureMessage(err) };
    }
    const snapshot = this.snapshot();
    this.rules = normalized;
    this.rulesVersion += 1;
    try {
      this.stabilize();
    } catch (err) {
      this.restore(snapshot);
      return { ok: false, error: failureMessage(err) };
    }
    this.emit();
    return { ok: true };
  }

  getState(field: string): FieldState {
    const rec = this.record(field);
    const errors: string[] = [];
    if (rec.syncError !== null) errors.push(rec.syncError);
    if (rec.asyncError !== null) errors.push(rec.asyncError);
    return {
      value: rec.value,
      visible: rec.visible,
      errors,
      pending: rec.inFlight.size > 0,
    };
  }

  dependencies(): Record<string, string[]> {
    const out: Record<string, string[]> = {};
    for (const [field, deps] of this.graph) out[field] = [...deps].sort();
    return out;
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  whenSettled(): Promise<void> {
    if (this.isSettled()) return Promise.resolve();
    return new Promise((resolve) => {
      this.settleWaiters.push(resolve);
    });
  }

  private stabilize(): void {
    this.recomputeVisibility();
    this.runSyncValidation();
    this.scheduleAsyncValidation();
  }

  private recomputeVisibility(): void {
    for (let iteration = 0; iteration < MAX_STABILIZE_ITERATIONS; iteration += 1) {
      const order = this.topoOrder();
      let settled = true;
      for (const name of order) {
        const rule = this.rules[name];
        const deps = new Set<string>();
        let visible = true;
        if (rule?.visibleWhen) {
          visible = Boolean(rule.visibleWhen(this.trackingCtx(deps)));
        }
        const rec = this.record(name);
        if (!setsEqual(deps, this.graph.get(name)!)) {
          this.graph.set(name, deps);
          settled = false;
        }
        if (rec.visible !== visible) {
          rec.visible = visible;
          settled = false;
        }
      }
      if (settled) return;
    }
    throw new Error('visibility rules did not reach a stable point');
  }

  private runSyncValidation(): void {
    for (const name of this.topoOrder()) {
      const rec = this.record(name);
      if (!rec.visible) {
        rec.syncError = null;
        rec.asyncError = null;
        continue;
      }
      const rule = this.rules[name];
      rec.syncError = rule?.validate
        ? rule.validate(rec.value, this.plainCtx()) ?? null
        : null;
    }
  }

  private scheduleAsyncValidation(): void {
    for (const [name, rec] of this.records) {
      const rule = this.rules[name];
      if (!rec.visible || !rule?.validateAsync) {
        if (rec.inFlight.size > 0 || rec.asyncSignature !== null) {
          rec.inFlight.clear();
          rec.nextRunId += 1;
          rec.asyncDeps = null;
          rec.asyncSignature = null;
          rec.asyncError = null;
        }
        continue;
      }
      const signature = this.asyncSignatureFor(name, rec.asyncDeps);
      if (signature === rec.asyncSignature) continue;
      rec.asyncError = null;
      rec.asyncSignature = signature;
      const runId = ++rec.nextRunId;
      rec.inFlight.add(runId);
      const valueVersion = rec.valueVersion;
      const rulesVersion = this.rulesVersion;
      const tracked = new Set<string>();
      const ctx = this.trackingCtx(tracked);
      let result: Promise<string | null>;
      try {
        result = Promise.resolve(rule.validateAsync(rec.value, ctx));
      } catch (err) {
        result = Promise.reject(err);
      }
      result.then(
        (message) => this.commitAsync(name, runId, valueVersion, rulesVersion, tracked, message, null),
        (err) => this.commitAsync(name, runId, valueVersion, rulesVersion, tracked, null, err),
      );
    }
  }

  private commitAsync(
    name: string,
    runId: number,
    valueVersion: number,
    rulesVersion: number,
    deps: Set<string>,
    message: string | null,
    failure: unknown,
  ): void {
    const rec = this.records.get(name);
    if (!rec) return;
    rec.inFlight.delete(runId);
    const current =
      runId === rec.nextRunId &&
      valueVersion === rec.valueVersion &&
      rulesVersion === this.rulesVersion &&
      rec.visible &&
      this.rules[name]?.validateAsync != null;
    if (current) {
      rec.asyncDeps = [...deps];
      rec.asyncSignature = this.asyncSignatureFor(name, rec.asyncDeps);
      rec.asyncError = failure !== null ? failureMessage(failure) : message;
    }
    if (this.isSettled()) this.releaseSettleWaiters();
    this.emit();
  }

  private asyncSignatureFor(name: string, deps: string[] | null): string {
    const rec = this.record(name);
    const parts = [`rules=${this.rulesVersion}`, `self=${rec.valueVersion}`];
    if (deps) {
      for (const dep of [...deps].sort()) {
        const depRec = this.record(dep);
        parts.push(`${dep}=${depRec.valueVersion}:${depRec.visible ? 1 : 0}`);
      }
    }
    return parts.join('|');
  }

  private topoOrder(): string[] {
    const indegree = new Map<string, number>();
    const dependents = new Map<string, string[]>();
    for (const name of this.records.keys()) {
      indegree.set(name, 0);
      dependents.set(name, []);
    }
    for (const [field, deps] of this.graph) {
      for (const dep of deps) {
        indegree.set(field, (indegree.get(field) ?? 0) + 1);
        dependents.get(dep)!.push(field);
      }
    }
    const queue: string[] = [];
    for (const [name, degree] of indegree) {
      if (degree === 0) queue.push(name);
    }
    const order: string[] = [];
    while (queue.length > 0) {
      const name = queue.shift()!;
      order.push(name);
      for (const dependent of dependents.get(name)!) {
        const degree = indegree.get(dependent)! - 1;
        indegree.set(dependent, degree);
        if (degree === 0) queue.push(dependent);
      }
    }
    if (order.length !== this.records.size) {
      const cyclic = [...indegree.entries()]
        .filter(([, degree]) => degree > 0)
        .map(([name]) => name);
      throw new CycleError(cyclic);
    }
    return order;
  }

  private trackingCtx(track: Set<string>): RuleContext {
    return {
      value: (field) => {
        const rec = this.record(field);
        track.add(field);
        return rec.value;
      },
      visible: (field) => {
        const rec = this.record(field);
        track.add(field);
        return rec.visible;
      },
    };
  }

  private plainCtx(): RuleContext {
    return {
      value: (field) => this.record(field).value,
      visible: (field) => this.record(field).visible,
    };
  }

  private normalizeRules(rules: FieldRules): FieldRules {
    const normalized: FieldRules = {};
    for (const [name, rule] of Object.entries(rules)) {
      if (!this.records.has(name)) throw new UnknownFieldError(name);
      normalized[name] = rule;
    }
    return normalized;
  }

  private snapshot(): Snapshot {
    const fields = new Map<string, FieldSnapshot>();
    for (const [name, rec] of this.records) {
      fields.set(name, {
        visible: rec.visible,
        syncError: rec.syncError,
        asyncError: rec.asyncError,
        inFlight: new Set(rec.inFlight),
        nextRunId: rec.nextRunId,
        asyncDeps: rec.asyncDeps ? [...rec.asyncDeps] : null,
        asyncSignature: rec.asyncSignature,
      });
    }
    return {
      rules: this.rules,
      rulesVersion: this.rulesVersion,
      graph: new Map([...this.graph].map(([k, v]) => [k, new Set(v)])),
      fields,
    };
  }

  private restore(snapshot: Snapshot): void {
    this.rules = snapshot.rules;
    this.rulesVersion = snapshot.rulesVersion;
    this.graph = snapshot.graph;
    for (const [name, state] of snapshot.fields) {
      Object.assign(this.records.get(name)!, state);
    }
  }

  private record(field: string): FieldRecord {
    const rec = this.records.get(field);
    if (!rec) throw new UnknownFieldError(field);
    return rec;
  }

  private isSettled(): boolean {
    for (const rec of this.records.values()) {
      if (rec.inFlight.size > 0) return false;
    }
    return true;
  }

  private releaseSettleWaiters(): void {
    const waiters = this.settleWaiters;
    this.settleWaiters = [];
    for (const resolve of waiters) resolve();
  }

  private emit(): void {
    for (const listener of [...this.listeners]) listener();
  }
}
