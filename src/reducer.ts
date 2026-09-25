import { writeFileSync, readFileSync, existsSync } from 'node:fs';
import { validate } from './constraints.ts';
import type { Input, Policy, RuleVersions, Schema } from './model.ts';
import { Oracle, type VerdictPair } from './oracle.ts';
import { applyOp, cascadeRemove, cloneInput, describeOp, enumerateOps, measure, type Op } from './ops.ts';

export type Outcome = 'accepted' | 'rejected-invalid' | 'rejected-difference-lost';
type TestResult = Outcome | 'exhausted';

export interface StepEvidence {
  step: number;
  phase: 'attr-ddmin' | 'set-ddmin' | 'int-boundary' | 'certify';
  op: string;
  outcome: Outcome;
  verdicts?: VerdictPair;
  ruleVersions: RuleVersions;
  budget: { max: number; spent: number; remaining: number };
  errors?: string[];
}

export interface CertificateEntry {
  op: string;
  outcome: Exclude<Outcome, 'accepted'>;
}

export interface ReductionResult {
  candidate: Input;
  /** '1-minimal' only when the certificate pass completed; budget exhaustion
   *  can only ever claim a local minimum. */
  status: '1-minimal' | 'local-minimum';
  budgetExhausted: boolean;
  budget: { max: number; spent: number };
  ruleVersions: RuleVersions;
  evidence: StepEvidence[];
  certificate?: CertificateEntry[];
  journalPath?: string;
}

interface Journal {
  formatVersion: 1;
  status: 'in-progress' | '1-minimal' | 'local-minimum';
  budget: { max: number; spent: number };
  ruleVersions: RuleVersions;
  candidate: Input;
  cache: Record<string, VerdictPair>;
  evidence: StepEvidence[];
  certificate: CertificateEntry[] | null;
}

export interface ReducerOptions {
  schema: Schema;
  policyA: Policy;
  policyB: Policy;
  budget: number;
  journalPath?: string;
}

export class Reducer {
  private readonly schema: Schema;
  private readonly oracle: Oracle;
  private budgetMax: number;
  private readonly journalPath?: string;
  private evidence: StepEvidence[] = [];
  private candidate: Input = {};
  private target!: VerdictPair;
  private exhausted = false;

  constructor(opts: ReducerOptions) {
    this.schema = opts.schema;
    this.oracle = new Oracle(opts.policyA, opts.policyB);
    this.budgetMax = opts.budget;
    this.journalPath = opts.journalPath;
  }

  reduce(input: Input): ReductionResult {
    const v = validate(this.schema, input);
    if (!v.ok) throw new Error(`initial input invalid: ${v.errors.join('; ')}`);
    const pair = this.oracle.evaluate(input);
    if (!this.oracle.differs(pair)) {
      throw new Error('initial input exhibits no judgment difference');
    }
    this.target = pair;
    this.candidate = cloneInput(input);
    this.saveJournal('in-progress');
    this.mainLoop();
    return this.finish();
  }

  /** Resume from a journal: restores candidate, oracle cache, spent budget and
   *  evidence — recovery never loses the best candidate found so far. */
  static resume(
    journalPath: string,
    opts: Omit<ReducerOptions, 'budget' | 'journalPath'> & { extraBudget: number },
  ): Reducer {
    if (!existsSync(journalPath)) throw new Error(`no journal at ${journalPath}`);
    const j = JSON.parse(readFileSync(journalPath, 'utf8')) as Journal;
    const r = new Reducer({ ...opts, budget: j.budget.spent + opts.extraBudget, journalPath });
    r.candidate = j.candidate;
    r.oracle.importCache(j.cache);
    r.oracle.spent = j.budget.spent;
    r.target = r.oracle.evaluate(j.candidate);
    r.evidence = j.evidence;
    return r;
  }

  /** Continue reducing the candidate restored from the journal. */
  resumeReduce(): ReductionResult {
    this.mainLoop();
    return this.finish();
  }

