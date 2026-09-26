import type { JsonObject, RepairSuggestionResult, RootSchema } from './types.ts';
import { compileSchema, validateInstance } from './validate.ts';
import { buildCandidateGroups } from './candidates.ts';
import { searchRepairs } from './search.ts';

export interface SuggestOptions {
  budget?: number; // 穷举核验的组合数上限，默认 20000
}

export const DEFAULT_BUDGET = 20000;

export function suggestRepairs(
  rawSchema: Record<string, unknown>,
  instance: JsonObject,
  opts: SuggestOptions = {},
): RepairSuggestionResult {
  const schema: RootSchema = compileSchema(rawSchema);
  const { valid, errors } = validateInstance(instance, schema);

  if (valid) {
    return {
      valid: true,
      errors: [],
      status: 'valid',
      groups: [],
      bestPlans: [],
      optimalCost: 0,
      upperBoundCost: 0,
      pendingGroups: [],
      unexploredGroups: [],
      budget: { used: 0, limit: opts.budget ?? DEFAULT_BUDGET },
      combinationsTotal: '1',
      combinationsChecked: 1,
    };
  }

  const groups = buildCandidateGroups(instance, schema, errors);
  const result = searchRepairs(instance, schema, errors, groups, {
    budget: opts.budget ?? DEFAULT_BUDGET,
  });
  return result;
}

export { applyPlan, providePendingValues } from './apply.ts';
export { validateInstance, compileSchema } from './validate.ts';
export { levenshtein } from './candidates.ts';
export type * from './types.ts';
