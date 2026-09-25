/**
 * Secure view engine.
 *
 * Pipeline order is fixed and leak-safe:
 *   1. FILTER  — row-visibility predicate; hidden rows are dropped in a single
 *                pass and never counted, sorted, aggregated, or serialized.
 *   2. MASK    — field projection onto the fixed public schema; unrevealed
 *                fields become the MASKED sentinel (distinct from null).
 *   3. SORT    — operates on the masked public view only, so masked originals
 *                cannot influence ordering.
 *   4. AGGREGATE — computed over visible, masked rows only.
 *
 * The policy reads explicit attributes only; it never touches payload data and
 * never calls external identity services.
 */

import { PolicyError, evalCond, validateCond } from "./policy.ts";
import type { Attrs, AttrValue, Cond } from "./policy.ts";

/** Unique sentinel for a masked field. Distinct from null and from any string. */
export const MASKED: unique symbol = Symbol("MASKED");

export type PublicValue = AttrValue | typeof MASKED;

export interface Row {
  /** Explicit attributes — the ONLY input policy conditions may read. */
  readonly attrs: Attrs;
  /** Payload fields. */
  readonly data: Readonly<Record<string, AttrValue>>;
}

export interface Policy {
  /** Fixed public schema: output rows contain exactly these fields, in order. */
  readonly fields: readonly string[];
  /** Row-visibility condition over attributes. */
  readonly row: Cond;
  /**
   * Per-field reveal condition over attributes. Deny by default: a schema
   * field with no reveal condition is always masked.
   */
  readonly reveal: Readonly<Record<string, Cond>>;
}

export interface SortSpec {
  readonly field: string;
  readonly dir?: "asc" | "desc";
}

export type AggFn = "count" | "sum" | "avg" | "min" | "max";

export interface AggSpec {
  readonly fn: AggFn;
  /** Required for sum/avg/min/max; ignored for count. */
  readonly field?: string;
  readonly as: string;
}

export interface Query {
  readonly sort?: readonly SortSpec[];
  readonly aggregates?: readonly AggSpec[];
}

export interface PublicOutput {
  readonly schema: "secure-view/v1";
  readonly fields: readonly string[];
  readonly rows: ReadonlyArray<Readonly<Record<string, PublicValue>>>;
  readonly aggregates: Readonly<Record<string, number | null>>;
  readonly meta: { readonly visibleRows: number };
}

export function validatePolicy(policy: Policy, allowedAttrs: readonly string[]): void {
  if (!Array.isArray(policy.fields) || policy.fields.length === 0) {
    throw new PolicyError("policy must declare a non-empty public schema");
  }
  const seen = new Set<string>();
  for (const f of policy.fields) {
    if (typeof f !== "string" || f.length === 0) {
      throw new PolicyError("schema field names must be non-empty strings");
    }
    if (seen.has(f)) throw new PolicyError(`duplicate schema field: ${f}`);
    seen.add(f);
  }
  const allowed = new Set(allowedAttrs);
  validateCond(policy.row, allowed);
  for (const f of policy.fields) {
    const cond = policy.reveal[f];
    if (cond !== undefined) validateCond(cond, allowed);
  }
  for (const k of Object.keys(policy.reveal)) {
    if (!seen.has(k)) {
      throw new PolicyError(`reveal condition targets non-schema field: ${k}`);
    }
  }
}

function validateQuery(query: Query, schema: ReadonlySet<string>): void {
  for (const s of query.sort ?? []) {
    if (!schema.has(s.field)) {
      throw new PolicyError(`sort field not in public schema: ${s.field}`);
    }
    if (s.dir !== undefined && s.dir !== "asc" && s.dir !== "desc") {
      throw new PolicyError(`invalid sort direction on field: ${s.field}`);
    }
  }
  for (const a of query.aggregates ?? []) {
    if (a.fn !== "count") {
      if (a.field === undefined || !schema.has(a.field)) {
        throw new PolicyError(`aggregate field not in public schema: ${String(a.field)}`);
      }
    }
  }
}

/**
 * Content-independent order: real values rank 0, null ranks 1, MASKED ranks 2.
 * Direction applies only within rank 0, so masked/null positions can never
 * depend on hidden original values.
 */
