import type { DmlStatement, RowImage } from './types.ts';

function quoteIdent(ident: string): string {
  return `"${ident.replaceAll('"', '""')}"`;
}

function literal(value: unknown): string {
  if (value === null || value === undefined) return 'NULL';
  if (typeof value === 'number') return Number.isFinite(value) ? String(value) : 'NULL';
  if (typeof value === 'boolean') return value ? 'TRUE' : 'FALSE';
  if (value instanceof Date) return `'${value.toISOString()}'`;
  return `'${String(value).replaceAll("'", "''")}'`;
}

function whereClause(where: RowImage): string {
  return Object.entries(where)
    .map(([column, value]) =>
      value === null ? `${quoteIdent(column)} IS NULL` : `${quoteIdent(column)} = ${literal(value)}`,
    )
    .join(' AND ');
}

/** 将一条补偿 DML 渲染为可执行 SQL */
export function renderSql(stmt: DmlStatement): string {
  switch (stmt.kind) {
    case 'INSERT': {
      const values = stmt.values ?? {};
      const columns = Object.keys(values).map(quoteIdent).join(', ');
      const vals = Object.values(values).map(literal).join(', ');
      return `INSERT INTO ${quoteIdent(stmt.table)} (${columns}) VALUES (${vals});`;
    }
    case 'UPDATE': {
      const set = Object.entries(stmt.set ?? {})
        .map(([column, value]) => `${quoteIdent(column)} = ${literal(value)}`)
        .join(', ');
      return `UPDATE ${quoteIdent(stmt.table)} SET ${set} WHERE ${whereClause(stmt.where ?? {})};`;
    }
    case 'DELETE':
      return `DELETE FROM ${quoteIdent(stmt.table)} WHERE ${whereClause(stmt.where ?? {})};`;
  }
}

export function renderScript(statements: DmlStatement[]): string {
  const body = statements.map((stmt) => renderSql(stmt)).join('\n');
  return `BEGIN;\n${body}\nCOMMIT;\n`;
}
