/** Public API: parse -> decorrelate -> plan -> (optionally) execute. */

import { parse } from './parser';
import { decorrelate, renderRewrittenSql, RewriteResult } from './decorrelate';
import { buildPlan, explain, PlanNode } from './planner';

export * from './ast';
export * from './parser';
export * from './decorrelate';
export * from './planner';
export * from './engine';

export interface UnnestResult {
  original: string;
  rewritten: string;
  changed: boolean;
  result: RewriteResult;
  plan: PlanNode | null;
  explain: string;
}

export function unnest(sql: string): UnnestResult {
  const stmt = parse(sql);
  const result = decorrelate(stmt);
  const rewritten = renderRewrittenSql(result);
  const plan = result.changed ? buildPlan(result) : null;
  return {
    original: sql,
    rewritten,
    changed: result.changed,
    result,
    plan,
    explain: plan ? explain(plan) : '(no rewrite applied)',
  };
}
