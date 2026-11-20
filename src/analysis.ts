import type { DatabaseSchema, Expr, Plan, SqlValue, TenantReadPolicy } from "./types.ts";
import { policyTenantLabels } from "./policy.ts";

export interface PlanAnalysis {
  readonly tables: readonly string[];
  readonly aliases: Readonly<Record<string, string>>;
  readonly aliasesByTable: Readonly<Record<string, readonly string[]>>;
  readonly columnsByTable: Readonly<Record<string, readonly string[]>>;
  readonly identifierConstants: readonly string[];
  readonly enumLiterals: readonly string[];
  readonly tenantColumns: Readonly<Record<string, string>>;
  readonly columnUsage: Readonly<Record<string, { readonly id: boolean; readonly enums: boolean }>>;
  readonly equalityEdges: Readonly<Record<string, readonly string[]>>;
}

function addTo(map: Map<string, Set<string>>, key: string, value: string): void {
  let set = map.get(key);
  if (set === undefined) {
    set = new Set();
    map.set(key, set);
  }
  set.add(value);
}

function collectScans(plan: Plan, aliases: Map<string, string>, tables: Set<string>): void {
  switch (plan.kind) {
    case "scan": {
      const alias = plan.alias ?? plan.table;
      const existing = aliases.get(alias);
      if (existing !== undefined && existing !== plan.table) {
        throw new Error(`Alias ${alias} is bound to two tables`);
      }
      aliases.set(alias, plan.table);
      aliases.set(plan.table, plan.table);
      tables.add(plan.table);
      return;
    }
    case "filter":
      collectScans(plan.input, aliases, tables);
      return;
    case "project":
      collectScans(plan.input, aliases, tables);
      return;
    case "join":
      collectScans(plan.left, aliases, tables);
      collectScans(plan.right, aliases, tables);
      return;
    case "unionAll":
      for (const input of plan.inputs) collectScans(input, aliases, tables);
      return;
  }
}

function collectExpr(
  expr: Expr,
  aliases: ReadonlyMap<string, string>,
  tables: readonly string[],
  columns: Map<string, Set<string>>,
  ids: Set<string>,
  enums: Set<string>,
): void {
  switch (expr.kind) {
    case "column": {
      if (expr.table !== undefined && expr.table !== "") {
        const table = aliases.get(expr.table);
        if (table !== undefined) {
          addTo(columns, table, expr.column);
        } else if (tables.includes(expr.table)) {
          addTo(columns, expr.table, expr.column);
        }
      } else {
        for (const table of tables) addTo(columns, table, expr.column);
      }
      return;
    }
    case "const":
      if (expr.value.kind === "id") ids.add(expr.value.name);
      if (expr.value.kind === "enum") enums.add(expr.value.value);
      return;
    case "eq":
      collectExpr(expr.left, aliases, tables, columns, ids, enums);
      collectExpr(expr.right, aliases, tables, columns, ids, enums);
      return;
    case "isNull":
      collectExpr(expr.expr, aliases, tables, columns, ids, enums);
      return;
    case "and":
    case "or":
      for (const arg of expr.args) {
        collectExpr(arg, aliases, tables, columns, ids, enums);
      }
      return;
    case "not":
      collectExpr(expr.expr, aliases, tables, columns, ids, enums);
      return;
  }
}

function collectPlanExprs(
  plan: Plan,
  aliases: ReadonlyMap<string, string>,
  tables: readonly string[],
  columns: Map<string, Set<string>>,
  ids: Set<string>,
  enums: Set<string>,
): void {
  switch (plan.kind) {
    case "scan":
      return;
    case "filter":
      collectExpr(plan.predicate, aliases, tables, columns, ids, enums);
      collectPlanExprs(plan.input, aliases, tables, columns, ids, enums);
      return;
    case "project":
      for (const entry of plan.columns) {
        collectExpr(entry.expr, aliases, tables, columns, ids, enums);
      }
      collectPlanExprs(plan.input, aliases, tables, columns, ids, enums);
      return;
    case "join":
      collectExpr(plan.on, aliases, tables, columns, ids, enums);
      collectPlanExprs(plan.left, aliases, tables, columns, ids, enums);
      collectPlanExprs(plan.right, aliases, tables, columns, ids, enums);
      return;
    case "unionAll":
      for (const input of plan.inputs) {
        collectPlanExprs(input, aliases, tables, columns, ids, enums);
      }
      return;
  }
}

function nodeName(table: string, column: string): string {
  return `${table}\u0000${column}`;
}

function directColumn(
  expr: Expr,
  aliases: ReadonlyMap<string, string>,
  tables: readonly string[],
): { table: string; column: string }[] {
  if (expr.kind !== "column") return [];
  if (expr.table !== undefined && expr.table !== "") {
    const table = aliases.get(expr.table) ?? expr.table;
    return tables.includes(table) ? [{ table, column: expr.column }] : [];
  }
  return tables.map((table) => ({ table, column: expr.column }));
}

