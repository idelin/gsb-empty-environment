import { createHash } from "node:crypto";
import type { Expr, Plan, Projection, SqlValue } from "./types.ts";
import { NULL, enumValue, idValue, normalizeValue } from "./value.ts";

export function column(table: string | undefined, name: string): Expr {
  return table === undefined
    ? { kind: "column", column: name }
    : { kind: "column", table, column: name };
}

export const col = (table: string, name: string): Expr => column(table, name);
export const ref = (name: string): Expr => column(undefined, name);

export function constant(value: SqlValue | string | number | boolean | null): Expr {
  return { kind: "const", value: normalizeValue(value) };
}

export const literal = constant;

export function idConstant(name: string): Expr {
  return { kind: "const", value: idValue(name) };
}

export const idConst = idConstant;

export function enumConstant(value: string | number | boolean): Expr {
  return { kind: "const", value: enumValue(value) };
}

export const enumConst = enumConstant;

export function nullConstant(): Expr {
  return { kind: "const", value: NULL };
}

export function eq(left: Expr, right: Expr): Expr {
  return { kind: "eq", left, right };
}

export const equals = eq;

export function isNull(expr: Expr): Expr {
  return { kind: "isNull", expr };
}

export const isNullExpr = isNull;

export function and(...args: readonly Expr[]): Expr {
  return { kind: "and", args };
}

export const allOf = and;

export function or(...args: readonly Expr[]): Expr {
  return { kind: "or", args };
}

export const anyOf = or;

export function not(expr: Expr): Expr {
  return { kind: "not", expr };
}

export function scan(table: string, alias?: string): Plan {
  return alias === undefined ? { kind: "scan", table } : { kind: "scan", table, alias };
}

export const table = scan;
export const fromTable = scan;

export function join(
  joinType: "inner" | "left",
  left: Plan,
  right: Plan,
  on: Expr,
): Plan {
  return { kind: "join", joinType, left, right, on };
}

export function innerJoin(left: Plan, right: Plan, on: Expr): Plan {
  return join("inner", left, right, on);
}

export function leftJoin(left: Plan, right: Plan, on: Expr): Plan {
  return join("left", left, right, on);
}

export function filter(input: Plan, predicate: Expr): Plan {
  return { kind: "filter", input, predicate };
}

export const where = filter;

export function project(
  input: Plan,
  columns: readonly (Projection | string)[],
): Plan {
  return {
    kind: "project",
    input,
    columns: columns.map((entry) => {
      if (typeof entry !== "string") return entry;
      const dot = entry.lastIndexOf(".");
      return dot > 0
        ? { name: entry, expr: column(entry.slice(0, dot), entry.slice(dot + 1)) }
        : { name: entry, expr: ref(entry) };
    }),
  };
}

export const select = project;

export function projection(name: string, expr: Expr): Projection {
  return { name, expr };
}

export const as = projection;

export function unionAll(...inputs: readonly Plan[]): Plan {
  return { kind: "unionAll", inputs };
}

export const union = unionAll;

function objectRecord(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new PlanSyntaxError("Expected an object");
  }
  return value as Record<string, unknown>;
}

export class PlanSyntaxError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PlanSyntaxError";
  }
}

function normalizeJoinType(raw: unknown): "inner" | "left" {
  const text = String(raw ?? "").toLowerCase().replaceAll("_", " ").trim();
  if (text === "inner" || text === "join" || text === "inner join") return "inner";
  if (
    text === "left" ||
    text === "left outer" ||
    text === "left join" ||
    text === "left outer join"
  ) {
    return "left";
  }
  throw new PlanSyntaxError(`Unsupported join type: ${String(raw)}`);
}

