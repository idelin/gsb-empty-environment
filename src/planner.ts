/**
 * Physical planner: turns a RewriteResult into an executable plan tree.
 * Equi-key SEMI/ANTI joins are planned as hash joins (build side = inner
 * relation, probed once per outer row), which removes the O(outer x inner)
 * correlated nested-loop evaluation.
 */

import { Expr, exprToSql, JoinKey, RewriteResult } from './decorrelate';
import { SelectItem } from './ast';

export type PlanOp =
  | 'SeqScan'
  | 'Filter'
  | 'HashSemiJoin'
  | 'HashAntiJoin'
  | 'NestedLoopSemiJoin'
  | 'NestedLoopAntiJoin'
  | 'Project';

export interface PlanNode {
  op: PlanOp;
  table?: string;
  alias?: string;
  keys?: JoinKey[];
  filters?: Expr[];
  residual?: Expr[];
  select?: SelectItem[];
  children: PlanNode[];
}

export function buildPlan(result: RewriteResult): PlanNode {
  if (!result.outer) {
    throw new Error('cannot plan: query was not rewritten');
  }
  const outer = result.outer;
  let node: PlanNode = {
    op: 'SeqScan',
    table: outer.table,
    alias: outer.alias,
    children: [],
  };

  for (const join of result.joins) {
    let inner: PlanNode = {
      op: 'SeqScan',
      table: join.table,
      alias: join.alias,
      children: [],
    };
    if (join.filters.length > 0) {
      inner = { op: 'Filter', alias: join.alias, filters: join.filters, children: [inner] };
    }
    let op: PlanOp;
    if (join.keys.length > 0) {
      op = join.type === 'SEMI' ? 'HashSemiJoin' : 'HashAntiJoin';
    } else {
      op = join.type === 'SEMI' ? 'NestedLoopSemiJoin' : 'NestedLoopAntiJoin';
    }
    // children[0] = probe (outer) side, children[1] = build (inner) side
    node = { op, keys: join.keys, children: [node, inner] };
  }

  if (result.residual.length > 0) {
    node = { op: 'Filter', alias: outer.alias, residual: result.residual, children: [node] };
  }

  node = { op: 'Project', select: result.query.select, children: [node] };
  return node;
}

export function explain(node: PlanNode, indent = ''): string {
  const lines: string[] = [];
  const head = (label: string): void => {
    lines.push(`${indent}${label}`);
  };
  switch (node.op) {
    case 'SeqScan':
      head(`SeqScan on ${node.table} ${node.alias}`);
      break;
    case 'Filter': {
      const preds = (node.filters ?? node.residual ?? []).map(exprToSql).join(' AND ');
      head(`Filter: ${preds}`);
      break;
    }
    case 'HashSemiJoin':
    case 'HashAntiJoin':
    case 'NestedLoopSemiJoin':
    case 'NestedLoopAntiJoin': {
      const keys = (node.keys ?? []).map((k) => `${k.outer} = ${k.inner}`).join(', ');
      head(`${node.op}${keys ? `  [${keys}]` : ''}`);
      break;
    }
    case 'Project':
      head('Project');
      break;
  }
  for (const child of node.children) {
    lines.push(explain(child, indent + '  '));
  }
  return lines.join('\n');
}
