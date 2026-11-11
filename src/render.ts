import type { CanonicalQuery, CExpr, CRelation, CSelect, CSelectItem } from './canonical.ts';
import { QueryStructureError } from './errors.ts';

export interface RenderedQuery {
  sql: string;
  placeholders: string[];
}

export function renderQuery(query: CanonicalQuery): RenderedQuery {
  const placeholders = query.slots.map((_, index) => `$${index + 1}`);
  const sql = renderSelect(query.tree, placeholders);
  return { sql, placeholders };
}

function quoteIdent(value: string): string {
  return `"${value.replaceAll('"', '""')}"`;
}

function renderSelect(node: CSelect, placeholders: string[]): string {
  const parts: string[] = ['SELECT'];
  if (node.distinct) {
    parts.push('DISTINCT');
  }
  parts.push(node.selectList.map((item) => renderItem(item, placeholders)).join(', '));
  parts.push('FROM');
  parts.push(renderRelation(node.from, placeholders));
  for (const joined of node.joins) {
    const keyword =
      joined.type === 'inner'
        ? 'INNER JOIN'
        : joined.type === 'left'
          ? 'LEFT JOIN'
          : joined.type === 'right'
            ? 'RIGHT JOIN'
            : 'FULL JOIN';
    parts.push(
      `${keyword} ${renderRelation(joined.relation, placeholders)} ON ${renderExpr(joined.on, placeholders)}`,
    );
  }
  if (node.where) {
    parts.push(`WHERE ${renderExpr(node.where, placeholders)}`);
  }
  if (node.groupBy.length > 0) {
    parts.push(
      `GROUP BY ${node.groupBy.map((expr) => renderExpr(expr, placeholders)).join(', ')}`,
    );
  }
  if (node.having) {
    parts.push(`HAVING ${renderExpr(node.having, placeholders)}`);
  }
  if (node.orderBy.length > 0) {
    const items = node.orderBy
      .map(
        (item) =>
          `${renderExpr(item.expr, placeholders)} ${item.direction === 'asc' ? 'ASC' : 'DESC'}`,
      )
      .join(', ');
    parts.push(`ORDER BY ${items}`);
  }
  if (node.limit !== null) {
    parts.push(`LIMIT ${node.limit}`);
  }
  return parts.join(' ');
}

function renderItem(item: CSelectItem, placeholders: string[]): string {
  if (item.star) {
    return item.qualifier === null ? '*' : `${quoteIdent(item.qualifier)}.*`;
  }
  const rendered = renderExpr(item.expr, placeholders);
  return item.alias === null ? rendered : `${rendered} AS ${quoteIdent(item.alias)}`;
}

function renderRelation(relation: CRelation, placeholders: string[]): string {
  if (relation.kind === 'table') {
    const head =
      relation.schema === null
        ? quoteIdent(relation.name)
        : `${quoteIdent(relation.schema)}.${quoteIdent(relation.name)}`;
    return relation.alias === null ? head : `${head} AS ${quoteIdent(relation.alias)}`;
  }
  return `(${renderSelect(relation.query, placeholders)}) AS ${quoteIdent(relation.alias)}`;
}

function renderExpr(expr: CExpr, placeholders: string[]): string {
  switch (expr.kind) {
    case 'param': {
      const placeholder = placeholders[expr.slot];
      if (placeholder === undefined) {
        throw new QueryStructureError(`renderer slot out of range: ${expr.slot}`);
      }
      return placeholder;
    }
    case 'lit':
      return String(expr.value);
    case 'bool':
      return expr.value ? 'TRUE' : 'FALSE';
    case 'col':
      return expr.qualifier === null
        ? quoteIdent(expr.name)
        : `${quoteIdent(expr.qualifier)}.${quoteIdent(expr.name)}`;
    case 'bin':
      return `(${renderExpr(expr.left, placeholders)} ${expr.op} ${renderExpr(expr.right, placeholders)})`;
    case 'un':
      return `(${expr.op} ${renderExpr(expr.expr, placeholders)})`;
    case 'isNull':
      return `(${renderExpr(expr.expr, placeholders)} IS NULL)`;
    case 'inList':
      return `(${renderExpr(expr.expr, placeholders)} ${expr.negated ? 'NOT ' : ''}IN (${expr.slots
        .map((slot) => {
          const placeholder = placeholders[slot];
          if (placeholder === undefined) {
            throw new QueryStructureError(`renderer slot out of range: ${slot}`);
          }
          return placeholder;
        })
        .join(', ')}))`;
    case 'inQuery':
      return `(${renderExpr(expr.expr, placeholders)} ${expr.negated ? 'NOT ' : ''}IN (${renderSelect(
        expr.query,
        placeholders,
      )}))`;
    case 'exists':
      return `(EXISTS (${renderSelect(expr.query, placeholders)}))`;
    case 'scalarQuery':
      return `(${renderSelect(expr.query, placeholders)})`;
    case 'func': {
      const args = expr.star
        ? '*'
        : expr.args.map((arg) => renderExpr(arg, placeholders)).join(', ');
      return `${expr.name}(${expr.distinct && !expr.star ? 'DISTINCT ' : ''}${args})`;
    }
    case 'between':
      return `(${renderExpr(expr.expr, placeholders)} ${expr.negated ? 'NOT ' : ''}BETWEEN ${renderExpr(
        expr.low,
        placeholders,
      )} AND ${renderExpr(expr.high, placeholders)})`;
  }
}