function rankOf(v: PublicValue): 0 | 1 | 2 {
  if (v === MASKED) return 2;
  if (v === null) return 1;
  return 0;
}

function compareValues(a: AttrValue, b: AttrValue): number {
  if (typeof a === "number" && typeof b === "number") return a - b;
  if (typeof a === "string" && typeof b === "string") return a < b ? -1 : a > b ? 1 : 0;
  if (typeof a === "boolean" && typeof b === "boolean") return (a ? 1 : 0) - (b ? 1 : 0);
  // Mixed types: fixed type order, content-independent.
  const t = (v: AttrValue) => (typeof v === "number" ? 0 : typeof v === "string" ? 1 : 2);
  return t(a) - t(b);
}

function comparePublic(a: PublicValue, b: PublicValue, dir: "asc" | "desc"): number {
  const ra = rankOf(a);
  const rb = rankOf(b);
  if (ra !== rb) return ra - rb;
  if (ra !== 0) return 0;
  const c = compareValues(a as AttrValue, b as AttrValue);
  return dir === "desc" ? -c : c;
}

function projectRow(row: Row, policy: Policy): Record<string, PublicValue> {
  const out: Record<string, PublicValue> = {};
  for (const f of policy.fields) {
    const revealCond = policy.reveal[f] ?? { op: "const", value: false } as const;
    if (evalCond(revealCond, row.attrs)) {
      const v = row.data[f];
      out[f] = v === undefined ? null : v; // missing payload -> genuine null
    } else {
      out[f] = MASKED;
    }
  }
  return out;
}

function aggregate(
  rows: ReadonlyArray<Readonly<Record<string, PublicValue>>>,
  spec: AggSpec,
): number | null {
  if (spec.fn === "count") return rows.length;
  const nums: number[] = [];
  for (const r of rows) {
    const v = r[spec.field as string];
    if (typeof v === "number") nums.push(v); // null and MASKED are skipped
  }
  if (nums.length === 0) return null;
  switch (spec.fn) {
    case "sum": return nums.reduce((a, b) => a + b, 0);
    case "avg": return nums.reduce((a, b) => a + b, 0) / nums.length;
    case "min": return Math.min(...nums);
    case "max": return Math.max(...nums);
  }
  return null;
}

export function executeQuery(
  rows: readonly Row[],
  policy: Policy,
  query: Query = {},
  allowedAttrs: readonly string[] = [],
): PublicOutput {
  validatePolicy(policy, allowedAttrs.length > 0 ? allowedAttrs : collectAttrNames(rows));
  const schema = new Set(policy.fields);
  validateQuery(query, schema);

  // 1+2. Filter, then project/mask — hidden rows never enter any later stage.
  const view: Array<Record<string, PublicValue>> = [];
  for (const r of rows) {
    if (evalCond(policy.row, r.attrs)) view.push(projectRow(r, policy));
  }

  // 3. Sort on the masked public view only (Array.prototype.sort is stable,
  //    so equal-rank masked entries keep input order — never value order).
  const sorts = query.sort ?? [];
  if (sorts.length > 0) {
    view.sort((a, b) => {
      for (const s of sorts) {
        const c = comparePublic(a[s.field] as PublicValue, b[s.field] as PublicValue, s.dir ?? "asc");
        if (c !== 0) return c;
      }
      return 0;
    });
  }

  // 4. Aggregates over the visible, masked view only.
  const aggregates: Record<string, number | null> = {};
  for (const a of query.aggregates ?? []) {
    aggregates[a.as] = aggregate(view, a);
  }

  return {
    schema: "secure-view/v1",
    fields: policy.fields,
    rows: view,
    aggregates,
    meta: { visibleRows: view.length },
  };
}

function collectAttrNames(rows: readonly Row[]): string[] {
  const names = new Set<string>();
  for (const r of rows) for (const k of Object.keys(r.attrs)) names.add(k);
  return [...names];
}

/** JSON-safe rendering: MASKED -> "[MASKED]", null stays null. */
export function toPublicJSON(output: PublicOutput): string {
  return JSON.stringify(output, (_key, value: unknown) =>
    typeof value === "symbol" ? "[MASKED]" : value);
}
