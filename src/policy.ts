import { PolicyError } from "./errors.ts";

export type AttributeType =
  | { readonly kind: "integer" }
  | { readonly kind: "enum"; readonly values: readonly string[] };

export interface AttributeDecl {
  readonly name: string;
  readonly type: AttributeType;
}

export type IntCmp = "eq" | "ne" | "lt" | "lte" | "gt" | "gte";

export type Condition =
  | { readonly op: "in"; readonly attribute: string; readonly values: readonly string[] }
  | { readonly op: "int"; readonly attribute: string; readonly cmp: IntCmp; readonly value: number }
  | { readonly op: "int"; readonly attribute: string; readonly cmp: IntCmp; readonly otherAttribute: string }
  | { readonly op: "and"; readonly all: readonly Condition[] }
  | { readonly op: "or"; readonly any: readonly Condition[] }
  | { readonly op: "not"; readonly cond: Condition };

export interface MaskRule {
  readonly id: string;
  readonly when: Condition;
  readonly fields: readonly string[];
}

export interface PolicySpec {
  readonly subjectAttributes: readonly AttributeDecl[];
  readonly rowAttributes: readonly AttributeDecl[];
  readonly publicFields: readonly string[];
  readonly rowVisibleWhen: Condition;
  readonly maskRules: readonly MaskRule[];
}

export interface CompiledPolicy {
  readonly spec: PolicySpec;
  readonly attributeTypes: ReadonlyMap<string, AttributeType>;
  readonly maskableFields: ReadonlySet<string>;
  readonly publicFields: readonly string[];
}

const ATTR_REF_PATTERN = /^(subject|row)\.[A-Za-z_][A-Za-z0-9_]*$/;

function buildAttributeTypes(spec: PolicySpec): Map<string, AttributeType> {
  const types = new Map<string, AttributeType>();
  for (const scope of ["subject", "row"] as const) {
    const decls = scope === "subject" ? spec.subjectAttributes : spec.rowAttributes;
    for (const decl of decls) {
      const key = `${scope}.${decl.name}`;
      if (types.has(key)) {
        throw new PolicyError(`duplicate attribute declaration "${key}"`);
      }
      if (decl.type.kind === "enum" && decl.type.values.length === 0) {
        throw new PolicyError(`enum attribute "${key}" must declare a non-empty finite value set`);
      }
      types.set(key, decl.type);
    }
  }
  return types;
}

function requireAttribute(types: ReadonlyMap<string, AttributeType>, ref: string): AttributeType {
  if (!ATTR_REF_PATTERN.test(ref)) {
    throw new PolicyError(`malformed attribute reference "${ref}" (expected "subject.<name>" or "row.<name>")`);
  }
  const type = types.get(ref);
  if (type === undefined) {
    throw new PolicyError(`unknown attribute "${ref}": policy may only read explicitly declared attributes`);
  }
  return type;
}

function validateCondition(types: ReadonlyMap<string, AttributeType>, cond: Condition): void {
  switch (cond.op) {
    case "and":
    case "or": {
      const children = cond.op === "and" ? cond.all : cond.any;
      if (children.length === 0) {
        throw new PolicyError(`"${cond.op}" condition requires at least one operand`);
      }
      for (const child of children) validateCondition(types, child);
      return;
    }
    case "not":
      validateCondition(types, cond.cond);
      return;
    case "in": {
      const type = requireAttribute(types, cond.attribute);
      if (type.kind !== "enum") {
        throw new PolicyError(`"in" condition on "${cond.attribute}" requires an enum attribute`);
      }
      if (cond.values.length === 0) {
        throw new PolicyError(`"in" condition on "${cond.attribute}" requires a non-empty finite set`);
      }
      for (const value of cond.values) {
        if (!type.values.includes(value)) {
          throw new PolicyError(
            `"in" condition on "${cond.attribute}": value "${value}" is outside the declared finite set`,
          );
        }
      }
      return;
    }
    case "int": {
      const type = requireAttribute(types, cond.attribute);
      if (type.kind !== "integer") {
        throw new PolicyError(`integer condition on "${cond.attribute}" requires an integer attribute`);
      }
      if ("otherAttribute" in cond) {
        const other = requireAttribute(types, cond.otherAttribute);
        if (other.kind !== "integer") {
          throw new PolicyError(`integer condition on "${cond.otherAttribute}" requires an integer attribute`);
        }
      } else if (!Number.isInteger(cond.value)) {
        throw new PolicyError(`integer condition on "${cond.attribute}" requires an integer literal`);
      }
      return;
    }
  }
}

export function compilePolicy(spec: PolicySpec): CompiledPolicy {
  const types = buildAttributeTypes(spec);
  if (spec.publicFields.length === 0 || new Set(spec.publicFields).size !== spec.publicFields.length) {
    throw new PolicyError("publicFields must be a non-empty set of unique field names");
  }
  const publicFields = Object.freeze([...spec.publicFields]);

  validateCondition(types, spec.rowVisibleWhen);

  const ruleIds = new Set<string>();
  const maskableFields = new Set<string>();
  for (const rule of spec.maskRules) {
    if (ruleIds.has(rule.id)) {
      throw new PolicyError(`duplicate mask rule id "${rule.id}"`);
    }
    ruleIds.add(rule.id);
    validateCondition(types, rule.when);
    if (rule.fields.length === 0) {
      throw new PolicyError(`mask rule "${rule.id}" must mask at least one field`);
    }
    for (const field of rule.fields) {
      if (!publicFields.includes(field)) {
        throw new PolicyError(`mask rule "${rule.id}" references field "${field}" which is not in the public schema`);
      }
      maskableFields.add(field);
    }
  }

  return Object.freeze({
    spec,
    attributeTypes: types,
    maskableFields,
    publicFields,
  });
}
