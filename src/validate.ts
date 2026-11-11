import {
  ALLOWED_FUNCTIONS,
  MAX_ORDER_ITEMS,
  walkExpr,
} from './ast.ts';
import type { Expr, ParamNode, Relation, SelectNode, SelectItem } from './ast.ts';
import { QueryStructureError } from './errors.ts';
import { IDENTIFIER_RE, assertIdentifier } from './ident.ts';
import { compileWhitelist } from './whitelist.ts';
import type { IdentifierWhitelist, ResolvedTable } from './whitelist.ts';

interface RelationScope {
  alias: string;
  columns: ReadonlySet<string>;
}

interface Scope {
  relations: Map<string, RelationScope>;
}

type SubqueryPosition = 'root' | 'exists' | 'derived' | 'scalar' | 'inQuery';

export function validateQuery(root: SelectNode, whitelist: IdentifierWhitelist): void {
  const tables = compileWhitelist(whitelist);
  const paramTypes = new Map<string, string>();
  validateSelect(root, tables, [], 'root', paramTypes);
}

function validateSelect(
  node: SelectNode,
  tables: Map<string, ResolvedTable>,
  outer: Scope[],
  position: SubqueryPosition,
  paramTypes: Map<string, string>,
): Scope {
  const scope = buildScope(node, tables, outer, paramTypes);
  const chain = [...outer, scope];

  validateSelectList(node.selectList, position, tables, chain, paramTypes);

  if (node.where) {
    validateExpr(node.where, tables, chain, paramTypes);
  }
  if (node.having) {
    validateExpr(node.having, tables, chain, paramTypes);
  }
  for (const groupExpr of node.groupBy) {
    if (groupExpr.kind !== 'col') {
      throw new QueryStructureError('GROUP BY supports plain column references only');
    }
    validateExpr(groupExpr, tables, chain, paramTypes);
  }
  if (node.orderBy.length > MAX_ORDER_ITEMS) {
    throw new QueryStructureError(
      `ORDER BY is bounded to ${MAX_ORDER_ITEMS} items, received ${node.orderBy.length}`,
    );
  }
  for (const item of node.orderBy) {
    if (item.direction !== 'asc' && item.direction !== 'desc') {
      throw new QueryStructureError('ORDER BY direction must be asc or desc');
    }
    validateExpr(item.expr, tables, chain, paramTypes);
  }
  return scope;
}

function buildScope(
  node: SelectNode,
  tables: Map<string, ResolvedTable>,
  outer: Scope[],
  paramTypes: Map<string, string>,
): Scope {
  const relations = new Map<string, RelationScope>();
  addRelation(node.from, tables, outer, relations, paramTypes);
  for (const joined of node.joins) {
    addRelation(joined.relation, tables, outer, relations, paramTypes);
    validateExpr(joined.on, tables, [...outer, { relations }], paramTypes);
  }
  return { relations };
}

function addRelation(
  relation: Relation,
  tables: Map<string, ResolvedTable>,
  outer: Scope[],
  relations: Map<string, RelationScope>,
  paramTypes: Map<string, string>,
): void {
  if (relation.kind === 'table') {
    const key =
      relation.schema === undefined ? relation.name : `${relation.schema}.${relation.name}`;
    const resolved = tables.get(key);
    if (!resolved) {
      throw new QueryStructureError(`table is not present in the identifier whitelist: ${key}`);
    }
    const alias = relation.alias ?? relation.name;
    assertIdentifier(alias, 'table alias');
    if (relations.has(alias)) {
      throw new QueryStructureError(`duplicate relation alias in query scope: ${alias}`);
    }
    relations.set(alias, { alias, columns: resolved.columns });
    return;
  }

  assertIdentifier(relation.alias, 'subquery alias');
  if (relations.has(relation.alias)) {
    throw new QueryStructureError(`duplicate relation alias in query scope: ${relation.alias}`);
  }
  validateSelect(relation.query, tables, outer, 'derived', paramTypes);
  const columns = new Set<string>();
  for (const item of relation.query.selectList) {
    if (item.star) {
      throw new QueryStructureError('derived table subqueries must not use SELECT *');
    }
    const name = outputName(item);
    if (name === undefined) {
      throw new QueryStructureError(
        'every derived table column needs an AS alias or a plain column name',
      );
    }
    columns.add(name);
  }
  relations.set(relation.alias, { alias: relation.alias, columns });
}

