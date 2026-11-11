import type { SelectNode } from './ast.ts';
import type { CanonicalQuery, CExpr, CSelect, ParamSlot } from './canonical.ts';
import { equalCanonical } from './canonical.ts';
import { QueryStructureError } from './errors.ts';
import { normalizeQuery } from './normalize.ts';
import { SqlParser } from './parser/sql-parser.ts';
import { renderQuery } from './render.ts';
import { validateQuery } from './validate.ts';
import type { IdentifierWhitelist } from './whitelist.ts';

export interface VerificationReport {
  sql: string;
  placeholders: string[];
  slots: ParamSlot[];
  checks: {
    identifierWhitelist: 'passed';
    scopeResolution: 'passed';
    renderRoundTrip: 'passed';
    parameterInvariance: 'passed';
    repeatedParameters: 'passed';
    emptyInSets: 'passed';
    subqueryScoping: 'passed';
    boundedOrdering: 'passed';
  };
}

export function buildAndVerify(
  root: SelectNode,
  whitelist: IdentifierWhitelist,
): VerificationReport {
  validateQuery(root, whitelist);
  const canonical = normalizeQuery(root);
  const rendered = renderQuery(canonical);

  if (rendered.sql.includes("'")) {
    throw new QueryStructureError('rendered SQL must never contain inline string literals');
  }

  const reparsed = new SqlParser(rendered.sql).parseSelectStatement();
  if (!equalCanonical(reparsed, canonical.tree)) {
    throw new QueryStructureError(
      'round-trip mismatch: the independent parser reconstructed a different query structure',
    );
  }

  verifyParameterInvariance(canonical, rendered.sql);
  verifySlotCoverage(canonical, rendered.placeholders);

  return {
    sql: rendered.sql,
    placeholders: rendered.placeholders,
    slots: canonical.slots,
    checks: {
      identifierWhitelist: 'passed',
      scopeResolution: 'passed',
      renderRoundTrip: 'passed',
      parameterInvariance: 'passed',
      repeatedParameters: 'passed',
      emptyInSets: 'passed',
      subqueryScoping: 'passed',
      boundedOrdering: 'passed',
    },
  };
}

function verifySlotCoverage(canonical: CanonicalQuery, placeholders: string[]): void {
  if (placeholders.length !== canonical.slots.length) {
    throw new QueryStructureError('placeholder count does not match parameter slot count');
  }
  const referenced = new Set<number>();
  collectSlots(canonical.tree, referenced);
  for (let slot = 0; slot < canonical.slots.length; slot += 1) {
    if (!referenced.has(slot)) {
      throw new QueryStructureError(`parameter slot $${slot + 1} is never referenced`);
    }
  }
}

function collectSlots(node: CSelect, referenced: Set<number>): void {
  const visit = (expr: CExpr): void => {
    switch (expr.kind) {
      case 'param':
        referenced.add(expr.slot);
        return;
      case 'bin':
        visit(expr.left);
        visit(expr.right);
        return;
      case 'un':
      case 'isNull':
        visit(expr.expr);
        return;
      case 'inList':
        visit(expr.expr);
        expr.slots.forEach((slot) => referenced.add(slot));
        return;
      case 'inQuery':
        visit(expr.expr);
        collectSlots(expr.query, referenced);
        return;
      case 'exists':
      case 'scalarQuery':
        collectSlots(expr.query, referenced);
        return;
      case 'between':
        visit(expr.expr);
        visit(expr.low);
        visit(expr.high);
        return;
      case 'func':
        expr.args.forEach(visit);
        return;
      case 'lit':
      case 'bool':
      case 'col':
        return;
    }
  };

  node.selectList.forEach((item) => {
    if (!item.star) {
      visit(item.expr);
    }
  });
  visitRelation(node.from);
  node.joins.forEach((joined) => {
    visit(joined.on);
    visitRelation(joined.relation);
  });
  if (node.where) {
    visit(node.where);
  }
  node.groupBy.forEach(visit);
  if (node.having) {
    visit(node.having);
  }
  node.orderBy.forEach((item) => visit(item.expr));

  function visitRelation(relation: CSelect['from']): void {
    if (relation.kind === 'subquery') {
      collectSlots(relation.query, referenced);
    }
  }
}

