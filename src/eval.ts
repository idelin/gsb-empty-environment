import { PolicyError } from "./errors.ts";
import type { CompiledPolicy, Condition, IntCmp } from "./policy.ts";

export class InvalidRowAttribute extends Error {
  readonly attribute: string;
  constructor(attribute: string) {
    super(`invalid value for row attribute "${attribute}"`);
    this.name = "InvalidRowAttribute";
    this.attribute = attribute;
  }
}

export interface EvalContext {
  readonly subject: Readonly<Record<string, unknown>>;
  readonly row: Readonly<Record<string, unknown>>;
}

function readAttribute(policy: CompiledPolicy, ref: string, ctx: EvalContext): string | number {
  const type = policy.attributeTypes.get(ref);
  if (type === undefined) {
    throw new PolicyError(`unknown attribute "${ref}"`);
  }
  const dot = ref.indexOf(".");
  const scope = ref.slice(0, dot);
  const name = ref.slice(dot + 1);
  const source = scope === "subject" ? ctx.subject : ctx.row;
  const value = source[name];
  const valid =
    type.kind === "integer"
      ? typeof value === "number" && Number.isInteger(value)
      : typeof value === "string" && type.values.includes(value);
  if (!valid) {
    if (scope === "row") {
      throw new InvalidRowAttribute(ref);
    }
    throw new PolicyError(`invalid or missing value for subject attribute "${ref}"`);
  }
  return value as string | number;
}

function compareInt(left: number, cmp: IntCmp, right: number): boolean {
  switch (cmp) {
    case "eq": return left === right;
    case "ne": return left !== right;
    case "lt": return left < right;
    case "lte": return left <= right;
    case "gt": return left > right;
    case "gte": return left >= right;
  }
}

export function evaluateCondition(
  policy: CompiledPolicy,
  cond: Condition,
  ctx: EvalContext,
): boolean {
  switch (cond.op) {
    case "and":
      return cond.all.every((child) => evaluateCondition(policy, child, ctx));
    case "or":
      return cond.any.some((child) => evaluateCondition(policy, child, ctx));
    case "not":
      return !evaluateCondition(policy, cond.cond, ctx);
    case "in": {
      const value = readAttribute(policy, cond.attribute, ctx);
      return cond.values.includes(value as string);
    }
    case "int": {
      const left = readAttribute(policy, cond.attribute, ctx) as number;
      const right =
        "otherAttribute" in cond
          ? (readAttribute(policy, cond.otherAttribute, ctx) as number)
          : cond.value;
      return compareInt(left, cond.cmp, right);
    }
  }
}
