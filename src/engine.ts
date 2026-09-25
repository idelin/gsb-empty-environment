import { compile, type Dag, type DagNode } from './dag.ts';
import { toString, type Expr } from './expr.ts';
import { partialEval } from './partial.ts';
import type { AttrProvider, InMemoryAttrStore, ReadResult } from './store.ts';
import { triAnd, triNot, triOr, type Tri } from './tri.ts';

/** Explanation tree recorded during evaluation (pre-pruning). */
export interface Explain {
  readonly node: string;
  readonly result: Tri;
  readonly attr?: string;
  readonly value?: unknown;
  readonly children?: Explain[];
}

export interface AttrEvidence {
  readonly attr: string;
  readonly condition: string;
  readonly value: unknown;
  readonly result: Tri;
}

export type EvalStatus = 'ok' | 'conflict' | 'error';

export interface Decision {
  readonly status: EvalStatus;
  readonly ruleId: string;
  readonly subject: string;
  readonly decision?: Tri;
  readonly explanation?: Explain;
  readonly evidence?: readonly AttrEvidence[];
  readonly attempts: number;
  readonly fromCache: boolean;
  readonly error?: string;
}

interface CacheEntry {
  readonly ruleVersion: number;
  readonly decision: Tri;
  readonly explanation: Explain;
  readonly evidence: readonly AttrEvidence[];
  readonly deps: ReadonlySet<string>;
  readonly versions: ReadonlyMap<string, number>;
}

interface RuleEntry {
  readonly expr: Expr;
  readonly dag: Dag;
  version: number;
}

export interface EngineOptions {
  readonly maxRetries?: number;
  readonly cache?: boolean;
}

const cacheKey = (ruleId: string, subject: string) => `${ruleId}::${subject}`;

export class PolicyEngine {
  private readonly provider: AttrProvider;
  private readonly maxRetries: number;
  private readonly cacheEnabled: boolean;
  private readonly rules = new Map<string, RuleEntry>();
  private readonly cache = new Map<string, CacheEntry>();

  constructor(provider: AttrProvider, opts: EngineOptions = {}) {
    this.provider = provider;
    this.maxRetries = opts.maxRetries ?? 2;
    this.cacheEnabled = opts.cache ?? true;
  }

  bindStore(store: InMemoryAttrStore): this {
    store.onChange((subject, attr) => this.notifyAttributeChanged(subject, attr));
    return this;
  }

  setRule(ruleId: string, expr: Expr): void {
    const prev = this.rules.get(ruleId);
    this.rules.set(ruleId, { expr, dag: compile(expr), version: (prev?.version ?? 0) + 1 });
    for (const k of [...this.cache.keys()]) {
      if (k.startsWith(`${ruleId}::`)) this.cache.delete(k);
    }
  }

  removeRule(ruleId: string): void {
    this.rules.delete(ruleId);
    for (const k of [...this.cache.keys()]) {
      if (k.startsWith(`${ruleId}::`)) this.cache.delete(k);
    }
  }

  notifyAttributeChanged(subject: string, attr: string): void {
    for (const [k, entry] of [...this.cache.entries()]) {
      if (k.endsWith(`::${subject}`) && entry.deps.has(attr)) this.cache.delete(k);
    }
  }

  get cacheSize(): number {
    return this.cache.size;
  }

  dagOf(ruleId: string): Dag {
    const rule = this.rules.get(ruleId);
    if (!rule) throw new Error(`unknown rule: ${ruleId}`);
    return rule.dag;
  }

  residual(ruleId: string, known: ReadonlyMap<string, unknown>): { expr: Expr; text: string } {
    const rule = this.rules.get(ruleId);
    if (!rule) throw new Error(`unknown rule: ${ruleId}`);
    const expr = partialEval(rule.expr, known);
    return { expr, text: toString(expr) };
  }
  async evaluate(ruleId: string, subject: string, signal?: AbortSignal): Promise<Decision> {
    const rule = this.rules.get(ruleId);
    if (!rule) {
      return { status: 'error', ruleId, subject, attempts: 0, fromCache: false, error: `unknown rule: ${ruleId}` };
    }
    const key = cacheKey(ruleId, subject);
    if (this.cacheEnabled) {
      const hit = await this.lookupCache(key, rule.version, subject);
      if (hit) return hit;
    }

    let lastError: unknown;
    let attempts = 0;
    for (let attempt = 1; attempt <= this.maxRetries + 1; attempt++) {
      attempts = attempt;
      try {
        const outcome = await this.evalNode(rule.dag.root, subject, new Map(), signal);
        const consistent = await this.checkVersions(subject, outcome.reads);
        if (!consistent) continue;
        const explanation = minSlice(outcome.explain);
        const evidence = collectEvidence(explanation);
        if (this.cacheEnabled) {
          this.cache.set(key, {
            ruleVersion: rule.version,
            decision: outcome.value,
            explanation,
            evidence,
            deps: new Set(outcome.reads.keys()),
            versions: new Map([...outcome.reads].map(([a, r]) => [a, r.version])),
          });
        }
        return {
          status: 'ok',
          ruleId,
          subject,
          decision: outcome.value,
          explanation,
          evidence,
          attempts: attempt,
          fromCache: false,
        };
      } catch (err) {
        lastError = err;
        break;
      }
    }
    if (lastError !== undefined) {
      return {
        status: 'error',
        ruleId,
        subject,
        attempts,
        fromCache: false,
        error: lastError instanceof Error ? lastError.message : String(lastError),
      };
    }
    return {
      status: 'conflict',
      ruleId,
      subject,
      attempts,
      fromCache: false,
      error: 'attribute versions kept changing during evaluation',
    };
  }