export function normalizeExpr(raw: unknown): Expr {
  if (typeof raw === "string") return ref(raw);
  const record = objectRecord(raw);
  const kind = String(record.kind ?? record.op ?? record.type ?? "")
    .toLowerCase()
    .replaceAll("_", " ")
    .replaceAll(" ", "");

  if (kind === "column" || kind === "col" || kind === "ref") {
    const name = String(record.column ?? record.name ?? record.field ?? "");
    if (!name) throw new PlanSyntaxError("Column expression has no name");
    const qualifier = record.table ?? record.alias ?? record.qualifier;
    return qualifier === undefined || qualifier === null || qualifier === ""
      ? ref(name)
      : column(String(qualifier), name);
  }
  if (kind === "const" || kind === "constant" || kind === "literal" || kind === "value") {
    return constant(normalizeValue(record.value ?? record));
  }
  if (kind === "id" || kind === "identifier") {
    return idConstant(String(record.name ?? record.value));
  }
  if (kind === "enum") {
    return enumConstant(String(record.value ?? record.name));
  }
  if (kind === "null") {
    return nullConstant();
  }
  if (kind === "eq" || kind === "equals" || kind === "=" || kind === "==") {
    return eq(
      normalizeExpr(record.left ?? record.lhs ?? record.a),
      normalizeExpr(record.right ?? record.rhs ?? record.b),
    );
  }
  if (kind === "isnull" || kind === "is null" || kind === "null?") {
    return isNull(normalizeExpr(record.expr ?? record.arg ?? record.value));
  }
  if (kind === "and" || kind === "&&") {
    const args = record.args ?? record.exprs ?? record.values;
    if (!Array.isArray(args)) throw new PlanSyntaxError("AND requires an expression array");
    return and(...args.map(normalizeExpr));
  }
  if (kind === "or" || kind === "||") {
    const args = record.args ?? record.exprs ?? record.values;
    if (!Array.isArray(args)) throw new PlanSyntaxError("OR requires an expression array");
    return or(...args.map(normalizeExpr));
  }
  if (kind === "not" || kind === "!") {
    return not(normalizeExpr(record.expr ?? record.arg ?? record.value));
  }
  throw new PlanSyntaxError(`Unsupported expression kind: ${String(raw)}`);
}

export function normalizePlan(raw: unknown): Plan {
  const record = objectRecord(raw);
  const kind = String(record.kind ?? record.type ?? "")
    .toLowerCase()
    .replaceAll("_", " ")
    .replaceAll(" ", "");

  if (kind === "scan" || kind === "table" || kind === "relation" || kind === "from") {
    const name = String(record.table ?? record.name ?? record.relation ?? "");
    if (!name) throw new PlanSyntaxError("Scan has no table name");
    const alias = record.alias ?? record.as;
    return scan(name, alias === undefined || alias === null ? undefined : String(alias));
  }
  if (kind === "join" || kind === "innerjoin" || kind === "leftjoin" ||
      ((kind === "inner" || kind === "left") && record.left !== undefined)) {
    const impliedJoin =
      kind === "leftjoin" || kind === "left"
        ? "left"
        : kind === "innerjoin" || kind === "inner"
          ? "inner"
          : undefined;
    const nestedJoinType = String(record.type ?? "").toLowerCase();
    return join(
      normalizeJoinType(
        record.joinType ??
          record.join ??
          record.kindOfJoin ??
          record.side ??
          impliedJoin ??
          (nestedJoinType.includes("left") || nestedJoinType.includes("inner")
            ? nestedJoinType
            : undefined),
      ),
      normalizePlan(record.left),
      normalizePlan(record.right),
      normalizeExpr(record.on ?? record.predicate ?? record.condition),
    );
  }
  if (kind === "filter" || kind === "where" || kind === "selection") {
    return filter(
      normalizePlan(record.input ?? record.child ?? record.source),
      normalizeExpr(record.predicate ?? record.where ?? record.condition ?? record.expr),
    );
  }
  if (kind === "project" || kind === "projection" || kind === "select") {
    const rawColumns = record.columns ?? record.projections ?? record.select;
    if (!Array.isArray(rawColumns)) throw new PlanSyntaxError("Projection requires columns");
    const columns: Projection[] = rawColumns.map((entry, index) => {
      if (typeof entry === "string") {
        const dot = entry.lastIndexOf(".");
        return dot > 0
          ? { name: entry, expr: column(entry.slice(0, dot), entry.slice(dot + 1)) }
          : { name: entry, expr: ref(entry) };
      }
      const item = objectRecord(entry);
      const expr = item.expr ?? item.expression ?? item.value;
      if (expr === undefined) {
        const name = String(item.column ?? item.name ?? `c${index}`);
        return { name, expr: ref(name) };
      }
      return {
        name: String(item.name ?? item.alias ?? item.as ?? `c${index}`),
        expr: normalizeExpr(expr),
      };
    });
    return project(normalizePlan(record.input ?? record.child ?? record.source), columns);
  }
  if (kind === "unionall" || kind === "union") {
    const inputs = record.inputs ?? record.branches ?? record.children;
    if (!Array.isArray(inputs)) throw new PlanSyntaxError("UNION ALL requires inputs");
    return unionAll(...inputs.map(normalizePlan));
  }
  throw new PlanSyntaxError(`Unsupported plan kind: ${String(raw)}`);
}

