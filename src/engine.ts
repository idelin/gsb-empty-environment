import {
  CandidateNode,
  Conflict,
  CandidateOutcome,
  EvalResult,
  FieldEvidence,
  Rule,
  RuleEvaluation,
  Snapshot,
} from './types';
import { deepEqual, show } from './util';

interface Assignment {
  readonly ruleId: string;
  readonly priority: number;
  readonly value: unknown;
}

/**
 * Two-phase evaluation.
 *
 * Phase 1 (snapshot judgement): every rule condition is evaluated against the
 * pristine input snapshot. Assignments are collected but never written back,
 * so no rule can observe another rule's writes from this round.
 *
 * Phase 2 (per-field resolution): for each assigned field, the highest
 * priority wins. Ties at the top priority with equal values agree; ties with
 * different values are reported as conflicts and the field keeps its
 * snapshot value.
 */
export function evaluate(rules: readonly Rule[], snapshot: Snapshot): EvalResult {
  // Phase 1: judge all conditions against the immutable snapshot.
  const evaluations: RuleEvaluation[] = [];
  const pending = new Map<string, Assignment[]>();
  for (const rule of rules) {
    const matched = rule.condition.evaluate(snapshot);
    evaluations.push({
      ruleId: rule.id,
      priority: rule.priority,
      matched,
      condition: rule.condition.explain(snapshot),
    });
    if (!matched) continue;
    for (const field of Object.keys(rule.assigns)) {
      const list = pending.get(field) ?? [];
      list.push({ ruleId: rule.id, priority: rule.priority, value: rule.assigns[field] });
      pending.set(field, list);
    }
  }

  // Phase 2: resolve each field independently by priority.
  const output: Record<string, unknown> = { ...snapshot };
  const fields: FieldEvidence[] = [];
  const conflicts: Conflict[] = [];

  const assignedFields = new Set<string>([...Object.keys(snapshot), ...pending.keys()]);
  for (const field of [...assignedFields].sort()) {
    const assignments = pending.get(field);
    if (!assignments || assignments.length === 0) {
      fields.push({ field, status: 'untouched', candidates: [] });
      continue;
    }

    const topPriority = Math.max(...assignments.map((a) => a.priority));
    const top = assignments.filter((a) => a.priority === topPriority);
    const distinct: Assignment[] = [];
    for (const a of top) {
      if (!distinct.some((d) => deepEqual(d.value, a.value))) distinct.push(a);
    }
    const isConflict = distinct.length > 1;
    const winner = isConflict ? undefined : distinct[0]!;

    const outcomeOf = (a: Assignment): CandidateOutcome => {
      if (a.priority < topPriority) return 'shadowed';
      if (isConflict) return 'conflict';
      return a.ruleId === winner!.ruleId ? 'winner' : 'agreed';
    };
    const candidates: CandidateNode[] = assignments
      .slice()
      .sort((a, b) => b.priority - a.priority || (a.ruleId < b.ruleId ? -1 : a.ruleId > b.ruleId ? 1 : 0))
      .map((a) => ({ ruleId: a.ruleId, priority: a.priority, value: a.value, outcome: outcomeOf(a) }));

    if (isConflict) {
      // Same top priority, different values: conflict. Snapshot value stands.
      const entries = distinct
        .map((d) => ({ ruleId: d.ruleId, value: d.value }))
        .sort((a, b) => (a.ruleId < b.ruleId ? -1 : 1));
      fields.push({
        field,
        status: 'conflict',
        conflict: { priority: topPriority, entries },
        candidates,
      });
      conflicts.push({ field, priority: topPriority, entries });
      continue;
    }

    output[field] = winner!.value;
    fields.push({
      field,
      status: 'resolved',
      winner: { ruleId: winner!.ruleId, priority: winner!.priority, value: winner!.value },
      candidates,
    });
  }

  return { output, rules: evaluations, fields, conflicts };
}

/** Render the per-field evidence trees as indented text. */
export function renderEvidence(result: EvalResult): string {
  const lines: string[] = [];
  for (const field of result.fields) {
    if (field.status === 'untouched') {
      lines.push(`${field.field}: untouched (no rule assigned it)`);
      continue;
    }
    if (field.status === 'conflict') {
      lines.push(`${field.field}: CONFLICT at priority ${field.conflict!.priority}`);
    } else {
      lines.push(
        `${field.field}: ${show(field.winner!.value)}  <- ${field.winner!.ruleId} (priority ${field.winner!.priority})`,
      );
    }
    for (const c of field.candidates) {
      lines.push(`  [${c.outcome}] ${c.ruleId} priority=${c.priority} value=${show(c.value)}`);
    }
  }
  return lines.join('\n');
}