function collectEqualityUsage(
  plan: Plan,
  aliases: ReadonlyMap<string, string>,
  tables: readonly string[],
  usage: Map<string, { id: boolean; enums: boolean }>,
  edges: Map<string, Set<string>>,
): void {
  const mark = (table: string, column: string, kind: "id" | "enum"): void => {
    const key = nodeName(table, column);
    const current = usage.get(key) ?? { id: false, enums: false };
    if (kind === "id") current.id = true;
    else current.enums = true;
    usage.set(key, current);
  };
  const markConst = (node: { table: string; column: string }, expr: Expr): void => {
    if (expr.kind === "const") {
      if (expr.value.kind === "id") mark(node.table, node.column, "id");
      if (expr.value.kind === "enum") mark(node.table, node.column, "enum");
    }
  };
  const connect = (left: string, right: string): void => {
    let set = edges.get(left);
    if (set === undefined) {
      set = new Set();
      edges.set(left, set);
    }
    set.add(right);
    let reverse = edges.get(right);
    if (reverse === undefined) {
      reverse = new Set();
      edges.set(right, reverse);
    }
    reverse.add(left);
  };

  const walk = (expr: Expr): void => {
    switch (expr.kind) {
      case "eq": {
        const leftColumns = directColumn(expr.left, aliases, tables);
        const rightColumns = directColumn(expr.right, aliases, tables);
        for (const node of leftColumns) markConst(node, expr.right);
        for (const node of rightColumns) markConst(node, expr.left);
        for (const left of leftColumns) {
          for (const right of rightColumns) {
            connect(nodeName(left.table, left.column), nodeName(right.table, right.column));
          }
        }
        walk(expr.left);
        walk(expr.right);
        return;
      }
      case "and":
      case "or":
        for (const arg of expr.args) walk(arg);
        return;
      case "isNull":
        walk(expr.expr);
        return;
      case "not":
        walk(expr.expr);
        return;
      default:
        return;
    }
  };

  const walkPlan = (node: Plan): void => {
    switch (node.kind) {
      case "scan":
        return;
      case "filter":
        walk(node.predicate);
        walkPlan(node.input);
        return;
      case "project":
        for (const entry of node.columns) walk(entry.expr);
        walkPlan(node.input);
        return;
      case "join":
        walk(node.on);
        walkPlan(node.left);
        walkPlan(node.right);
        return;
      case "unionAll":
        for (const input of node.inputs) walkPlan(input);
        return;
    }
  };

  walkPlan(plan);
}

export function analyzePlans(
  plans: readonly Plan[],
  policy?: TenantReadPolicy,
  schema?: DatabaseSchema,
): PlanAnalysis {
  const aliasMap = new Map<string, string>();
  const tables = new Set<string>();
  for (const plan of plans) collectScans(plan, aliasMap, tables);

  const tableList = [...tables].sort();
  const columns = new Map<string, Set<string>>();
  const ids = new Set<string>();
  const enums = new Set<string>();

  for (const plan of plans) {
    collectPlanExprs(plan, aliasMap, tableList, columns, ids, enums);
  }

  const usage = new Map<string, { id: boolean; enums: boolean }>();
  const edges = new Map<string, Set<string>>();
  for (const plan of plans) {
    collectEqualityUsage(plan, aliasMap, tableList, usage, edges);
  }

  const tenantColumns: Record<string, string> = {};
  const labels = policyTenantLabels(policy);
  const policyNeedsTenant =
    policy !== undefined &&
    (
      labels.length > 0 ||
      policy.canRead !== undefined ||
      policy.rowVisible !== undefined ||
      (policy.rules?.length ?? 0) > 0 ||
      policy.allowNullTenant === true ||
      policy.tenantColumns !== undefined
    );
  for (const table of tableList) {
    tenantColumns[table] = policy?.tenantColumns?.[table] ?? policy?.tenantColumn ?? "tenant";
    if (policyNeedsTenant || policy?.tenantColumns?.[table] !== undefined) {
      addTo(columns, table, tenantColumns[table]);
    }
  }

  for (const label of labels) enums.add(label);

  const columnsByTable: Record<string, string[]> = {};
  for (const table of tableList) {
    columnsByTable[table] = [...(columns.get(table) ?? [])].sort();
  }

  const aliasesByTable: Record<string, string[]> = {};
  for (const table of tableList) {
    aliasesByTable[table] = [...aliasMap.entries()]
      .filter(([, value]) => value === table)
      .map(([alias]) => alias)
      .sort();
  }

  return {
    tables: tableList,
    aliases: Object.fromEntries(aliasMap),
    aliasesByTable,
    columnsByTable,
    identifierConstants: [...ids].sort(),
    enumLiterals: [...enums].sort(),
    tenantColumns,
    columnUsage: Object.fromEntries(
      [...usage.entries()].map(([key, value]) => [key, { id: value.id, enums: value.enums }]),
    ),
    equalityEdges: Object.fromEntries(
      [...edges.entries()].map(([key, value]) => [key, [...value].sort()]),
    ),
  };
}