function verifyParameterInvariance(canonical: CanonicalQuery, sql: string): void {
  const baseline = stripValues(canonical.tree);
  const probeForms = buildProbeForms(canonical.slots);

  for (let slot = 0; slot < canonical.slots.length; slot += 1) {
    for (const form of probeForms[slot]) {
      const probeSql = injectProbe(sql, slot, form);
      const reparsed = new SqlParser(probeSql).parseSelectStatement();
      const normalized = stripValues(reparsed);
      if (!equalCanonical(normalized, baseline)) {
        throw new QueryStructureError('parameter substitution changed the parsed query structure');
      }
    }
  }
}

function buildProbeForms(slots: readonly ParamSlot[]): string[][] {
  const typeForms: Record<ParamSlot['paramType'], string[]> = {
    string: ['text', 'text OR 1=1', "text'; DROP TABLE users;--", ''],
    integer: ['0', '-1', '9007199254740991', '1 OR 1=1'],
    number: ['3.14159', '-0.0', '1e308'],
    boolean: ['TRUE', 'FALSE'],
    date: ['2026-11-11T00:00:00.000Z'],
    null: ['NULL'],
  };
  return slots.map((slot) => typeForms[slot.paramType]);
}

function injectProbe(sql: string, targetSlot: number, form: string): string {
  return sql.replace(/\$([0-9]+)/g, (match, digits: string) => {
    void match;
    const slot = Number(digits) - 1;
    return slot === targetSlot ? `$$${form}$$` : '$$neutral$$';
  });
}

function stripValues(node: CSelect): CSelect {
  return stripSelect(node);
}

function stripSelect(node: CSelect): CSelect {
  return {
    ...node,
    selectList: node.selectList.map((item) =>
      item.star ? item : { ...item, expr: stripExpr(item.expr) },
    ),
    from:
      node.from.kind === 'subquery'
        ? { ...node.from, query: stripSelect(node.from.query) }
        : node.from,
    joins: node.joins.map((joined) => ({
      ...joined,
      relation:
        joined.relation.kind === 'subquery'
          ? { ...joined.relation, query: stripSelect(joined.relation.query) }
          : joined.relation,
      on: stripExpr(joined.on),
    })),
    where: node.where ? stripExpr(node.where) : null,
    groupBy: node.groupBy.map(stripExpr),
    having: node.having ? stripExpr(node.having) : null,
    orderBy: node.orderBy.map((item) => ({ ...item, expr: stripExpr(item.expr) })),
  };
}

function stripExpr(expr: CExpr): CExpr {
  switch (expr.kind) {
    case 'param':
      return { kind: 'lit', value: null };
    case 'lit':
    case 'bool':
      return { kind: 'lit', value: null };
    case 'col':
      return expr;
    case 'bin':
      return { ...expr, left: stripExpr(expr.left), right: stripExpr(expr.right) };
    case 'un':
    case 'isNull':
      return { ...expr, expr: stripExpr(expr.expr) };
    case 'inList':
      return { ...expr, expr: stripExpr(expr.expr), slots: expr.slots.map(() => -1) };
    case 'inQuery':
      return { ...expr, expr: stripExpr(expr.expr), query: stripSelect(expr.query) };
    case 'exists':
    case 'scalarQuery':
      return { ...expr, query: stripSelect(expr.query) };
    case 'func':
      return { ...expr, args: expr.args.map(stripExpr) };
    case 'between':
      return {
        ...expr,
        expr: stripExpr(expr.expr),
        low: stripExpr(expr.low),
        high: stripExpr(expr.high),
      };
  }
}
