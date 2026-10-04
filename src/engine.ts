import { evalCondition } from './conditions';
import { checkPolicy, decide, evalRules, interpret, PolicyIssue } from './interpreter';
import { Action, Decision, FileFacts, Rule } from './types';

export class PolicyError extends Error {
  constructor(public readonly issues: PolicyIssue[]) {
    super('invalid policy:\n' + issues.map((i) => `  [${i.kind}] ${i.message}`).join('\n'));
    this.name = 'PolicyError';
  }
}

interface FileRecord {
  facts: FileFacts;
  decision: Decision;
  /** Ids of rules evaluating `true` for this file. */
  trueRuleIds: string[];
  /** Ids of rules evaluating `unknown` for this file. */
  unknownRuleIds: string[];
}

export interface UpdatePlan {
  /** Files that newly require download/backup. */
  downloads: string[];
  /** Files whose backup must be revoked (no longer `backup`). */
  revocations: string[];
  /** Files whose outcome is now unknown. */
  unknowns: string[];
  /** Files actually re-evaluated (the possibly-affected set). */
  recomputed: string[];
}

function stableStringify(value: unknown): string {
  if (Array.isArray(value)) {
    return '[' + value.map(stableStringify).join(',') + ']';
  }
  if (value !== null && typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>)
      .filter(([, v]) => v !== undefined)
      .sort(([a], [b]) => (a < b ? -1 : 1))
      .map(([k, v]) => JSON.stringify(k) + ':' + stableStringify(v));
    return '{' + entries.join(',') + '}';
  }
  return JSON.stringify(value);
}

function isBackup(d: Decision): boolean {
  return d.kind === 'decided' && d.action === 'backup';
}

/**
 * Holds the current policy plus per-file evaluation records so that a
 * policy update only re-interprets files that could possibly be affected.
 */
export class PolicyEngine {
  private rules: Rule[];
  private readonly records = new Map<string, FileRecord>();

  constructor(rules: Rule[], private readonly defaultAction: Action = 'exclude') {
    PolicyEngine.assertValid(rules);
    this.rules = rules.map((r) => ({ ...r, condition: { ...r.condition } }));
  }

  private static assertValid(rules: Rule[]): void {
    const errors = checkPolicy(rules).filter((i) => i.severity === 'error');
    if (errors.length > 0) throw new PolicyError(errors);
  }

  /** Warnings (e.g. shadowed rules) for the current policy. */
  warnings(): PolicyIssue[] {
    return checkPolicy(this.rules).filter((i) => i.severity === 'warning');
  }

  getRules(): readonly Rule[] {
    return this.rules;
  }

  /** Insert or replace file facts and evaluate them against the current policy. */
  upsertFiles(files: FileFacts[]): void {
    for (const facts of files) {
      const evals = evalRules(this.rules, facts);
      this.records.set(facts.path, {
        facts,
        decision: decide(evals, this.defaultAction),
        trueRuleIds: evals.filter((e) => e.result === true).map((e) => e.rule.id),
        unknownRuleIds: evals.filter((e) => e.result === 'unknown').map((e) => e.rule.id),
      });
    }
  }

  removeFile(path: string): void {
    this.records.delete(path);
  }

  getDecision(path: string): Decision | undefined {
    return this.records.get(path)?.decision;
  }

  /**
   * Apply a new policy, recomputing only files that could be affected:
   *  - files whose previous evaluation touched a removed or changed rule;
   *  - files whose known facts do not definitely rule out an added/changed rule.
   * Returns the resulting download / revocation plan.
   */
  updatePolicy(newRules: Rule[]): UpdatePlan {
    PolicyEngine.assertValid(newRules);

    const oldById = new Map(this.rules.map((r) => [r.id, r]));
    const newById = new Map(newRules.map((r) => [r.id, r]));

    const removedOrChanged = new Set<string>();
    const addedOrChanged: Rule[] = [];

    for (const [id, oldRule] of oldById) {
      const next = newById.get(id);
      if (!next || stableStringify(next) !== stableStringify(oldRule)) {
        removedOrChanged.add(id);
      }
    }
    for (const [id, next] of newById) {
      const prev = oldById.get(id);
      if (!prev || stableStringify(prev) !== stableStringify(next)) {
        addedOrChanged.push(next);
      }
    }

    const affected = new Set<string>();
    if (removedOrChanged.size > 0 || addedOrChanged.length > 0) {
      for (const [path, record] of this.records) {
        if (record.trueRuleIds.some((id) => removedOrChanged.has(id)) ||
            record.unknownRuleIds.some((id) => removedOrChanged.has(id))) {
          affected.add(path);
          continue;
        }
        // A file can only be newly affected by an added/changed rule if the
        // rule's conditions do not definitely fail on the known facts.
        for (const rule of addedOrChanged) {
          if (evalCondition(rule.condition, record.facts) !== false) {
            affected.add(path);
            break;
          }
        }
      }
    }

    this.rules = newRules.map((r) => ({ ...r, condition: { ...r.condition } }));

    const plan: UpdatePlan = { downloads: [], revocations: [], unknowns: [], recomputed: [] };
    const sortedAffected = [...affected].sort();
    for (const path of sortedAffected) {
      const record = this.records.get(path)!;
      const before = record.decision;
      const evals = evalRules(this.rules, record.facts);
      const after = decide(evals, this.defaultAction);
      this.records.set(path, {
        facts: record.facts,
        decision: after,
        trueRuleIds: evals.filter((e) => e.result === true).map((e) => e.rule.id),
        unknownRuleIds: evals.filter((e) => e.result === 'unknown').map((e) => e.rule.id),
      });

      plan.recomputed.push(path);
      if (!isBackup(before) && isBackup(after)) plan.downloads.push(path);
      if (isBackup(before) && !isBackup(after)) plan.revocations.push(path);
      if (after.kind === 'unknown') plan.unknowns.push(path);
    }
    return plan;
  }

  /** Full-scan reference: recompute every file from scratch. */
  fullScanReference(): Map<string, Decision> {
    const out = new Map<string, Decision>();
    for (const [path, record] of this.records) {
      out.set(path, interpret(this.rules, record.facts, this.defaultAction));
    }
    return out;
  }

  /**
   * Cross-check incremental state against the full-scan reference.
   * Returns a list of mismatch descriptions (empty when consistent).
   */
  verifyAgainstReference(): string[] {
    const reference = this.fullScanReference();
    const mismatches: string[] = [];
    for (const [path, expected] of reference) {
      const actual = this.records.get(path)!.decision;
      if (stableStringify(actual) !== stableStringify(expected)) {
        mismatches.push(
          `${path}: incremental=${stableStringify(actual)} reference=${stableStringify(expected)}`,
        );
      }
    }
    return mismatches;
  }
}
