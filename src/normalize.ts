import type { Expr, InListNode, ParamNode, SelectItem, SelectNode } from './ast.ts';
import type {
  CanonicalQuery,
  CExpr,
  CExprItem,
  CJoin,
  COrder,
  CRelation,
  CSelect,
  CSelectItem,
  ParamSlot,
} from './canonical.ts';

export function normalizeQuery(root: SelectNode): CanonicalQuery {
  const slots: ParamSlot[] = [];
  const slotByName = new Map<string, number>();

  const slotOf = (node: ParamNode): number => {
    const existing = slotByName.get(node.name);
    if (existing !== undefined) {
      return existing;
    }
    const index = slots.length;
    slotByName.set(node.name, index);
    slots.push({ name: node.name, paramType: node.paramType });
    return index;
  };

  const normalizeExpr = (expr: Expr): CExpr => {
    switch (expr.kind) {
      case 'param':
        return { kind: 'param', slot: slotOf(expr) };
      case 'lit':
        return { kind: 'lit', value: expr.value };
      case 'bool':
        return { kind: 'bool', value: expr.value };
      case 'col':
        return {
          kind: 'col',
          qualifier: expr.qualifier ?? null,
          name: expr.name,
        };
      case 'bin':
        return {
          kind: 'bin',
          op: expr.op,
          left: normalizeExpr(expr.left),
          right: normalizeExpr(expr.right),
        };
      case 'un':
        return { kind: 'un', op: expr.op, expr: normalizeExpr(expr.expr) };
      case 'isNull':
        return { kind: 'isNull', expr: normalizeExpr(expr.expr) };
      case 'inList':
        return normalizeInList(expr);
      case 'inQuery':
        return {
          kind: 'inQuery',
          expr: normalizeExpr(expr.expr),
          query: normalizeSelect(expr.query),
          negated: expr.negated,
        };
      case 'exists':
        return { kind: 'exists', query: normalizeSelect(expr.query) };
      case 'scalarQuery':
        return { kind: 'scalarQuery', query: normalizeSelect(expr.query) };
      case 'func':
        return {
          kind: 'func',
          name: expr.name,
          args: expr.args.map(normalizeExpr),
          star: expr.star,
          distinct: expr.distinct,
        };
      case 'between':
        return {
          kind: 'between',
          expr: normalizeExpr(expr.expr),
          low: normalizeExpr(expr.low),
          high: normalizeExpr(expr.high),
          negated: expr.negated,
        };
    }
  };

  const normalizeInList = (node: InListNode): CExpr => {
    const target = normalizeExpr(node.expr);
    if (node.items.length === 0) {
      const emptyMatch: CExpr = { kind: 'bool', value: false };
      return node.negated ? { kind: 'un', op: 'NOT', expr: emptyMatch } : emptyMatch;
    }
    return {
      kind: 'inList',
      expr: target,
      slots: node.items.map(slotOf),
      negated: node.negated,
    };
  };

  const normalizeRelation = (relation: SelectNode['from']): CRelation => {
    if (relation.kind === 'table') {
      return {
        kind: 'table',
        schema: relation.schema ?? null,
        name: relation.name,
        alias: relation.alias ?? null,
      };
    }
    return {
      kind: 'subquery',
      query: normalizeSelect(relation.query),
      alias: relation.alias,
    };
  };

  const normalizeItem = (item: SelectItem): CSelectItem => {
    if (item.star) {
      return { star: true, qualifier: item.qualifier ?? null };
    }
    const normalized: CExprItem = {
      star: false,
      expr: normalizeExpr(item.expr),
      alias: item.as ?? null,
    };
    return normalized;
  };

  const normalizeSelect = (node: SelectNode): CSelect => {
    const joins: CJoin[] = node.joins.map((joined) => ({
      type: joined.type,
      relation: normalizeRelation(joined.relation),
      on: normalizeExpr(joined.on),
    }));
    const orderBy: COrder[] = node.orderBy.map((item) => ({
      expr: normalizeExpr(item.expr),
      direction: item.direction,
    }));
    return {
      kind: 'select',
      distinct: node.distinct,
      selectList: node.selectList.map(normalizeItem),
      from: normalizeRelation(node.from),
      joins,
      where: node.where ? normalizeExpr(node.where) : null,
      groupBy: node.groupBy.map(normalizeExpr),
      having: node.having ? normalizeExpr(node.having) : null,
      orderBy,
      limit: node.limit,
    };
  };

  const tree = normalizeSelect(root);
  return { tree, slots };
}
