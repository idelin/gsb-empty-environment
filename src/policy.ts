/**
 * Policy condition AST and evaluator.
 *
 * Security contract:
 * - Conditions read ONLY explicit attributes (`Attrs`), never row payload data.
 * - Three construct families only: finite-set membership, integer comparisons,
 *   boolean combinators. No functions, no regex, no external identity lookups.
 * - Evaluation is total (never throws) and carries no side effects, so filter
 *   order cannot leak hidden-row content or counts through errors or effects.
 */

export type AttrValue = string | number | boolean | null;
export type Attrs = Readonly<Record<string, AttrValue>>;

export type IntCmp = "eq" | "ne" | "lt" | "lte" | "gt" | "gte" | "between";

export type Cond =
  | { readonly op: "const"; readonly value: boolean }
  | { readonly op: "in"; readonly attr: string; readonly values: readonly AttrValue[] }
  | {
      readonly op: "int";
      readonly attr: string;
      readonly cmp: IntCmp;
      readonly value: number;
      readonly max?: number;
    }
  | { readonly op: "and"; readonly args: readonly Cond[] }
  | { readonly op: "or"; readonly args: readonly Cond[] }
  | { readonly op: "not"; readonly arg: Cond };

/** Errors name attributes/fields only — never attribute or payload values. */
export class PolicyError extends Error {
  override readonly name = "PolicyError";
}

const MAX_SET_SIZE = 1024;
const INT_CMPS: readonly IntCmp[] = ["eq", "ne", "lt", "lte", "gt", "gte", "between"];

function assertAttrName(attr: unknown, allowed: ReadonlySet<string>): asserts attr is string {
  if (typeof attr !== "string" || attr.length === 0) {
    throw new PolicyError("condition attribute must be a non-empty string");
  }
  if (!allowed.has(attr)) {
    throw new PolicyError(`condition references undeclared attribute: ${attr}`);
  }
}

function assertSafeInteger(n: unknown, what: string): asserts n is number {
  if (typeof n !== "number" || !Number.isSafeInteger(n)) {
    throw new PolicyError(`${what} must be a safe integer`);
  }
}

function validateSetValue(v: unknown): void {
  if (v !== null && typeof v !== "string" && typeof v !== "boolean") {
    if (typeof v === "number" && Number.isSafeInteger(v)) return;
    throw new PolicyError("set members must be strings, booleans, safe integers, or null");
  }
}

export function validateCond(cond: Cond, allowed: ReadonlySet<string>): void {
  if (cond === null || typeof cond !== "object") {
    throw new PolicyError("condition must be an object");
  }
  switch (cond.op) {
    case "const":
      if (typeof cond.value !== "boolean") {
        throw new PolicyError("const condition requires a boolean value");
      }
      return;
    case "in": {
      assertAttrName(cond.attr, allowed);
      if (!Array.isArray(cond.values) || cond.values.length > MAX_SET_SIZE) {
        throw new PolicyError(`in-condition on attribute ${cond.attr} requires a finite set (<= ${MAX_SET_SIZE})`);
      }
      for (const v of cond.values) validateSetValue(v);
      return;
    }
    case "int": {
      assertAttrName(cond.attr, allowed);
      if (!INT_CMPS.includes(cond.cmp)) {
        throw new PolicyError(`unknown integer comparison on attribute ${cond.attr}`);
      }
      assertSafeInteger(cond.value, `comparison bound on attribute ${cond.attr}`);
      if (cond.cmp === "between") {
        assertSafeInteger(cond.max, `upper bound on attribute ${cond.attr}`);
        if ((cond.max as number) < cond.value) {
          throw new PolicyError(`between bounds inverted on attribute ${cond.attr}`);
        }
      }
      return;
    }
    case "and":
    case "or":
      if (!Array.isArray(cond.args)) {
        throw new PolicyError(`${cond.op}-condition requires an argument list`);
      }
      for (const c of cond.args) validateCond(c, allowed);
      return;
    case "not":
      validateCond(cond.arg, allowed);
      return;
    default:
      throw new PolicyError("unknown condition operator");
  }
}

/**
 * Total evaluator: unknown/missing/mistyped attributes make atomic conditions
 * false instead of throwing, so no error path can disclose attribute values.
 */
export function evalCond(cond: Cond, attrs: Attrs): boolean {
  switch (cond.op) {
    case "const":
      return cond.value;
    case "in": {
      const v = attrs[cond.attr];
      if (v === undefined) return false;
      return cond.values.some((m) => Object.is(m, v));
    }
    case "int": {
      const v = attrs[cond.attr];
      if (typeof v !== "number" || !Number.isSafeInteger(v)) return false;
      switch (cond.cmp) {
        case "eq": return v === cond.value;
        case "ne": return v !== cond.value;
        case "lt": return v < cond.value;
        case "lte": return v <= cond.value;
        case "gt": return v > cond.value;
        case "gte": return v >= cond.value;
        case "between": return v >= cond.value && v <= (cond.max as number);
      }
      return false;
    }
    case "and":
      return cond.args.every((c) => evalCond(c, attrs));
    case "or":
      return cond.args.some((c) => evalCond(c, attrs));
    case "not":
      return !evalCond(cond.arg, attrs);
  }
}
