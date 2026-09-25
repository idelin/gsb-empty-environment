import { Journal } from './journal.ts';
import { planAsWritten, planPolicy } from './planner.ts';
import type { Plan } from './planner.ts';
import { compare, validateExpr } from './policy.ts';
import type { Expr } from './policy.ts';
import { Scheduler } from './scheduler.ts';
import {
  CancelledError,
  PolicyError,
  VersionConflictError,
} from './types.ts';
import type {
  AttrKey,
  AttributeSource,
  DecisionRecord,
  EvidenceEntry,
  ReadSession,
  Subject,
  Version,
} from './types.ts';

export type OrderMode = 'planned' | 'as-written';

export interface EvaluateInput {
  readonly subject: Subject;
  readonly permission: string;
  readonly signal?: AbortSignal;
}

export interface EvaluatorOptions {
  readonly fetchTimeoutMs?: number;
  readonly maxVersionRetries?: number;
  readonly journal?: Journal;
}

class EvalSession implements ReadSession {
  readonly versions = new Map<string, Version>();
  readonly values = new Map<AttrKey, unknown>();
  readonly pendingReads = new Map<AttrKey, Promise<unknown>>();
  readonly evidence: EvidenceEntry[] = [];
  readonly subject: Subject;
  readonly permission: string;
  readonly signal: AbortSignal;
  constructor(subject: Subject, permission: string, signal: AbortSignal) {
    this.subject = subject;
    this.permission = permission;
    this.signal = signal;
  }
}

function throwIfAborted(signal: AbortSignal): void {
  if (signal.aborted) {
    throw new CancelledError();
  }
}

export class PolicyEvaluator {
  private readonly scheduler: Scheduler;
  private readonly journal: Journal;
  private readonly knownAttrs: Set<AttrKey>;
  private readonly sourcesByAttr: Map<AttrKey, AttributeSource>;
  private readonly maxVersionRetries: number;

  constructor(sources: readonly AttributeSource[], opts: EvaluatorOptions = {}) {
    this.sourcesByAttr = new Map();
    for (const s of sources) {
      for (const a of s.attrs) {
        if (this.sourcesByAttr.has(a)) {
          throw new PolicyError(`attribute "${a}" is provided by more than one source`);
        }
        this.sourcesByAttr.set(a, s);
      }
    }
    this.knownAttrs = new Set(this.sourcesByAttr.keys());
    this.scheduler = new Scheduler(this.sourcesByAttr, { fetchTimeoutMs: opts.fetchTimeoutMs });
    this.journal = opts.journal ?? new Journal();
    this.maxVersionRetries = opts.maxVersionRetries ?? 3;
  }

  records(): readonly DecisionRecord[] {
    return this.journal.all();
  }

  planOf(expr: Expr, mode: OrderMode = 'planned'): Plan {
    validateExpr(expr, this.knownAttrs);
    return mode === 'planned'
      ? planPolicy(expr, this.sourcesByAttr)
      : planAsWritten(expr, this.sourcesByAttr);
  }

  async evaluate(
    expr: Expr,
    input: EvaluateInput,
    mode: OrderMode = 'planned',
  ): Promise<DecisionRecord> {
    const plan = this.planOf(expr, mode);
    const signal = input.signal ?? new AbortController().signal;
    for (let attempt = 1; ; attempt++) {
      const session = new EvalSession(input.subject, input.permission, signal);
      try {
        // Must-read attributes are prefetched in one batch per source;
        // optional attributes are fetched lazily, only if the decision path
        // actually reaches them.
        await Promise.all([...plan.mustRead].map((k) => this.scheduler.read(session, k)));
        const result = await this.evalExpr(plan.ordered, session);
        return this.journal.commit({
          decision: result ? 'allow' : 'deny',
          subjectId: input.subject.id,
          permission: input.permission,
          evidence: session.evidence,
          attempts: attempt,
        });
      } catch (err) {
        if (err instanceof VersionConflictError && attempt <= this.maxVersionRetries) {
          this.scheduler.invalidate(input.permission, input.subject.id, err.source);
          continue;
        }
        throw err;
      }
    }
  }

  private async evalExpr(e: Expr, s: EvalSession): Promise<boolean> {
    switch (e.kind) {
      case 'cmp': {
        const v = await this.scheduler.read(s, e.attr);
        if (typeof v !== 'number' || !Number.isInteger(v)) {
          throw new PolicyError(`attribute "${e.attr}" did not resolve to an integer`);
        }
        return compare(v, e.op, e.value);
      }
      case 'in': {
        const v = await this.scheduler.read(s, e.attr);
        return e.values.some((member) => member === v);
      }
      case 'not':
        return !(await this.evalExpr(e.arg, s));
      case 'and':
        for (const arg of e.args) {
          throwIfAborted(s.signal);
          if (!(await this.evalExpr(arg, s))) {
            return false;
          }
        }
        return true;
      case 'or':
        for (const arg of e.args) {
          throwIfAborted(s.signal);
          if (await this.evalExpr(arg, s)) {
            return true;
          }
        }
        return false;
    }
  }
}
