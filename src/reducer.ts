/**
 * Budget-aware hierarchical reducer.
 *
 * Reduction order per pass (hierarchical delta debugging):
 *   1. attribute level  — ddmin over removable attributes;
 *   2. set level        — ddmin over elements of each finite-set attribute;
 *   3. integer level    — boundary search per integer attribute.
 * Passes repeat until a fixpoint, then every declared basic reduction op is
 * tried once more to build a 1-minimality certificate.
 *
 * Invariants:
 *  - validity first: schema-invalid candidates are rejected locally without
 *    spending oracle budget; the difference is only checked on valid inputs;
 *  - every oracle result is journaled with the rule version (cache);
 *  - the best candidate is checkpointed after every accepted step, so
 *    resuming from the journal never loses it;
 *  - budget exhaustion never reports "minimal", only "local-minimum".
 */
import type { Input } from './condition.ts';
import { isValid, isRemovable, type Schema } from './schema.ts';
import type { Oracle, OracleVerdict } from './policy.ts';
import { Journal, loadJournal, canonicalKey } from './journal.ts';

export interface ReducerOptions {
  schema: Schema;
  oracle: Oracle;
  /** Rule version; cached oracle results from other versions are ignored. */
  ruleVersion: string;
  /** Total oracle-call budget (consumed calls from a resumed journal count). */
  budget: number;
  journalPath: string;
  /** Resume from an existing journal: restores cache, budget usage, best candidate. */
  resume?: boolean;
  runId?: string;
}

export type ReductionStatus = 'minimal' | 'local-minimum';

export interface StepRecord {
  op: string;
  detail: string;
  before: Input;
  after: Input;
  oracleUsed: number;
  budgetLeft: number;
}

export interface CertificateCheck {
  op: string;
  verdict: 'invalid' | 'same' | 'diff' | 'not-checked';
}

export interface ReductionResult {
  candidate: Input;
  status: ReductionStatus;
  reason: string;
  oracleCalls: number;
  budget: number;
  steps: StepRecord[];
  certificate: { complete: boolean; checks: CertificateCheck[] };
}

class BudgetExhausted extends Error {}

type Verdict = 'invalid' | OracleVerdict;

export class Reducer {
  private readonly journal: Journal;
  private readonly opts: ReducerOptions;
  private readonly cache = new Map<string, OracleVerdict>();
  private oracleUsed = 0;
  private readonly steps: StepRecord[] = [];
  private exhausted = false;

  constructor(opts: ReducerOptions) {
    this.opts = opts;
    this.journal = new Journal(opts.journalPath);
    if (opts.resume) {
      const state = loadJournal(opts.journalPath);
      if (state.ruleVersion === opts.ruleVersion) {
        for (const [k, v] of state.oracleCache) this.cache.set(k, v);
        this.oracleUsed = state.oracleCalls;
      }
      // Recovery: the checkpointed best candidate is never lost.
      if (state.best) this.best = structuredClone(state.best);
      this.journal.rewindTo(state.maxSeq);
    }
    this.journal.emit({
      type: 'run-start',
      runId: opts.runId ?? `run-${Date.now()}`,
      ruleVersion: opts.ruleVersion,
      budget: opts.budget,
      at: new Date().toISOString(),
    });
  }

  private best: Input | undefined;

  private budgetLeft(): number {
    return this.opts.budget - this.oracleUsed;
  }

  /** Validity is free (local); only valid candidates consume oracle budget. */
  private test(candidate: Input): Verdict {
    if (!isValid(this.opts.schema, candidate)) return 'invalid';
    const key = canonicalKey(candidate);
    const cached = this.cache.get(key);
    if (cached !== undefined) return cached;
    if (this.budgetLeft() <= 0) {
      this.exhausted = true;
      throw new BudgetExhausted();
    }
    const verdict = this.opts.oracle(candidate);
    this.oracleUsed++;
    this.cache.set(key, verdict);
    this.journal.emit({ type: 'oracle', seq: this.journal.nextSeq(), key, verdict });
    return verdict;
  }

  private preserves(candidate: Input): boolean {
    return this.test(candidate) === 'diff';
  }

  private accept(op: string, detail: string, before: Input, after: Input): void {
    const step: StepRecord = {
      op, detail,
      before: structuredClone(before),
      after: structuredClone(after),
      oracleUsed: this.oracleUsed,
      budgetLeft: this.budgetLeft(),
    };
    this.steps.push(step);
    this.journal.emit({ type: 'step', seq: this.journal.nextSeq(), ...step });
    // Checkpoint: recovery must never lose the best candidate.
    this.journal.emit({ type: 'best', seq: this.journal.nextSeq(), candidate: structuredClone(after) });
  }

