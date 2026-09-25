import { digest } from './canonical.ts';
import type { ConditionRule, Policy, Rule } from './rule.ts';
import { evalCondition, normalizePolicy, ruleKey } from './rule.ts';
import type { Snapshot } from './snapshot.ts';
import { cloneSnapshot } from './snapshot.ts';

/** Three-valued result: sealed evidence makes a conclusion unverifiable. */
export type Tri = boolean | 'unverifiable';

export type ReadEntry =
  | { attr: string; kind: 'value'; value: unknown }
  | { attr: string; kind: 'sealed'; hash: string };

export type TraceNode =
  | { id: number; type: 'cond'; rule: ConditionRule; reads: ReadEntry[]; result: Tri }
  | { id: number; type: 'and' | 'or'; children: TraceNode[]; shortCircuited: boolean; result: Tri }
  | { id: number; type: 'not'; child: TraceNode; result: Tri }
  | { id: number; type: 'ref'; target: number };

/** Frozen evidence bundle: policy digest + snapshot + full evaluation trace. */
export interface EvidenceRecord {
  policyDigest: string;
  policy: Policy;
  snapshot: Snapshot;
  trace: TraceNode;
  readSet: string[];
  result: Tri;
}

export class MissingAttributeError extends Error {
  readonly attr: string;

  constructor(attr: string) {
    super(`attribute "${attr}" is missing from the snapshot`);
    this.name = 'MissingAttributeError';
    this.attr = attr;
  }
}

class Evaluator {
  private nextId = 0;
  private readonly memo = new Map<string, { id: number; result: Tri }>();

  private readonly snapshot: Snapshot;

  constructor(snapshot: Snapshot) {
    this.snapshot = snapshot;
  }

  eval(rule: Rule): { node: TraceNode; result: Tri } {
    const key = ruleKey(rule);
    const shared = this.memo.get(key);
    if (shared) {
      // Shared-subproof compression: identical subrules are proven once.
      return { node: { id: this.nextId++, type: 'ref', target: shared.id }, result: shared.result };
    }
    const id = this.nextId++;
    let node: TraceNode;
    let result: Tri;
    switch (rule.kind) {
      case 'in':
      case 'int': {
        const entry = this.snapshot[rule.attr];
        if (!entry) throw new MissingAttributeError(rule.attr);
        let read: ReadEntry;
        if (entry.kind === 'sealed') {
          read = { attr: rule.attr, kind: 'sealed', hash: entry.hash };
          result = 'unverifiable';
        } else {
          read = { attr: rule.attr, kind: 'value', value: entry.value };
          result = evalCondition(rule, entry.value);
        }
        node = { id, type: 'cond', rule, reads: [read], result };
        break;
      }
      case 'not': {
        const child = this.eval(rule.child);
        result = child.result === 'unverifiable' ? 'unverifiable' : !child.result;
        node = { id, type: 'not', child: child.node, result };
        break;
      }
      case 'and':
      case 'or': {
        const children: TraceNode[] = [];
        let sawUnverifiable = false;
        let shortCircuited = false;
        let decided: Tri | undefined;
        for (let i = 0; i < rule.children.length; i++) {
          const child = this.eval(rule.children[i]);
          children.push(child.node);
          if (rule.kind === 'and' && child.result === false) {
            decided = false;
            shortCircuited = i < rule.children.length - 1;
            break;
          }
          if (rule.kind === 'or' && child.result === true) {
            decided = true;
            shortCircuited = i < rule.children.length - 1;
            break;
          }
          if (child.result === 'unverifiable') sawUnverifiable = true;
        }
        result = decided ?? (sawUnverifiable ? 'unverifiable' : rule.kind === 'and');
        node = { id, type: rule.kind, children, shortCircuited, result };
        break;
      }
    }
    this.memo.set(key, { id, result });
    return { node, result };
  }
}

function collectReadSet(node: TraceNode, into: Set<string>): void {
  switch (node.type) {
    case 'cond':
      for (const read of node.reads) into.add(read.attr);
      break;
    case 'and':
    case 'or':
      for (const child of node.children) collectReadSet(child, into);
      break;
    case 'not':
      collectReadSet(node.child, into);
      break;
    case 'ref':
      break;
  }
}

/**
 * Evaluate a policy against a snapshot and freeze the evidence.
 * The snapshot is deep-copied: later real-time attribute changes
 * cannot affect this historical record.
 */
export function recordEvidence(policy: Policy, snapshot: Snapshot): EvidenceRecord {
  const frozen = cloneSnapshot(snapshot);
  const evaluator = new Evaluator(frozen);
  const { node, result } = evaluator.eval(policy.root);
  const readSet = new Set<string>();
  collectReadSet(node, readSet);
  return {
    policyDigest: digest(normalizePolicy(policy)),
    policy: structuredClone(policy),
    snapshot: frozen,
    trace: node,
    readSet: [...readSet].sort(),
    result,
  };
}