  private finish(): ReductionResult {
    let certificate: CertificateEntry[] | undefined;
    let status: '1-minimal' | 'local-minimum';
    if (this.exhausted) {
      status = 'local-minimum';
    } else {
      const cert = this.certify();
      if (cert === null) {
        status = 'local-minimum';
      } else {
        certificate = cert;
        status = '1-minimal';
      }
    }
    const result: ReductionResult = {
      candidate: cloneInput(this.candidate),
      status,
      budgetExhausted: this.exhausted,
      budget: { max: this.budgetMax, spent: this.oracle.spent },
      ruleVersions: this.oracle.ruleVersions,
      evidence: this.evidence,
      certificate,
      journalPath: this.journalPath,
    };
    this.saveJournal(status, certificate ?? null);
    return result;
  }

  /** Fixpoint over the hierarchical phases, interleaved with certification;
   *  an accepted certificate op restarts the phases (worklist). */
  private mainLoop(): void {
    for (;;) {
      let changed = false;
      if (!this.exhausted) changed = this.attrDdmin() || changed;
      if (!this.exhausted) changed = this.setDdmin() || changed;
      if (!this.exhausted) changed = this.intBoundary() || changed;
      if (this.exhausted) return;
      if (!changed) {
        const applied = this.certifyOnce();
        if (this.exhausted) return;
        if (!applied) return;
      }
    }
  }

  /** Test a candidate: validity first (free), then the judgment difference
   *  (costs budget on cache miss). */
  private test(next: Input, phase: StepEvidence['phase'], op: string): TestResult {
    const v = validate(this.schema, next);
    if (!v.ok) {
      this.record(phase, op, 'rejected-invalid', undefined, v.errors);
      return 'rejected-invalid';
    }
    if (this.oracle.spent >= this.budgetMax && !this.oracle.cached(next)) {
      this.exhausted = true;
      return 'exhausted';
    }
    const pair = this.oracle.evaluate(next);
    if (pair.a === this.target.a && pair.b === this.target.b) {
      this.record(phase, op, 'accepted', pair);
      return 'accepted';
    }
    this.record(phase, op, 'rejected-difference-lost', pair);
    return 'rejected-difference-lost';
  }

  private record(
    phase: StepEvidence['phase'],
    op: string,
    outcome: Outcome,
    verdicts?: VerdictPair,
    errors?: string[],
  ): void {
    this.evidence.push({
      step: this.evidence.length + 1,
      phase,
      op,
      outcome,
      verdicts,
      ruleVersions: this.oracle.ruleVersions,
      budget: {
        max: this.budgetMax,
        spent: this.oracle.spent,
        remaining: Math.max(0, this.budgetMax - this.oracle.spent),
      },
      errors,
    });
  }

  /** Generic delta-debugging minimization. `tryRemove` tests the candidate
   *  with `chunk` removed from the *current* state and must apply accepted
   *  removals itself, so later chunks compose with earlier ones. */
  private ddmin<T>(items: T[], tryRemove: (chunk: T[]) => TestResult): void {
    let current = [...items];
    let n = 2;
    while (current.length >= 2) {
      const chunkSize = Math.ceil(current.length / n);
      let reduced = false;
      for (let i = 0; i < current.length; i += chunkSize) {
        const chunk = current.slice(i, i + chunkSize);
        const r = tryRemove(chunk);
        if (r === 'exhausted') return;
        if (r === 'accepted') {
          const removed = new Set(chunk);
          current = current.filter((x) => !removed.has(x));
          n = Math.max(n - 1, 2);
          reduced = true;
          break;
        }
      }
      if (!reduced) {
        if (n >= current.length) break;
        n = Math.min(current.length, n * 2);
      }
    }
    if (current.length === 1) tryRemove(current); // single-item lists still get one probe
  }

