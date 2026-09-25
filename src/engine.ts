import { PolicyError } from "./errors.ts";
import { evaluateCondition, InvalidRowAttribute } from "./eval.ts";
import type { EvalContext } from "./eval.ts";
import { MASKED } from "./mask.ts";
import type { MaskedMarker } from "./mask.ts";
import type { CompiledPolicy } from "./policy.ts";

export type Row = Readonly<Record<string, unknown>>;

export type OutputCell = unknown | MaskedMarker;

export interface SortSpec {
  readonly field: string;
  readonly direction?: "asc" | "desc";
}

export type AggregateFn = "count" | "sum" | "min" | "max" | "avg";

export interface AggregateSpec {
  readonly fn: AggregateFn;
  readonly field?: string;
  readonly as: string;
}

export interface QuerySpec {
  readonly select?: readonly string[];
  readonly sortBy?: SortSpec;
  readonly limit?: number;
  readonly aggregates?: readonly AggregateSpec[];
}

export interface QueryResult {
  readonly columns: readonly string[];
  readonly rows: ReadonlyArray<Record<string, OutputCell>>;
  readonly aggregates: Record<string, number | null>;
}

function validateSelect(policy: CompiledPolicy, select: readonly string[] | undefined): readonly string[] {
  if (select === undefined) return policy.publicFields;
  if (select.length === 0 || new Set(select).size !== select.length) {
    throw new PolicyError("select must be a non-empty set of unique field names");
  }
  for (const field of select) {
    if (!policy.publicFields.includes(field)) {
      throw new PolicyError(`select references field "${field}" which is not in the public schema`);
    }
  }
  return select;
}

function validateSort(policy: CompiledPolicy, sortBy: SortSpec | undefined): void {
  if (sortBy === undefined) return;
  if (!policy.publicFields.includes(sortBy.field)) {
    throw new PolicyError(`sort field "${sortBy.field}" is not in the public schema`);
  }
  if (policy.maskableFields.has(sortBy.field)) {
    throw new PolicyError(
      `cannot sort by field "${sortBy.field}": the policy may mask it, ordering could leak masked values`,
    );
  }
}

function validateLimit(limit: number | undefined): void {
  if (limit === undefined) return;
  if (!Number.isInteger(limit) || limit < 0) {
    throw new PolicyError("limit must be a non-negative integer");
  }
}

function validateAggregates(
  policy: CompiledPolicy,
  aggregates: readonly AggregateSpec[] | undefined,
): readonly AggregateSpec[] {
  if (aggregates === undefined) return [];
  const names = new Set<string>();
  for (const agg of aggregates) {
    if (agg.as.length === 0 || names.has(agg.as)) {
      throw new PolicyError("aggregate output names must be non-empty and unique");
    }
    names.add(agg.as);
    if (agg.fn === "count") continue;
    if (agg.field === undefined) {
      throw new PolicyError(`aggregate "${agg.as}" (${agg.fn}) requires a field`);
    }
    if (!policy.publicFields.includes(agg.field)) {
      throw new PolicyError(`aggregate "${agg.as}" references field "${agg.field}" which is not in the public schema`);
    }
    if (policy.maskableFields.has(agg.field)) {
      throw new PolicyError(
        `cannot aggregate field "${agg.field}": the policy may mask it, the result could leak masked values`,
      );
    }
  }
  return aggregates;
}

function compareCells(a: unknown, b: unknown): number {
  if (a === null && b === null) return 0;
  if (a === null) return 1;
  if (b === null) return -1;
  if (typeof a === "number" && typeof b === "number") return a - b;
  if (typeof a === "string" && typeof b === "string") return a < b ? -1 : a > b ? 1 : 0;
  throw new PolicyError("cannot sort field with mixed or unsupported value types");
}

function computeAggregates(
  aggregates: readonly AggregateSpec[],
  rows: ReadonlyArray<Record<string, OutputCell>>,
): Record<string, number | null> {
  const out: Record<string, number | null> = {};
  for (const agg of aggregates) {
    if (agg.fn === "count") {
      out[agg.as] = rows.length;
      continue;
    }
    const field = agg.field as string;
    let count = 0;
    let sum = 0;
    let min = Number.POSITIVE_INFINITY;
    let max = Number.NEGATIVE_INFINITY;
    for (const row of rows) {
      const value = row[field];
      if (value === null || value === undefined) continue;
      if (typeof value !== "number") {
        throw new PolicyError(`aggregate "${agg.as}": field "${field}" contains non-numeric values`);
      }
      count += 1;
      sum += value;
      if (value < min) min = value;
      if (value > max) max = value;
    }
    switch (agg.fn) {
      case "sum": out[agg.as] = count === 0 ? null : sum; break;
      case "min": out[agg.as] = count === 0 ? null : min; break;
      case "max": out[agg.as] = count === 0 ? null : max; break;
      case "avg": out[agg.as] = count === 0 ? null : sum / count; break;
    }
  }
  return out;
}

export function executeQuery(
  policy: CompiledPolicy,
  subject: Readonly<Record<string, unknown>>,
  table: readonly Row[],
  query: QuerySpec = {},
): QueryResult {
  const columns = validateSelect(policy, query.select);
  validateSort(policy, query.sortBy);
  validateLimit(query.limit);
  const aggregates = validateAggregates(policy, query.aggregates);

  const visibleRows: Array<Record<string, OutputCell>> = [];
  for (const row of table) {
    const ctx: EvalContext = { subject, row };
    let visible: boolean;
    try {
      visible = evaluateCondition(policy, policy.spec.rowVisibleWhen, ctx);
    } catch (err) {
      if (err instanceof InvalidRowAttribute) continue;
      throw err;
    }
    if (!visible) continue;

    const maskedFields = new Set<string>();
    for (const rule of policy.spec.maskRules) {
      let applies: boolean;
      try {
        applies = evaluateCondition(policy, rule.when, ctx);
      } catch (err) {
        if (err instanceof InvalidRowAttribute) {
          applies = true;
        } else {
          throw err;
        }
      }
      if (applies) {
        for (const field of rule.fields) maskedFields.add(field);
      }
    }

    const out: Record<string, OutputCell> = {};
    for (const field of policy.publicFields) {
      const value = row[field] === undefined ? null : row[field];
      out[field] = maskedFields.has(field) ? MASKED : value;
    }
    visibleRows.push(out);
  }

  if (query.sortBy !== undefined) {
    const { field, direction = "asc" } = query.sortBy;
    const sign = direction === "desc" ? -1 : 1;
    visibleRows.sort((a, b) => sign * compareCells(a[field], b[field]));
  }

  const aggregateValues = computeAggregates(aggregates, visibleRows);

  const limited = query.limit === undefined ? visibleRows : visibleRows.slice(0, query.limit);
  const projected = limited.map((row) => {
    const out: Record<string, OutputCell> = {};
    for (const field of columns) out[field] = row[field];
    return out;
  });

  return { columns, rows: projected, aggregates: aggregateValues };
}
