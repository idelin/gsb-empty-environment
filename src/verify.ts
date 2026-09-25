import { canonicalize, digest } from './canonical.ts';
import type { EvidenceRecord, ReadEntry, TraceNode, Tri } from './evaluate.ts';
import type { Rule } from './rule.ts';
import { evalCondition, normalizePolicy, ruleKey } from './rule.ts';
import type { Snapshot } from './snapshot.ts';

export interface Divergence {
  path: string;
  reason: string;
  expected?: unknown;
  actual?: unknown;
}

export type VerifyStatus = 'verified' | 'unverifiable' | 'rejected';

export interface VerifyReport {
  status: VerifyStatus;
  result?: Tri;
  divergence?: Divergence;
}

function divergence(path: string, reason: string, expected?: unknown, actual?: unknown): Divergence {
  return { path, reason, expected, actual };
}

type Step = { result: Tri; divergence?: undefined } | { result?: undefined; divergence: Divergence };

/**
 * Lockstep replay: walks the recorded trace and the policy together,
 * recomputing every branch and every read from the frozen snapshot.
 * The first divergence is reported with its exact path; the recorded
 * results are never trusted (source claims are not accepted).
 */
class Walker {
  private nextId = 0;
  private readonly memo = new Map<string, { id: number; result: Tri }>();
  readonly reads: string[] = [];

  private readonly snapshot: Snapshot;

  constructor(snapshot: Snapshot) {
    this.snapshot = snapshot;
  }

  verify(rule: Rule, node: TraceNode, path: string): Step {
    const key = ruleKey(rule);
    const id = this.nextId++;
    if (node.id !== id) {
      return { divergence: divergence(`${path}.id`, 'node-id-mismatch', id, node.id) };
    }
    const shared = this.memo.get(key);
    if (shared) {
      if (node.type !== 'ref' || node.target !== shared.id) {
        return {
          divergence: divergence(path, 'shared-subproof-mismatch', { type: 'ref', target: shared.id }, node),
        };
      }
      return { result: shared.result };
    }
    if (node.type === 'ref') {
      return { divergence: divergence(path, 'unexpected-ref', 'first occurrence of subproof', node) };
    }
    let step: Step;
    switch (rule.kind) {
      case 'in':
      case 'int':
        step = this.verifyCondition(rule, node, path);
        break;
      case 'not':
        step = this.verifyNot(rule, node, path);
        break;
      case 'and':
      case 'or':
        step = this.verifyCombinator(rule, node, path);
        break;
    }
    if (step.divergence) return step;
    this.memo.set(key, { id, result: step.result });
    return step;
  }

  private verifyCondition(rule: Extract<Rule, { kind: 'in' | 'int' }>, node: TraceNode, path: string): Step {
    if (node.type !== 'cond') {
      return { divergence: divergence(path, 'node-type-mismatch', 'cond', node.type) };
    }
    if (ruleKey(node.rule) !== ruleKey(rule)) {
      return { divergence: divergence(`${path}.rule`, 'condition-mismatch', rule, node.rule) };
    }
    const entry = this.snapshot[rule.attr];
    if (!entry) {
      return {
        divergence: divergence(`${path}.reads`, 'missing-evidence', `snapshot entry for "${rule.attr}"`, undefined),
      };
    }
    const expectedRead: ReadEntry =
      entry.kind === 'sealed'
        ? { attr: rule.attr, kind: 'sealed', hash: entry.hash }
        : { attr: rule.attr, kind: 'value', value: entry.value };
    this.reads.push(rule.attr);
    if (canonicalize(node.reads) !== canonicalize([expectedRead])) {
      return { divergence: divergence(`${path}.reads`, 'read-mismatch', [expectedRead], node.reads) };
    }
    const expected: Tri = entry.kind === 'sealed' ? 'unverifiable' : evalCondition(rule, entry.value);
    if (node.result !== expected) {
      const reason = entry.kind === 'sealed' ? 'sealed-claim-not-accepted' : 'result-mismatch';
      return { divergence: divergence(`${path}.result`, reason, expected, node.result) };
    }
    return { result: expected };
  }

