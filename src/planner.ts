// 将重写后的逻辑计划（AST）转换为物理执行计划
// SEMI/ANTI JOIN -> HashSemiJoin / HashAntiJoin（右表 build，左表 probe）

import {
  splitAnd,
  type Expr,
  type SelectStmt,
  type TableSource,
} from './ast.ts';
import { formatExpr } from './format.ts';
import { collectAliases } from './unnest.ts';

export type PlanNode =
  | { type: 'Project'; columns: string[]; input: PlanNode }
  | { type: 'Filter'; condition: string; input: PlanNode }
  | {
      type: 'HashSemiJoin' | 'HashAntiJoin' | 'HashInnerJoin' | 'NestedCrossJoin';
      probeKeys: string[];
      buildKeys: string[];
      residual?: string;
      probe: PlanNode;
      build: PlanNode;
    }
  | { type: 'SeqScan'; table: string; alias: string };

function sourceAliases(s: TableSource): Set<string> {
  return new Set(collectAliases([s]));
}

/** 从 ON 条件中拆分出等值连接键（两侧各引一侧别名）与残余条件 */
function splitJoinCondition(
  on: Expr | undefined,
  leftAliases: Set<string>,
  rightAliases: Set<string>,
): { probeKeys: Expr[]; buildKeys: Expr[]; residual: Expr[] } {
  const probeKeys: Expr[] = [];
  const buildKeys: Expr[] = [];
  const residual: Expr[] = [];

  const refsOf = (e: Expr): Set<string> => {
    const s = new Set<string>();
    const walk = (x: Expr) => {
      if (x.kind === 'column' && x.table) s.add(x.table);
      else if (x.kind === 'binary') {
        walk(x.left);
        walk(x.right);
      } else if (x.kind === 'in') walk(x.expr);
    };
    walk(e);
    return s;
  };

  for (const cond of on ? splitAnd(on) : []) {
    if (cond.kind === 'binary' && cond.op === '=') {
      const lr = refsOf(cond.left);
      const rr = refsOf(cond.right);
      const leftFromLeft = [...lr].some((a) => leftAliases.has(a));
      const rightFromRight = [...rr].some((a) => rightAliases.has(a));
      const leftFromRight = [...lr].some((a) => rightAliases.has(a));
      const rightFromLeft = [...rr].some((a) => leftAliases.has(a));
      if (leftFromLeft && rightFromRight && !leftFromRight && !rightFromLeft) {
        probeKeys.push(cond.left);
        buildKeys.push(cond.right);
        continue;
      }
      if (leftFromRight && rightFromLeft && !leftFromLeft && !rightFromRight) {
        probeKeys.push(cond.right);
        buildKeys.push(cond.left);
        continue;
      }
    }
    residual.push(cond);
  }
  return { probeKeys, buildKeys, residual };
}

function planSource(source: TableSource): PlanNode {
  if (source.kind === 'table') {
    return { type: 'SeqScan', table: source.name, alias: source.alias ?? source.name };
  }
  const probe = planSource(source.left);
  const build = planSource(source.right);
  if (source.joinType === 'CROSS') {
    return {
      type: 'NestedCrossJoin',
      probeKeys: [],
      buildKeys: [],
      probe,
      build,
    };
  }
  const { probeKeys, buildKeys, residual } = splitJoinCondition(
    source.on,
    sourceAliases(source.left),
    sourceAliases(source.right),
  );
  const type =
    source.joinType === 'SEMI'
      ? 'HashSemiJoin'
      : source.joinType === 'ANTI'
        ? 'HashAntiJoin'
        : 'HashInnerJoin';
  return {
    type,
    probeKeys: probeKeys.map(formatExpr),
    buildKeys: buildKeys.map(formatExpr),
    residual: residual.length ? residual.map(formatExpr).join(' AND ') : undefined,
    probe,
    build,
  } as PlanNode;
}

export function toPhysicalPlan(stmt: SelectStmt): PlanNode {
  const from = stmt.from.reduce<TableSource | null>((acc, s) => {
    if (!acc) return s;
    return { kind: 'join', joinType: 'CROSS', left: acc, right: s };
  }, null);
  if (!from) throw new Error('SELECT 缺少 FROM');

  let node: PlanNode = planSource(from);
  if (stmt.where) {
    node = { type: 'Filter', condition: formatExpr(stmt.where), input: node };
  }
  const columns = stmt.columns.map((c) =>
    c.kind === 'star' ? (c.table ? `${c.table}.*` : '*') : formatExpr(c.expr),
  );
  return { type: 'Project', columns, input: node };
}

/** 以树形文本打印执行计划 */
export function formatPlan(node: PlanNode, indent = ''): string {
  switch (node.type) {
    case 'Project':
      return `${indent}Project [${node.columns.join(', ')}]\n${formatPlan(node.input, indent + '  ')}`;
    case 'Filter':
      return `${indent}Filter (${node.condition})\n${formatPlan(node.input, indent + '  ')}`;
    case 'SeqScan':
      return `${indent}SeqScan ${node.table} AS ${node.alias}`;
    default: {
      const keys =
        node.probeKeys.length > 0
          ? ` keys: (${node.probeKeys.join(', ')}) = (${node.buildKeys.join(', ')})`
          : '';
      const residual = node.residual ? ` residual: (${node.residual})` : '';
      return (
        `${indent}${node.type}${keys}${residual}\n` +
        `${indent}  probe:\n${formatPlan(node.probe, indent + '    ')}\n` +
        `${indent}  build:\n${formatPlan(node.build, indent + '    ')}`
      );
    }
  }
}
