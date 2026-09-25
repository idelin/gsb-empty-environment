import type { AttributeValue, Attributes, IntegerOp, Policy } from "./types.ts";

const INTEGER_OPS: ReadonlySet<IntegerOp> = new Set(["==", "!=", "<", "<=", ">", ">="]);

/**
 * Structural validation: predicates may only reference a single explicit
 * attribute, compare against finite literals, and combine via boolean ops.
 */
export function validatePolicy(policy: Policy, path = "$"): void {
  switch (policy.kind) {
    case "allowAll":
    case "denyAll":
      return;
    case "attrIn": {
      assertNonEmptyAttr(policy.attr, path);
      if (!Array.isArray(policy.values) || policy.values.length === 0) {
        throw new Error(`${path}.attrIn requires a non-empty finite value set`);
      }
      if (!policy.values.every(isScalar)) {
        throw new Error(`${path}.attrIn values must be finite scalars`);
      }
      return;
    }
    case "intCmp": {
      assertNonEmptyAttr(policy.attr, path);
      if (!INTEGER_OPS.has(policy.op)) {
        throw new Error(`${path}.intCmp has invalid operator: ${String((policy as { op: unknown }).op)}`);
      }
      if (!Number.isInteger(policy.value)) {
        throw new Error(`${path}.intCmp literal must be an integer`);
      }
      return;
    }
    case "not":
      validatePolicy(policy.operand, `${path}.not`);
      return;
    case "and":
    case "or": {
      if (!Array.isArray(policy.operands) || policy.operands.length === 0) {
        throw new Error(`${path}.${policy.kind} requires at least one operand`);
      }
      policy.operands.forEach((operand, index) => {
        validatePolicy(operand, `${path}.${policy.kind}[${index}]`);
      });
      return;
    }
    default: {
      const exhaustive: never = policy;
      throw new Error(`${path}: unknown policy kind ${JSON.stringify(exhaustive)}`);
    }
  }
}

function assertNonEmptyAttr(attr: unknown, path: string): asserts attr is string {
  if (typeof attr !== "string" || attr.length === 0) {
    throw new Error(`${path}: attribute name must be a non-empty string`);
  }
}

function isScalar(value: unknown): value is string | number | boolean {
  return typeof value === "string" || typeof value === "boolean" || typeof value === "number";
}

/**
 * Evaluation reads only the supplied attributes map; there is no lookup hook
 * of any kind. Missing attributes and type mismatches fail closed (false).
 */
export function evalPolicy(policy: Policy, attrs: Attributes): boolean {
  switch (policy.kind) {
    case "allowAll":
      return true;
    case "denyAll":
      return false;
    case "attrIn": {
      const raw = ownAttr(attrs, policy.attr);
      if (raw === undefined) return false;
      if (Array.isArray(raw)) return raw.some((v) => policy.values.includes(v));
      return policy.values.includes(raw);
    }
    case "intCmp": {
      const raw = ownAttr(attrs, policy.attr);
      if (typeof raw !== "number" || !Number.isInteger(raw) || !Number.isInteger(policy.value)) {
        return false;
      }
      switch (policy.op) {
        case "==": return raw === policy.value;
        case "!=": return raw !== policy.value;
        case "<": return raw < policy.value;
        case "<=": return raw <= policy.value;
        case ">": return raw > policy.value;
        case ">=": return raw >= policy.value;
      }
      return false;
    }
    case "not":
      return !evalPolicy(policy.operand, attrs);
    case "and":
      return policy.operands.every((operand) => evalPolicy(operand, attrs));
    case "or":
      return policy.operands.some((operand) => evalPolicy(operand, attrs));
  }
}

export const allowAll = (): Policy => ({ kind: "allowAll" });
export const denyAll = (): Policy => ({ kind: "denyAll" });
export const attrIn = (attr: string, values: ReadonlyArray<string | number | boolean>): Policy => ({
  kind: "attrIn",
  attr,
  values,
});
export const intCmp = (attr: string, op: IntegerOp, value: number): Policy => ({ kind: "intCmp", attr, op, value });
export const not = (operand: Policy): Policy => ({ kind: "not", operand });
export const and = (...operands: Policy[]): Policy => {
  if (operands.length === 1) return operands[0];
  const flat: Policy[] = [];
  for (const operand of operands) {
    if (operand.kind === "denyAll") return denyAll();
    if (operand.kind === "and") flat.push(...operand.operands);
    else if (operand.kind !== "allowAll") flat.push(operand);
  }
  return flat.length === 0 ? allowAll() : flat.length === 1 ? flat[0] : { kind: "and", operands: flat };
};
export const or = (...operands: Policy[]): Policy => {
  if (operands.length === 1) return operands[0];
  const flat: Policy[] = [];
  for (const operand of operands) {
    if (operand.kind === "allowAll") return allowAll();
    if (operand.kind === "or") flat.push(...operand.operands);
    else if (operand.kind !== "denyAll") flat.push(operand);
  }
  return flat.length === 0 ? denyAll() : flat.length === 1 ? flat[0] : { kind: "or", operands: flat };
};

function ownAttr(attrs: Attributes, name: string): AttributeValue | undefined {
  return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : undefined;
}