  private async lookupCache(key: string, ruleVersion: number, subject: string): Promise<Decision | null> {
    const entry = this.cache.get(key);
    if (!entry || entry.ruleVersion !== ruleVersion) return null;
    const sep = key.lastIndexOf('::');
    const ruleId = key.slice(0, sep);
    const now = await this.provider.versions(subject, [...entry.deps]);
    for (const [attr, v] of entry.versions) {
      if (now.get(attr) !== v) return null;
    }
    return {
      status: 'ok',
      ruleId,
      subject,
      decision: entry.decision,
      explanation: entry.explanation,
      evidence: entry.evidence,
      attempts: 0,
      fromCache: true,
    };
  }

  private async checkVersions(subject: string, reads: ReadonlyMap<string, ReadResult>): Promise<boolean> {
    const now = await this.provider.versions(subject, [...reads.keys()]);
    for (const [attr, r] of reads) {
      if (now.get(attr) !== r.version) return false;
    }
    return true;
  }

  private async evalNode(
    node: DagNode,
    subject: string,
    readCache: Map<string, ReadResult>,
    signal?: AbortSignal,
  ): Promise<{ value: Tri; reads: Map<string, ReadResult>; explain: Explain }> {
    const e = node.expr;
    switch (e.kind) {
      case 'lit':
        return { value: e.value, reads: readCache, explain: { node: toString(e), result: e.value } };
      case 'cmp':
      case 'in':
      case 'range': {
        if (signal?.aborted) throw new Error('evaluation aborted');
        let r = readCache.get(e.attr);
        if (!r) {
          r = await this.provider.read(subject, e.attr, signal);
          readCache.set(e.attr, r);
        }
        const value = leafValue(e, r.value);
        return {
          value,
          reads: readCache,
          explain: { node: toString(e), result: value, attr: e.attr, value: r.value },
        };
      }
      case 'not': {
        const c = await this.evalNode(node.children[0]!, subject, readCache, signal);
        const value = triNot(c.value);
        return {
          value,
          reads: readCache,
          explain: { node: 'NOT', result: value, children: [c.explain] },
        };
      }
      case 'and':
      case 'or': {
        const isAnd = e.kind === 'and';
        let acc: Tri = isAnd ? 'true' : 'false';
        const children: Explain[] = [];
        for (const child of node.children) {
          if (signal?.aborted) throw new Error('evaluation aborted');
          const c = await this.evalNode(child, subject, readCache, signal);
          children.push(c.explain);
          acc = isAnd ? triAnd(acc, c.value) : triOr(acc, c.value);
          if (isAnd && acc === 'false') break;
          if (!isAnd && acc === 'true') break;
        }
        return {
          value: acc,
          reads: readCache,
          explain: { node: isAnd ? 'AND' : 'OR', result: acc, children },
        };
      }
    }
  }
}

function leafValue(e: Extract<Expr, { kind: 'cmp' | 'in' | 'range' }>, v: unknown): Tri {
  if (typeof v !== 'number' || !Number.isInteger(v)) return 'unknown';
  switch (e.kind) {
    case 'cmp':
      return (e.op === 'eq' ? v === e.value : v !== e.value) ? 'true' : 'false';
    case 'in':
      return e.set.includes(v) ? 'true' : 'false';
    case 'range':
      return (e.min === null || v >= e.min) && (e.max === null || v <= e.max) ? 'true' : 'false';
  }
}

export function minSlice(ex: Explain): Explain {
  if (!ex.children || ex.children.length === 0) return ex;
  if (ex.node === 'NOT') {
    return { ...ex, children: [minSlice(ex.children[0]!)] };
  }
  if (ex.node === 'AND') {
    if (ex.result === 'false') {
      const culprit = ex.children.find((c) => c.result === 'false');
      return { ...ex, children: culprit ? [minSlice(culprit)] : [] };
    }
    if (ex.result === 'unknown') {
      return { ...ex, children: ex.children.filter((c) => c.result === 'unknown').map(minSlice) };
    }
    return { ...ex, children: ex.children.map(minSlice) };
  }
  if (ex.result === 'true') {
    const culprit = ex.children.find((c) => c.result === 'true');
    return { ...ex, children: culprit ? [minSlice(culprit)] : [] };
  }
  if (ex.result === 'unknown') {
    return { ...ex, children: ex.children.filter((c) => c.result === 'unknown').map(minSlice) };
  }
  return { ...ex, children: ex.children.map(minSlice) };
}

export function collectEvidence(ex: Explain, into: AttrEvidence[] = []): AttrEvidence[] {
  if (ex.attr !== undefined) {
    into.push({ attr: ex.attr, condition: ex.node, value: ex.value, result: ex.result });
  }
  for (const c of ex.children ?? []) collectEvidence(c, into);
  return into;
}