  private verifyNot(rule: Extract<Rule, { kind: 'not' }>, node: TraceNode, path: string): Step {
    if (node.type !== 'not') {
      return { divergence: divergence(path, 'node-type-mismatch', 'not', node.type) };
    }
    const child = this.verify(rule.child, node.child, `${path}.child`);
    if (child.divergence) return child;
    const expected: Tri = child.result === 'unverifiable' ? 'unverifiable' : !child.result;
    if (node.result !== expected) {
      return { divergence: divergence(`${path}.result`, 'result-mismatch', expected, node.result) };
    }
    return { result: expected };
  }

  private verifyCombinator(rule: Extract<Rule, { kind: 'and' | 'or' }>, node: TraceNode, path: string): Step {
    if (node.type !== rule.kind) {
      return { divergence: divergence(path, 'node-type-mismatch', rule.kind, node.type) };
    }
    let sawUnverifiable = false;
    let decided: Tri | undefined;
    let shortCircuited = false;
    let consumed = rule.children.length;
    for (let i = 0; i < rule.children.length; i++) {
      const childNode = node.children[i];
      if (!childNode) {
        return { divergence: divergence(`${path}.children[${i}]`, 'missing-branch', 'trace branch', undefined) };
      }
      const child = this.verify(rule.children[i], childNode, `${path}.children[${i}]`);
      if (child.divergence) return child;
      if (rule.kind === 'and' && child.result === false) {
        decided = false;
        shortCircuited = i < rule.children.length - 1;
        consumed = i + 1;
        break;
      }
      if (rule.kind === 'or' && child.result === true) {
        decided = true;
        shortCircuited = i < rule.children.length - 1;
        consumed = i + 1;
        break;
      }
      if (child.result === 'unverifiable') sawUnverifiable = true;
    }
    if (node.children.length !== consumed) {
      return {
        divergence: divergence(`${path}.children`, 'branch-count-mismatch', consumed, node.children.length),
      };
    }
    if (node.shortCircuited !== shortCircuited) {
      return {
        divergence: divergence(`${path}.shortCircuited`, 'short-circuit-mismatch', shortCircuited, node.shortCircuited),
      };
    }
    const expected: Tri = decided ?? (sawUnverifiable ? 'unverifiable' : rule.kind === 'and');
    if (node.result !== expected) {
      return { divergence: divergence(`${path}.result`, 'result-mismatch', expected, node.result) };
    }
    return { result: expected };
  }
}

/**
 * Offline replay verification of an evidence record.
 * Uses only the frozen snapshot inside the record; live attributes
 * are never consulted, so later corrections cannot change the outcome.
 */
export function verifyRecord(record: EvidenceRecord): VerifyReport {
  const actualDigest = digest(normalizePolicy(record.policy));
  if (actualDigest !== record.policyDigest) {
    return {
      status: 'rejected',
      divergence: divergence('$.policyDigest', 'policy-digest-mismatch', record.policyDigest, actualDigest),
    };
  }
  const walker = new Walker(record.snapshot);
  const step = walker.verify(record.policy.root, record.trace, '$');
  if (step.divergence) {
    return { status: 'rejected', divergence: step.divergence };
  }
  const readSet = [...new Set(walker.reads)].sort();
  if (canonicalize(readSet) !== canonicalize(record.readSet)) {
    return {
      status: 'rejected',
      divergence: divergence('$.readSet', 'readset-mismatch', readSet, record.readSet),
    };
  }
  if (record.result !== step.result) {
    return {
      status: 'rejected',
      divergence: divergence('$.result', 'result-mismatch', step.result, record.result),
    };
  }
  return {
    status: step.result === 'unverifiable' ? 'unverifiable' : 'verified',
    result: step.result,
  };
}