export function validatePlan(plan: Plan): void {
  const walk = (node: Plan, insideJoin: boolean): number => {
    switch (node.kind) {
      case "scan":
        if (!node.table) throw new PlanSyntaxError("Scan table is empty");
        return 1;
      case "join": {
        if (node.joinType !== "inner" && node.joinType !== "left") {
          throw new PlanSyntaxError("Only INNER and LEFT joins are supported");
        }
        if (insideJoin) throw new PlanSyntaxError("Nested joins are outside the two-table fragment");
        const leftScans = walk(node.left, true);
        const rightScans = walk(node.right, true);
        if (leftScans + rightScans > 2) {
          throw new PlanSyntaxError("A join branch may contain at most two table scans");
        }
        validateExpr(node.on);
        return leftScans + rightScans;
      }
      case "filter":
        {
          const count = walk(node.input, insideJoin);
          validateExpr(node.predicate);
          return count;
        }
      case "project":
        {
          const count = walk(node.input, insideJoin);
          for (const entry of node.columns) {
            if (!entry.name) throw new PlanSyntaxError("Projection name is empty");
            validateExpr(entry.expr);
          }
          return count;
        }
      case "unionAll":
        if (node.inputs.length === 0) throw new PlanSyntaxError("UNION ALL has no inputs");
        if (insideJoin) {
          throw new PlanSyntaxError("UNION ALL is not allowed inside a two-table join branch");
        }
        {
          let widest = 0;
          for (const input of node.inputs) widest = Math.max(widest, walk(input, false));
          return widest;
        }
      default: {
        const neverNode: never = node;
        throw new PlanSyntaxError(`Unsupported plan node: ${JSON.stringify(neverNode)}`);
      }
    }
  };
  walk(plan, false);
}

export function validateExpr(expr: Expr): void {
  const requireValue = (value: Expr): void => {
    if (value.kind !== "column" && value.kind !== "const") {
      throw new PlanSyntaxError("Equality and IS NULL require scalar column/constant operands");
    }
    validateExpr(value);
  };
  switch (expr.kind) {
    case "column":
      if (!expr.column) throw new PlanSyntaxError("Column name is empty");
      return;
    case "const":
      if (
        expr.value === null ||
        typeof expr.value !== "object" ||
        (expr.value.kind !== "null" && expr.value.kind !== "enum" && expr.value.kind !== "id")
      ) {
        throw new PlanSyntaxError("Invalid constant value");
      }
      return;
    case "eq":
      requireValue(expr.left);
      requireValue(expr.right);
      return;
    case "isNull":
      requireValue(expr.expr);
      return;
    case "and":
    case "or":
      for (const arg of expr.args) validateExpr(arg);
      return;
    case "not":
      validateExpr(expr.expr);
      return;
    default: {
      const neverExpr: never = expr;
      throw new PlanSyntaxError(`Unsupported expression: ${JSON.stringify(neverExpr)}`);
    }
  }
}

export function canonicalJson(value: unknown): string {
  const encode = (item: unknown): string => {
    if (item === null || item === undefined) return "null";
    if (typeof item === "number") {
      if (!Number.isFinite(item)) return "null";
      return JSON.stringify(item);
    }
    if (typeof item === "boolean") return item ? "true" : "false";
    if (typeof item === "string") return JSON.stringify(item);
    if (typeof item === "function") return JSON.stringify(`[function:${item.name}]`);
    if (Array.isArray(item)) return `[${item.map(encode).join(",")}]`;
    if (typeof item === "object") {
      const record = item as Record<string, unknown>;
      const keys = Object.keys(record).sort();
      return `{${keys.map((key) => `${JSON.stringify(key)}:${encode(record[key])}`).join(",")}}`;
    }
    return JSON.stringify(String(item));
  };
  return encode(value);
}

export function digest(value: unknown): string {
  return createHash("sha256").update(canonicalJson(value)).digest("hex");
}

export function planDigest(plan: Plan): string {
  return digest(plan);
}
