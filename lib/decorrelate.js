'use strict';
/** Compiled equivalent of src/decorrelate.ts. */

function splitConjuncts(expr) {
  if (!expr) return [];
  if (expr.kind === 'and') {
    const out = [];
    for (const item of expr.items) out.push(...splitConjuncts(item));
    return out;
  }
  return [expr];
}

function collectColumns(expr, out = []) {
  switch (expr.kind) {
    case 'column':
      out.push(expr);
      break;
    case 'binary':
      collectColumns(expr.left, out);
      collectColumns(expr.right, out);
      break;
    case 'and':
    case 'or':
      for (const item of expr.items) collectColumns(item, out);
      break;
    case 'in':
      collectColumns(expr.expr, out);
      break;
    default:
      break;
  }
  return out;
}

function containsSubquery(expr) {
  if (!expr) return false;
  switch (expr.kind) {
    case 'exists':
    case 'in':
      return true;
    case 'binary':
      return containsSubquery(expr.left) || containsSubquery(expr.right);
    case 'and':
    case 'or':
      return expr.items.some(containsSubquery);
    default:
      return false;
  }
}

function classify(expr, innerAlias, outerAlias) {
  const cols = collectColumns(expr);
  if (cols.length === 0) return 'none';
  let scope = 'none';
  for (const col of cols) {
    let s;
    if (col.table === null || col.table === innerAlias) s = 'inner';
    else if (col.table === outerAlias) s = 'outer';
    else s = 'mixed';
    if (scope === 'none') scope = s;
    else if (scope !== s) return 'mixed';
  }
  return scope;
}

function isEquality(e) {
  return e.kind === 'binary' && e.op === '=';
}

function fail(reason) {
  return { ok: false, reason };
}

function extractKeysAndFilters(sub, outerAlias, extraKeys) {
  if (sub.from.length !== 1) {
    return fail('subquery FROM must reference exactly one table');
  }
  if (containsSubquery(sub.where)) {
    return fail('nested subqueries are not supported');
  }
  const innerAlias = sub.from[0].alias;
  const keys = [...extraKeys];
  const filters = [];
  for (const conjunct of splitConjuncts(sub.where)) {
    if (isEquality(conjunct)) {
      const l = classify(conjunct.left, innerAlias, outerAlias);
      const r = classify(conjunct.right, innerAlias, outerAlias);
      if (l === 'inner' && r === 'outer') {
        keys.push({ inner: exprToSql(conjunct.left), outer: exprToSql(conjunct.right) });
        continue;
      }
      if (l === 'outer' && r === 'inner') {
        keys.push({ inner: exprToSql(conjunct.right), outer: exprToSql(conjunct.left) });
        continue;
      }
    }
    const scope = classify(conjunct, innerAlias, outerAlias);
    if (scope === 'inner' || scope === 'none') {
      filters.push(conjunct);
      continue;
    }
    return fail(`cannot decorrelate predicate: ${exprToSql(conjunct)}`);
  }
  return { ok: true, keys, filters };
}

