export type Decision = "allow" | "deny";

export type AttrValue = string | number | boolean;

export type AttrInput = Record<string, AttrValue>;

export interface EnumAttr {
  name: string;
  kind: "enum";
  values: AttrValue[];
}

export interface IntAttr {
  name: string;
  kind: "int";
  min: number;
  max: number;
}

export type AttrDef = EnumAttr | IntAttr;

export type Condition =
  | { op: "eq" | "neq"; attr: string; value: AttrValue }
  | { op: "in"; attr: string; values: AttrValue[] }
  | { op: "lt" | "le" | "gt" | "ge"; attr: string; value: number }
  | { op: "between"; attr: string; min: number; max: number }
  | { op: "and" | "or"; args: Condition[] }
  | { op: "not"; arg: Condition }
  | { op: "always" };

export interface Rule {
  id: string;
  effect: Decision;
  when: Condition;
  reason: string;
}

export type Combining = "deny-overrides" | "allow-overrides";

export interface Policy {
  name: string;
  combining: Combining;
  default: Decision;
  rules: Rule[];
}

export interface EvalResult {
  decision: Decision;
  matched: string[];
  reasons: string[];
}