function outputName(item: SelectItem): string | undefined {
  if (item.star) {
    return undefined;
  }
  if (item.as !== undefined) {
    return IDENTIFIER_RE.test(item.as) ? item.as : undefined;
  }
  return item.expr.kind === 'col' ? item.expr.name : undefined;
}

function validateSelectList(
  selectList: SelectItem[],
  position: SubqueryPosition,
  tables: Map<string, ResolvedTable>,
  chain: Scope[],
  paramTypes: Map<string, string>,
): void {
  for (const item of selectList) {
    if (item.star) {
      if (position !== 'root' && position !== 'exists') {
        throw new QueryStructureError('SELECT * is only allowed at root or inside EXISTS');
      }
      if (item.qualifier !== undefined) {
        resolveQualified(item.qualifier, '*', chain);
      }
      continue;
    }
    if (item.as !== undefined) {
      assertIdentifier(item.as, 'column alias');
    }
    validateExpr(item.expr, tables, chain, paramTypes);
  }
}

function validateExpr(
  expr: Expr,
  tables: Map<string, ResolvedTable>,
  chain: Scope[],
  paramTypes: Map<string, string>,
): void {
  walkExpr(expr, (node) => {
    switch (node.kind) {
      case 'param':
        registerParam(node, paramTypes);
        return;
      case 'col':
        resolveColumn(node, chain);
        return;
      case 'inList':
        for (const item of node.items) {
          registerParam(item, paramTypes);
        }
        return;
      case 'inQuery':
        validateSelect(node.query, tables, [], 'inQuery', paramTypes);
        return;
      case 'exists':
        validateSelect(node.query, tables, chain, 'exists', paramTypes);
        return;
      case 'scalarQuery':
        validateSelect(node.query, tables, chain, 'scalar', paramTypes);
        return;
      case 'func':
        if (!ALLOWED_FUNCTIONS.has(node.name)) {
          throw new QueryStructureError(`function not allowed: ${node.name}`);
        }
        return;
      case 'lit':
      case 'bool':
      case 'bin':
      case 'un':
      case 'isNull':
      case 'between':
        return;
    }
  });
}

function registerParam(node: ParamNode, paramTypes: Map<string, string>): void {
  const previous = paramTypes.get(node.name);
  if (previous !== undefined && previous !== node.paramType) {
    throw new QueryStructureError(
      `parameter ${node.name} reused with conflicting types: ${previous} vs ${node.paramType}`,
    );
  }
  paramTypes.set(node.name, node.paramType);
}

function resolveColumn(node: { qualifier?: string; name: string }, chain: Scope[]): void {
  if (node.qualifier !== undefined) {
    resolveQualified(node.qualifier, node.name, chain);
    return;
  }
  for (let depth = chain.length - 1; depth >= 0; depth -= 1) {
    const matches: string[] = [];
    for (const relation of chain[depth].relations.values()) {
      if (relation.columns.has(node.name)) {
        matches.push(relation.alias);
      }
    }
    if (matches.length > 1) {
      throw new QueryStructureError(
        `ambiguous unqualified column ${node.name}, matches ${matches.join(', ')}`,
      );
    }
    if (matches.length === 1) {
      return;
    }
  }
  throw new QueryStructureError(`unknown column not covered by whitelist: ${node.name}`);
}

function resolveQualified(qualifier: string, name: string, chain: Scope[]): void {
  for (let depth = chain.length - 1; depth >= 0; depth -= 1) {
    const relation = chain[depth].relations.get(qualifier);
    if (relation) {
      if (name !== '*' && !relation.columns.has(name)) {
        throw new QueryStructureError(
          `column ${qualifier}.${name} is not covered by the identifier whitelist`,
        );
      }
      return;
    }
  }
  throw new QueryStructureError(`unknown table qualifier: ${qualifier}`);
}
