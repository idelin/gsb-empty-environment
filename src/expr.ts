import type { Expr, SqlValue } from "./types.ts";
import { equalValues } from "./value.ts";

export type Truth = true | false | null;

export interface ExpressionRow {
  readonly entries: readonly {
    readonly table: string;
    readonly alias: string;
    readonly column: string;
    readonly value: SqlValue;
  }[];
  readonly tuple?: readonly SqlValue[];
}

export function resolveColumn(row: ExpressionRow, table: string | undefined, name: string): SqlValue {
  if (table !== undefined && table !== "") {
    const byAlias = row.entries.find((entry) => entry.alias === table && entry.column === name);
    if (byAlias) return byAlias.value;
    const byTable = row.entries.filter((entry) => entry.table === table && entry.column === name);
    if (byTable.length === 1) return byTable[0].value;
    if (byTable.length > 1) return { kind: "null" };
    return { kind: "null" };
  }

  const match = row.entries.filter((entry) => entry.column === name);
  if (match.length === 1) return match[0].value;
  return { kind: "null" };
}

export function evalValue(expr: Expr, row: ExpressionRow): SqlValue {
  switch (expr.kind) {
    case "column":
      return resolveColumn(row, expr.table, expr.column);
    case "const":
      return expr.value;
    default:
      throw new Error(`Expression ${expr.kind} is not a value expression`);
  }
}

export function evalPredicate(expr: Expr, row: ExpressionRow): Truth {
  switch (expr.kind) {
    case "eq":
      return equalValues(evalValue(expr.left, row), evalValue(expr.right, row));
    case "isNull": {
      const result = evalValue(expr.expr, row);
      return result.kind === "null";
    }
    case "and": {
      let unknown = false;
      for (const arg of expr.args) {
        const result = evalPredicate(arg, row);
        if (result === false) return false;
        if (result === null) unknown = true;
      }
      return unknown ? null : true;
    }
    case "or": {
      let unknown = false;
      for (const arg of expr.args) {
        const result = evalPredicate(arg, row);
        if (result === true) return true;
        if (result === null) unknown = true;
      }
      return unknown ? null : false;
    }
    case "not": {
      const result = evalPredicate(expr.expr, row);
      return result === null ? null : !result;
    }
    default:
      throw new Error(`Expression ${expr.kind} is not a predicate`);
  }
}
