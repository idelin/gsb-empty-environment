// 公共入口：parse -> unnest -> format / plan

import { parse } from './parser.ts';
import { unnest } from './unnest.ts';
import { formatSelect } from './format.ts';
import { toPhysicalPlan, formatPlan, type PlanNode } from './planner.ts';
import type { SelectStmt } from './ast.ts';

export interface RewriteResult {
  original: string;
  rewritten: string;
  ast: SelectStmt;
  plan: PlanNode;
  planText: string;
}

export function rewrite(sql: string): RewriteResult {
  const ast = unnest(parse(sql));
  const plan = toPhysicalPlan(ast);
  return {
    original: sql,
    rewritten: formatSelect(ast),
    ast,
    plan,
    planText: formatPlan(plan),
  };
}

export { parse } from './parser.ts';
export { unnest } from './unnest.ts';
export { formatSelect, formatExpr } from './format.ts';
export { toPhysicalPlan, formatPlan } from './planner.ts';
export type { SelectStmt } from './ast.ts';