function decorrelate(stmt) {
  const notes = [];
  const joins = [];
  const residual = [];

  if (stmt.from.length !== 1) {
    return {
      changed: false,
      query: stmt,
      outer: null,
      joins,
      residual: splitConjuncts(stmt.where),
      notes: ['outer query must reference exactly one table; nothing rewritten'],
    };
  }
  const outer = stmt.from[0];

  for (const conjunct of splitConjuncts(stmt.where)) {
    if (conjunct.kind !== 'exists' && conjunct.kind !== 'in') {
      residual.push(conjunct);
      continue;
    }
    const sub = conjunct.subquery;
    let extraKeys = [];
    if (conjunct.kind === 'in') {
      const items = sub.select;
      if (items.length !== 1 || items[0].kind !== 'expr') {
        residual.push(conjunct);
        notes.push('IN subquery must select exactly one expression; kept as-is');
        continue;
      }
      extraKeys = [
        { outer: exprToSql(conjunct.expr), inner: exprToSql(items[0].expr) },
      ];
    }
    const result = extractKeysAndFilters(sub, outer.alias, extraKeys);
    if (!result.ok) {
      residual.push(conjunct);
      notes.push(`${result.reason}; kept as-is`);
      continue;
    }
    const source =
      conjunct.kind === 'exists'
        ? conjunct.negated
          ? 'NOT EXISTS'
          : 'EXISTS'
        : conjunct.negated
          ? 'NOT IN'
          : 'IN';
    joins.push({
      type: conjunct.negated ? 'ANTI' : 'SEMI',
      source,
      table: sub.from[0].table,
      alias: sub.from[0].alias,
      keys: result.keys,
      filters: result.filters,
    });
    notes.push(
      `${source} subquery on ${sub.from[0].table} ${sub.from[0].alias} ` +
        `rewritten as ${conjunct.negated ? 'ANTI' : 'SEMI'} JOIN ` +
        `(keys: ${result.keys.map((k) => `${k.outer} = ${k.inner}`).join(', ') || 'none'})`,
    );
  }

  return { changed: joins.length > 0, query: stmt, outer, joins, residual, notes };
}

function exprToSql(expr) {
  switch (expr.kind) {
    case 'column':
      return expr.table ? `${expr.table}.${expr.name}` : expr.name;
    case 'literal':
      if (expr.value === null) return 'NULL';
      return typeof expr.value === 'string' ? `'${expr.value}'` : String(expr.value);
    case 'binary':
      return `${exprToSql(expr.left)} ${expr.op} ${exprToSql(expr.right)}`;
    case 'and':
      return expr.items.map(exprToSql).join(' AND ');
    case 'or':
      return expr.items.map((i) => `(${exprToSql(i)})`).join(' OR ');
    case 'exists':
      return `${expr.negated ? 'NOT ' : ''}EXISTS (${selectToSql(expr.subquery)})`;
    case 'in':
      return `${exprToSql(expr.expr)} ${expr.negated ? 'NOT ' : ''}IN (${selectToSql(expr.subquery)})`;
  }
}

function selectToSql(stmt) {
  const select = stmt.select
    .map((item) => {
      if (item.kind === 'star') return item.table ? `${item.table}.*` : '*';
      const s = exprToSql(item.expr);
      return item.alias ? `${s} AS ${item.alias}` : s;
    })
    .join(', ');
  const from = stmt.from.map((t) => `${t.table} ${t.alias}`).join(', ');
  const where = stmt.where ? ` WHERE ${exprToSql(stmt.where)}` : '';
  return `SELECT ${select} FROM ${from}${where}`;
}

function renderRewrittenSql(result) {
  if (!result.outer) return selectToSql(result.query);
  const outer = result.outer;
  const select = result.query.select
    .map((item) => {
      if (item.kind === 'star') return item.table ? `${item.table}.*` : `${outer.alias}.*`;
      const s = exprToSql(item.expr);
      return item.alias ? `${s} AS ${item.alias}` : s;
    })
    .join(', ');
  let sql = `SELECT ${select} FROM ${outer.table} ${outer.alias}`;
  for (const join of result.joins) {
    const onParts = [
      ...join.keys.map((k) => `${k.outer} = ${k.inner}`),
      ...join.filters.map(exprToSql),
    ];
    sql += ` ${join.type} JOIN ${join.table} ${join.alias}`;
    if (onParts.length > 0) sql += ` ON ${onParts.join(' AND ')}`;
  }
  if (result.residual.length > 0) {
    sql += ` WHERE ${result.residual.map(exprToSql).join(' AND ')}`;
  }
  return sql;
}

module.exports = {
  splitConjuncts,
  collectColumns,
  decorrelate,
  exprToSql,
  selectToSql,
  renderRewrittenSql,
};