  reduce(input: Input): ReductionResult {
    const initial = this.best ?? structuredClone(input);
    if (!isValid(this.opts.schema, initial)) {
      throw new Error('initial input is not schema-valid; nothing to reduce');
    }
    if (this.test(initial) !== 'diff') {
      throw new Error('initial input does not exhibit the judgment difference');
    }
    let current = initial;
    this.journal.emit({ type: 'best', seq: this.journal.nextSeq(), candidate: structuredClone(current) });

    try {
      let changed = true;
      while (changed) {
        changed = false;
        let next = this.ddminAttributes(current);
        if (next !== current) { current = next; changed = true; }
        next = this.ddminAllSets(current);
        if (next !== current) { current = next; changed = true; }
        next = this.shrinkAllInts(current);
        if (next !== current) { current = next; changed = true; }
      }
    } catch (err) {
      if (!(err instanceof BudgetExhausted)) throw err;
    }

    const certificate = this.certify(current);
    const minimal = certificate.complete && !this.exhausted;
    const status: ReductionStatus = minimal ? 'minimal' : 'local-minimum';
    const reason = minimal
      ? 'fixpoint reached and every declared basic op fails to preserve the difference'
      : this.exhausted
        ? 'oracle budget exhausted; only a local minimum is claimed'
        : 'certificate incomplete';
    const result: ReductionResult = {
      candidate: current,
      status,
      reason,
      oracleCalls: this.oracleUsed,
      budget: this.opts.budget,
      steps: this.steps,
      certificate,
    };
    this.journal.emit({
      type: 'finish', seq: this.journal.nextSeq(), status, reason,
      candidate: structuredClone(current),
    });
    return result;
  }

  // ----- level 1: attributes -------------------------------------------------

  private ddminAttributes(input: Input): Input {
    const removable = Object.keys(input).filter((k) => isRemovable(this.opts.schema, k, input));
    if (removable.length === 0) return input;
    const kept = this.ddmin(removable, (keptSet) => {
      const candidate: Input = {};
      for (const [k, v] of Object.entries(input)) {
        if (!removable.includes(k) || keptSet.has(k)) candidate[k] = v;
      }
      return candidate;
    });
    if (kept.size === removable.length) return input;
    const after: Input = {};
    for (const [k, v] of Object.entries(input)) {
      if (!removable.includes(k) || kept.has(k)) after[k] = v;
    }
    const removed = removable.filter((k) => !kept.has(k));
    this.accept('remove-attrs', `ddmin removed attributes: ${removed.join(', ')}`, input, after);
    return after;
  }

  // ----- level 2: finite-set elements ----------------------------------------

  private ddminAllSets(input: Input): Input {
    let current = input;
    for (const [name, def] of Object.entries(this.opts.schema.fields)) {
      if (def.type.kind !== 'set') continue;
      const value = current[name];
      if (!Array.isArray(value)) continue;
      const minSize = def.type.minSize ?? 0;
      const removableCount = value.length - minSize;
      if (removableCount <= 0) continue;
      const elements = [...value] as string[];
      const kept = this.ddmin(elements, (keptSet) => ({
        ...current,
        [name]: elements.filter((e) => keptSet.has(e)),
      }));
      // ddmin may not shrink below minSize worth of kept elements:
      if (kept.size < minSize) continue;
      if (kept.size === elements.length) continue;
      const after = {
        ...current,
        [name]: elements.filter((e) => kept.has(e)),
      };
      this.accept(
        'remove-set-elements',
        `ddmin ${name}: kept [${[...kept].join(', ')}]`,
        current, after,
      );
      current = after;
    }
    return current;
  }

