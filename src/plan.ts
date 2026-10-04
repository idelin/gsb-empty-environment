// 查询计划（逻辑算子树）与执行器。
import type { Expr, Row } from './expr.js';
import { and, conjuncts, formatExpr } from './expr.js';
import type { JoinType, Table } from './operators.js';
import { filter, join, project } from './operators.js';

export type Plan =
  | { kind: 'scan'; table: Table }
  | { kind: 'filter'; pred: Expr; input: Plan }
  | { kind: 'project'; cols: string[]; input: Plan }
  | { kind: 'join'; joinType: JoinType; leftKeys: string[]; rightKeys: string[]; left: Plan; right: Plan };

export const scan = (table: Table): Plan => ({ kind: 'scan', table });
export const filterNode = (pred: Expr, input: Plan): Plan => ({ kind: 'filter', pred, input });
export const projectNode = (cols: string[], input: Plan): Plan => ({ kind: 'project', cols, input });
export const joinNode = (
  joinType: JoinType,
  leftKeys: string[],
  rightKeys: string[],
  left: Plan,
  right: Plan,
): Plan => ({ kind: 'join', joinType, leftKeys, rightKeys, left, right });

export function execute(plan: Plan): Row[] {
  switch (plan.kind) {
    case 'scan':
      return plan.table.rows;
    case 'filter':
      return filter(execute(plan.input), plan.pred);
    case 'project':
      return project(execute(plan.input), plan.cols);
    case 'join': {
      const leftRows = execute(plan.left);
      const rightRows = execute(plan.right);
      const leftTable: Table = {
        name: 'left',
        rows: leftRows,
        schemaCols: outputCols(plan.left),
      };
      const rightTable: Table = {
        name: 'right',
        rows: rightRows,
        schemaCols: outputCols(plan.right),
      };
      return join(leftTable, rightTable, plan.leftKeys, plan.rightKeys, plan.joinType);
    }
  }
}

// 计划的输出列（按算子语义推导；连接为左列 + 右列）。
export function outputCols(plan: Plan): string[] {
  switch (plan.kind) {
    case 'scan':
      return plan.table.schemaCols ??
        (plan.table.rows.length > 0 ? Object.keys(plan.table.rows[0]) : []);
    case 'filter':
      return outputCols(plan.input);
    case 'project':
      return plan.cols;
    case 'join':
      return [...outputCols(plan.left), ...outputCols(plan.right)];
  }
}

export function formatPlan(plan: Plan, indent: string = ''): string {
  const pad = indent;
  switch (plan.kind) {
    case 'scan':
      return `${pad}Scan(${plan.table.name})`;
    case 'filter':
      return `${pad}Filter[${formatExpr(plan.pred)}]\n${formatPlan(plan.input, pad + '  ')}`;
    case 'project':
      return `${pad}Project[${plan.cols.join(',')}]\n${formatPlan(plan.input, pad + '  ')}`;
    case 'join':
      return (
        `${pad}Join[${plan.joinType}] ${plan.leftKeys.join(',')}=${plan.rightKeys.join(',')}\n` +
        `${formatPlan(plan.left, pad + '  ')}\n${formatPlan(plan.right, pad + '  ')}`
      );
  }
}

export function andOf(exprs: Expr[]): Expr {
  return exprs.length === 1 ? exprs[0] : and(...exprs);
}

export { conjuncts };
