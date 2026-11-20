import type {
  Database,
  Expr,
  Plan,
  SqlValue,
  TenantReadPolicy,
} from "./types.ts";
import { evalPredicate, evalValue, type ExpressionRow } from "./expr.ts";
import { normalizePolicy, type NormalizedPolicy, normalizeRow } from "./policy.ts";
import { NULL } from "./value.ts";

interface Cell {
  readonly table: string;
  readonly alias: string;
  readonly column: string;
  readonly value: SqlValue;
}

interface EvalRow {
  readonly cells: readonly Cell[];
  readonly tuple?: readonly SqlValue[];
}

export interface EvaluationOptions {
  readonly columnsByTable?: Readonly<Record<string, readonly string[]>>;
  readonly policy?: TenantReadPolicy;
}

export interface ColumnSpec {
  readonly table: string;
  readonly alias: string;
  readonly column: string;
}

function scanSpec(table: string, alias: string, columns: readonly string[]): ColumnSpec[] {
  return columns.map((column) => ({ table, alias, column }));
}

export function planOutputSpec(
  plan: Plan,
  columnsByTable: Readonly<Record<string, readonly string[]>> = {},
): ColumnSpec[] {
  switch (plan.kind) {
    case "scan": {
      const alias = plan.alias ?? plan.table;
      return scanSpec(plan.table, alias, columnsByTable[plan.table] ?? []);
    }
    case "filter":
      return planOutputSpec(plan.input, columnsByTable);
    case "join":
      return [
        ...planOutputSpec(plan.left, columnsByTable),
        ...planOutputSpec(plan.right, columnsByTable),
      ];
    case "project":
      return plan.columns.map((entry) => ({
        table: "",
        alias: "",
        column: entry.name,
      }));
    case "unionAll": {
      const width = plan.inputs.length === 0
        ? 0
        : planOutputSpec(plan.inputs[0], columnsByTable).length;
      return Array.from({ length: width }, (_, index) => ({
        table: "",
        alias: "",
        column: `c${index}`,
      }));
    }
  }
}

function nullRow(spec: readonly ColumnSpec[]): EvalRow {
  return {
    cells: spec.map((entry) => ({
      table: entry.table,
      alias: entry.alias,
      column: entry.column,
      value: NULL,
    })),
  };
}

function combine(left: EvalRow, right: EvalRow): EvalRow {
  return { cells: [...left.cells, ...right.cells] };
}

function asExpressionRow(row: EvalRow): ExpressionRow {
  return row.tuple === undefined
    ? { entries: row.cells }
    : { entries: row.cells, tuple: row.tuple };
}

export function rowToTuple(row: EvalRow): SqlValue[] {
  if (row.tuple !== undefined) return [...row.tuple];
  const sorted = [...row.cells].sort((left, right) => {
    const column = left.column.localeCompare(right.column);
    if (column !== 0) return column;
    return left.table.localeCompare(right.table);
  });
  return sorted.map((entry) => entry.value);
}

function evaluateRows(
  plan: Plan,
  db: Database,
  policy: NormalizedPolicy,
  columnsByTable: Readonly<Record<string, readonly string[]>>,
): EvalRow[] {
  switch (plan.kind) {
    case "scan": {
      const alias = plan.alias ?? plan.table;
      const rows = db[plan.table] ?? [];
      const result: EvalRow[] = [];
      for (const raw of rows) {
        const normalized = normalizeRow(raw);
        if (!policy.visible(plan.table, normalized)) continue;
        const columns = new Set([
          ...(columnsByTable[plan.table] ?? []),
          ...Object.keys(normalized),
        ]);
        const cells = [...columns].map((column) => ({
          table: plan.table,
          alias,
          column,
          value: normalized[column] ?? NULL,
        }));
        result.push({ cells });
      }
      return result;
    }

    case "filter": {
      return evaluateRows(plan.input, db, policy, columnsByTable).filter(
        (row) => evalPredicate(plan.predicate, asExpressionRow(row)) === true,
      );
    }

    case "join": {
      const leftRows = evaluateRows(plan.left, db, policy, columnsByTable);
      const rightRows = evaluateRows(plan.right, db, policy, columnsByTable);
      const rightSpec = planOutputSpec(plan.right, columnsByTable);
      const result: EvalRow[] = [];
      for (const left of leftRows) {
        let matched = false;
        for (const right of rightRows) {
          const combined = combine(left, right);
          if (evalPredicate(plan.on, asExpressionRow(combined)) === true) {
            result.push(combined);
            matched = true;
          }
        }
        if (!matched && plan.joinType === "left") {
          result.push(combine(left, nullRow(rightSpec)));
        }
      }
      return result;
    }

    case "project": {
      const input = evaluateRows(plan.input, db, policy, columnsByTable);
      return input.map((row) => {
        const tuple = plan.columns.map((entry) =>
          evalValue(entry.expr, asExpressionRow(row)),
        );
        return {
          cells: plan.columns.map((entry, index) => ({
            table: "",
            alias: "",
            column: entry.name || `c${index}`,
            value: tuple[index],
          })),
          tuple,
        };
      });
    }

    case "unionAll": {
      const result: EvalRow[] = [];
      for (const input of plan.inputs) {
        for (const row of evaluateRows(input, db, policy, columnsByTable)) {
          const tuple = rowToTuple(row);
          result.push({
            cells: tuple.map((value, index) => ({
              table: "",
              alias: "",
              column: `c${index}`,
              value,
            })),
            tuple,
          });
        }
      }
      return result;
    }
  }
}

export function evaluatePlan(
  plan: Plan,
  db: Database,
  policy?: TenantReadPolicy,
  options: Omit<EvaluationOptions, "policy"> = {},
): SqlValue[][] {
  const normalizedPolicy = normalizePolicy(policy);
  return evaluateRows(plan, db, normalizedPolicy, options.columnsByTable ?? {})
    .map(rowToTuple);
}

export function evaluatePlanWithOptions(
  plan: Plan,
  db: Database,
  options: EvaluationOptions = {},
): SqlValue[][] {
  return evaluatePlan(plan, db, options.policy, options);
}
