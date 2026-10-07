import { Conflict, EvalResult, FieldEvidence, Rule, RuleEvaluation, Snapshot } from './types';
import { deepEqual } from './util';

/**
 * Naive two-phase reference model, written as plainly as possible to serve as
 * an independent cross-check of the engine in `engine.ts`.
 *
 * Phase 1: loop over the rules, evaluate each condition against the input
 *          snapshot, stash the assignments of matched rules. Nothing is
 *          written back, so conditions cannot read this round's assignments.
 * Phase 2: loop over fields; scan all stashed assignments for that field,
 *          find the max priority, and check whether all top-priority values
 *          agree. Agreement picks the value; disagreement is a conflict.
 */
export function evaluateNaive(rules: readonly Rule[], snapshot: Snapshot): EvalResult {
  // ---- Phase 1 ----
  const evaluations: RuleEvaluation[] = [];
  const stash: { field: string; ruleId: string; priority: number; value: unknown }[] = [];
  for (const rule of rules) {
    const matched = rule.condition.evaluate(snapshot);
    evaluations.push({
      ruleId: rule.id,
      priority: rule.priority,
      matched,
      condition: rule.condition.explain(snapshot),
    });
    if (matched) {
      for (const field of Object.keys(rule.assigns)) {
        stash.push({ field, ruleId: rule.id, priority: rule.priority, value: rule.assigns[field] });
      }
    }
  }

  // ---- Phase 2 ----
  const output: Record<string, unknown> = { ...snapshot };
  const fields: FieldEvidence[] = [];
  const conflicts: Conflict[] = [];

  const fieldNames: string[] = [];
  for (const name of Object.keys(snapshot)) if (!fieldNames.includes(name)) fieldNames.push(name);
  for (const entry of stash) if (!fieldNames.includes(entry.field)) fieldNames.push(entry.field);
  fieldNames.sort();

  for (const field of fieldNames) {
    const mine = stash.filter((s) => s.field === field);
    if (mine.length === 0) {
      fields.push({ field, status: 'untouched', candidates: [] });
      continue;
    }
    let topPriority = mine[0]!.priority;
    for (const m of mine) if (m.priority > topPriority) topPriority = m.priority;
    const top = mine.filter((m) => m.priority === topPriority);

    const distinctValues: unknown[] = [];
    for (const t of top) {
      if (!distinctValues.some((v) => deepEqual(v, t.value))) distinctValues.push(t.value);
    }
    const isConflict = distinctValues.length > 1;
    const winnerEntry = top[0]!;

    const candidates = mine
      .slice()
      .sort((a, b) => b.priority - a.priority || (a.ruleId < b.ruleId ? -1 : a.ruleId > b.ruleId ? 1 : 0))
      .map((m) => ({
        ruleId: m.ruleId,
        priority: m.priority,
        value: m.value,
        outcome: (m.priority < topPriority
          ? 'shadowed'
          : isConflict
            ? 'conflict'
            : m.ruleId === winnerEntry.ruleId
              ? 'winner'
              : 'agreed') as FieldEvidence['candidates'][number]['outcome'],
      }));

    if (isConflict) {
      const entries = top
        .filter((t, i) => top.findIndex((u) => deepEqual(u.value, t.value)) === i)
        .map((t) => ({ ruleId: t.ruleId, value: t.value }))
        .sort((a, b) => (a.ruleId < b.ruleId ? -1 : 1));
      fields.push({ field, status: 'conflict', conflict: { priority: topPriority, entries }, candidates });
      conflicts.push({ field, priority: topPriority, entries });
    } else {
      output[field] = winnerEntry.value;
      fields.push({
        field,
        status: 'resolved',
        winner: { ruleId: winnerEntry.ruleId, priority: winnerEntry.priority, value: winnerEntry.value },
        candidates,
      });
    }
  }

  return { output, rules: evaluations, fields, conflicts };
}