  /**
   * Generic ddmin: given elements currently all present in the input and a
   * builder mapping a kept-subset to a candidate, return a 1-minimal kept
   * subset that still preserves the difference. Subsets that are invalid or
   * lose the difference are rejected (validity checked first, for free).
   */
  private ddmin<T>(elements: T[], build: (kept: Set<T>) => Input): Set<T> {
    let kept = new Set(elements);
    let n = 2;
    while (kept.size >= 2) {
      const subsets = splitEvenly([...kept], n);
      let reduced = false;
      for (const subset of subsets) {
        const complement = new Set([...kept].filter((e) => !subset.includes(e)));
        if (complement.size === 0) continue;
        if (this.preserves(build(complement))) {
          kept = complement;
          n = Math.max(n - 1, 2);
          reduced = true;
          break;
        }
      }
      if (reduced) continue;
      for (const subset of subsets) {
        if (subset.length === 0 || subset.length === kept.size) continue;
        if (this.preserves(build(new Set(subset)))) {
          kept = new Set(subset);
          n = 2;
          reduced = true;
          break;
        }
      }
      if (reduced) continue;
      if (n >= kept.size) break;
      n = Math.min(n * 2, kept.size);
    }
    // Drain: the ddmin loop stops at granularity limits; greedily remove the
    // remaining elements one at a time so the result is 1-minimal w.r.t.
    // single removals (the certificate relies on this).
    let progress = true;
    while (progress && kept.size > 0) {
      progress = false;
      for (const e of [...kept]) {
        const next = new Set(kept);
        next.delete(e);
        if (this.preserves(build(next))) {
          kept = next;
          progress = true;
          break;
        }
      }
    }
    return kept;
  }

  // ----- level 3: integer boundary search -------------------------------------

  private shrinkAllInts(input: Input): Input {
    let current = input;
    for (const [name, def] of Object.entries(this.opts.schema.fields)) {
      if (def.type.kind !== 'int') continue;
      const value = current[name];
      if (typeof value !== 'number') continue;
      const next = this.shrinkInt(current, name, value, def.type.min);
      if (next !== current) current = next;
    }
    return current;
  }

  /**
   * Boundary search toward the schema minimum: try the minimum outright;
   * if it fails, binary-search the flip boundary between min (failing) and
   * the current value (preserving) for the smallest preserving integer.
   */
  private shrinkInt(input: Input, name: string, value: number, min: number): Input {
    if (value === min) return input;
    const at = (v: number): Input => ({ ...input, [name]: v });
    if (this.preserves(at(min))) {
      this.accept('int-to-min', `${name}: ${value} -> ${min}`, input, at(min));
      return at(min);
    }
    // Binary search: lo fails (or is min, failing), hi preserves.
    let lo = min;
    let hi = value;
    let best = input;
    while (hi - lo > 1) {
      const mid = lo + Math.floor((hi - lo) / 2);
      if (this.preserves(at(mid))) {
        hi = mid;
        best = at(mid);
      } else {
        lo = mid;
      }
    }
    if (hi !== value) {
      this.accept('int-boundary', `${name}: ${value} -> ${hi} (flip boundary)`, input, best);
      return best;
    }
    return input;
  }

  // ----- 1-minimality certificate ---------------------------------------------

  /** Declared basic reduction ops for the certificate. */
  declaredBasicOps(input: Input): { op: string; apply: () => Input }[] {
    const ops: { op: string; apply: () => Input }[] = [];
    for (const name of Object.keys(input)) {
      if (isRemovable(this.opts.schema, name, input)) {
        ops.push({
          op: `remove-attr ${name}`,
          apply: () => {
            const c = { ...input };
            delete c[name];
            return c;
          },
        });
      }
    }
    for (const [name, def] of Object.entries(this.opts.schema.fields)) {
      const value = input[name];
      if (def.type.kind === 'set' && Array.isArray(value)) {
        const minSize = def.type.minSize ?? 0;
        if (value.length > minSize) {
          for (const el of value as string[]) {
            ops.push({
              op: `remove-set-element ${name} -= ${el}`,
              apply: () => ({ ...input, [name]: (value as string[]).filter((e) => e !== el) }),
            });
          }
        }
      }
      if (def.type.kind === 'int' && typeof value === 'number' && value > def.type.min) {
        ops.push({
          op: `dec-int ${name} -> ${value - 1}`,
          apply: () => ({ ...input, [name]: value - 1 }),
        });
      }
    }
    return ops;
  }

  private certify(input: Input): { complete: boolean; checks: CertificateCheck[] } {
    const checks: CertificateCheck[] = [];
    let complete = true;
    for (const { op, apply } of this.declaredBasicOps(input)) {
      try {
        const verdict = this.test(apply());
        checks.push({ op, verdict });
      } catch (err) {
        if (!(err instanceof BudgetExhausted)) throw err;
        checks.push({ op, verdict: 'not-checked' });
        complete = false;
      }
    }
    return { complete, checks };
  }
}

function splitEvenly<T>(items: T[], n: number): T[][] {
  const size = Math.ceil(items.length / n);
  const out: T[][] = [];
  for (let i = 0; i < items.length; i += size) out.push(items.slice(i, i + size));
  return out;
}