  /** Phase 1: remove irrelevant attributes (cascading dependents). */
  private attrDdmin(): boolean {
    const before = measure(this.schema, this.candidate);
    const removable = Object.keys(this.candidate)
      .filter((k) => !this.schema.attrs[k]?.required)
      .sort();
    this.ddmin(removable, (chunk) => {
      let next = this.candidate;
      for (const attr of chunk) next = cascadeRemove(this.schema, next, attr);
      const r = this.test(next, 'attr-ddmin', `remove attributes {${chunk.join(', ')}}`);
      if (r === 'accepted') {
        this.candidate = next;
        this.saveJournal('in-progress');
      }
      return r;
    });
    return measure(this.schema, this.candidate) < before;
  }

  /** Phase 2: shrink finite-set attributes element-wise. */
  private setDdmin(): boolean {
    const before = measure(this.schema, this.candidate);
    for (const [name, spec] of Object.entries(this.schema.attrs)) {
      if (this.exhausted) break;
      if (spec.kind !== 'set') continue;
      const v = this.candidate[name];
      if (!Array.isArray(v) || v.length === 0) continue;
      this.ddmin([...v], (chunk) => {
        const drop = new Set(chunk);
        const next = cloneInput(this.candidate);
        next[name] = (this.candidate[name] as string[]).filter((el) => !drop.has(el));
        const r = this.test(next, 'set-ddmin', `remove {${chunk.join(', ')}} from ${name}`);
        if (r === 'accepted') {
          this.candidate = next;
          this.saveJournal('in-progress');
        }
        return r;
      });
    }
    return measure(this.schema, this.candidate) < before;
  }

  /** Phase 3: integer boundary search toward the schema minimum. */
  private intBoundary(): boolean {
    const before = measure(this.schema, this.candidate);
    for (const [name, spec] of Object.entries(this.schema.attrs)) {
      if (this.exhausted) break;
      if (spec.kind !== 'int') continue;
      const v = this.candidate[name];
      if (typeof v !== 'number') continue;
      let lo = spec.min;
      let hi = v; // invariant: hi keeps the difference
      while (lo < hi) {
        const mid = Math.floor((lo + hi) / 2);
        const next = { ...this.candidate, [name]: mid };
        const r = this.test(next, 'int-boundary', `set ${name} = ${mid}`);
        if (r === 'exhausted') break;
        if (r === 'accepted') {
          hi = mid;
          this.candidate = next;
          this.saveJournal('in-progress');
        } else {
          lo = mid + 1;
        }
      }
    }
    return measure(this.schema, this.candidate) < before;
  }

  /** Try each declared basic op once; apply the first that preserves the
   *  difference. Returns true when an op was applied. */
  private certifyOnce(): boolean {
    for (const op of enumerateOps(this.schema, this.candidate)) {
      const next = applyOp(this.schema, this.candidate, op);
      if (!next) continue;
      const r = this.test(next, 'certify', describeOp(op));
      if (r === 'exhausted') return false;
      if (r === 'accepted') {
        this.candidate = next;
        this.saveJournal('in-progress');
        return true;
      }
    }
    return false;
  }

  /** Final 1-minimality certificate: every declared basic op must fail.
   *  Returns null when the budget ran out mid-pass (local minimum only). */
  private certify(): CertificateEntry[] | null {
    const cert: CertificateEntry[] = [];
    for (const op of enumerateOps(this.schema, this.candidate)) {
      const next = applyOp(this.schema, this.candidate, op);
      if (!next) continue;
      const r = this.test(next, 'certify', describeOp(op));
      if (r === 'exhausted') return null;
      if (r === 'accepted') return null; // unreachable after fixpoint, but stay honest
      cert.push({ op: describeOp(op), outcome: r });
    }
    return cert;
  }

  private saveJournal(status: Journal['status'], certificate: CertificateEntry[] | null = null): void {
    if (!this.journalPath) return;
    const j: Journal = {
      formatVersion: 1,
      status,
      budget: { max: this.budgetMax, spent: this.oracle.spent },
      ruleVersions: this.oracle.ruleVersions,
      candidate: this.candidate,
      cache: this.oracle.exportCache(),
      evidence: this.evidence,
      certificate,
    };
    writeFileSync(this.journalPath, JSON.stringify(j, null, 2));
  }
}
