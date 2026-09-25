import { hashRule } from './rule.ts';
import type { IntOp, Rule } from './rule.ts';
import { isSealed } from './snapshot.ts';
import type { AttributeSnapshot } from './snapshot.ts';
import type { EvaluationTrace, Outcome, ReadRecord, TraceNode } from './trace.ts';

export class MissingAttributeError extends Error {
  readonly attr: string;
  constructor(attr: string) {
    super(`attribute "${attr}" is absent from the snapshot`);
    this.name = 'MissingAttributeError';
    this.attr = attr;
  }
}

function compareInts(actual: number, op: IntOp, expected: number): boolean {
  switch (op) {
    case 'eq': return actual === expected;
    case 'ne': return actual !== expected;
    case 'lt': return actual < expected;
    case 'le': return actual <= expected;
    case 'gt': return actual > expected;
    case 'ge': return actual >= expected;
  }
}

export interface EvaluationResult {
  outcome: Outcome;
  trace: EvaluationTrace;
}

/**
 * Evaluates a rule against an explicit attribute snapshot and records the full
 * evaluation trace: every branch, every actual read, short-circuit skips.
 * Identical sub-rules are evaluated once and share one compressed subproof.
 */
export function evaluate(rule: Rule, snapshot: AttributeSnapshot): EvaluationResult {
  const subproofs: Record<string, TraceNode> = {};
  const flatReads: ReadRecord[] = [];
  const memo = new Map<string, { hash: string; outcome: Outcome }>();

  function readAttr(attr: string): number | 'sealed' {
    if (!(attr in snapshot)) throw new MissingAttributeError(attr);
    const raw = snapshot[attr];
    const value: number | 'sealed' = isSealed(raw) ? 'sealed' : raw;
    flatReads.push({ attr, value });
    return value;
  }

  function evalNode(current: Rule): { hash: string; outcome: Outcome } {
    const hash = hashRule(current);
    const hit = memo.get(hash);
    if (hit) return hit;

    let node: TraceNode;
    let outcome: Outcome;

    switch (current.kind) {
      case 'intCmp': {
        const value = readAttr(current.attr);
        outcome = value === 'sealed' ? 'unknown' : compareInts(value, current.op, current.value) ? 'true' : 'false';
        node = { ruleHash: hash, outcome, reads: [{ attr: current.attr, value }], children: [], skipped: [] };
        break;
      }
      case 'inSet': {
        const value = readAttr(current.attr);
        outcome = value === 'sealed' ? 'unknown' : current.set.includes(value) ? 'true' : 'false';
        node = { ruleHash: hash, outcome, reads: [{ attr: current.attr, value }], children: [], skipped: [] };
        break;
      }
      case 'not': {
        const child = evalNode(current.arg);
        outcome = child.outcome === 'unknown' ? 'unknown' : child.outcome === 'true' ? 'false' : 'true';
        node = { ruleHash: hash, outcome, reads: [], children: [child.hash], skipped: [] };
        break;
      }
      case 'and': {
        const children: string[] = [];
        const skipped: string[] = [];
        let sawUnknown = false;
        let decided: Outcome | null = null;
        for (const arg of current.args) {
          if (decided !== null) {
            skipped.push(hashRule(arg));
            continue;
          }
          const child = evalNode(arg);
          children.push(child.hash);
          if (child.outcome === 'false') decided = 'false';
          else if (child.outcome === 'unknown') sawUnknown = true;
        }
        outcome = decided ?? (sawUnknown ? 'unknown' : 'true');
        node = { ruleHash: hash, outcome, reads: [], children, skipped };
        break;
      }
      case 'or': {
        const children: string[] = [];
        const skipped: string[] = [];
        let sawUnknown = false;
        let decided: Outcome | null = null;
        for (const arg of current.args) {
          if (decided !== null) {
            skipped.push(hashRule(arg));
            continue;
          }
          const child = evalNode(arg);
          children.push(child.hash);
          if (child.outcome === 'true') decided = 'true';
          else if (child.outcome === 'unknown') sawUnknown = true;
        }
        outcome = decided ?? (sawUnknown ? 'unknown' : 'false');
        node = { ruleHash: hash, outcome, reads: [], children, skipped };
        break;
      }
    }

    subproofs[hash] = node;
    const entry = { hash, outcome };
    memo.set(hash, entry);
    return entry;
  }

  const root = evalNode(rule);
  return { outcome: root.outcome, trace: { root: root.hash, subproofs, reads: flatReads } };
}
