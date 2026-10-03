// 将 AST 序列化回 SQL 文本

import type { Expr, SelectItem, SelectStmt, TableSource } from './ast.ts';

export function formatExpr(expr: Expr): string {
  switch (expr.kind) {
    case 'column':
      return expr.table ? `${expr.table}.${expr.name}` : expr.name;
    case 'literal':
      if (expr.value === null) return 'NULL';
      if (expr.value === true) return 'TRUE';
      if (expr.value === false) return 'FALSE';
      if (typeof expr.value === 'string') return `'${expr.value}'`;
      return String(expr.value);
    case 'binary':
      return `${formatOperand(expr.left, expr.op)} ${expr.op} ${formatOperand(expr.right, expr.op)}`;
    case 'exists':
      return `${expr.negated ? 'NOT ' : ''}EXISTS (${formatSelect(expr.subquery)})`;
    case 'in':
      return `${formatExpr(expr.expr)} ${expr.negated ? 'NOT ' : ''}IN (${formatSelect(expr.subquery)})`;
  }
}

// 低优先级子表达式加括号，保证 AND/OR 还原正确
function formatOperand(expr: Expr, parentOp: string): string {
  if (expr.kind === 'binary') {
    const needParen =
      (parentOp === 'AND' && expr.op === 'OR') ||
      ((parentOp === '=' || parentOp === '<>') && (expr.op === 'AND' || expr.op === 'OR'));
    const s = formatExpr(expr);
    return needParen ? `(${s})` : s;
  }
  return formatExpr(expr);
}

function formatSelectItem(item: SelectItem): string {
  if (item.kind === 'star') return item.table ? `${item.table}.*` : '*';
  const s = formatExpr(item.expr);
  return item.alias ? `${s} AS ${item.alias}` : s;
}

export function formatSource(source: TableSource): string {
  if (source.kind === 'table') {
    return source.alias ? `${source.name} ${source.alias}` : source.name;
  }
  const left = formatSource(source.left);
  const right = formatSource(source.right);
  if (source.joinType === 'CROSS') return `${left} CROSS JOIN ${right}`;
  return `${left} ${source.joinType} JOIN ${right} ON ${formatExpr(source.on!)}`;
}

export function formatSelect(stmt: SelectStmt): string {
  const cols = stmt.columns.map(formatSelectItem).join(', ');
  const from = stmt.from.map(formatSource).join(', ');
  let sql = `SELECT ${cols} FROM ${from}`;
  if (stmt.where) sql += ` WHERE ${formatExpr(stmt.where)}`;
  return sql;
}
