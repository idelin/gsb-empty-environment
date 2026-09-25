import type { Condition, Primitive } from "./types.ts";

export const inSet = (attr: string, values: readonly Primitive[]): Condition => ({
  kind: "in",
  attr,
  values: [...values],
});

export const intCmp = (
  attr: string,
  op: "=" | "!=" | "<" | "<=" | ">" | ">=",
  value: number,
): Condition => ({ kind: "int", attr, op, value });

export const and = (...of: readonly Condition[]): Condition => ({
  kind: "and",
  of,
});

export const or = (...of: readonly Condition[]): Condition => ({
  kind: "or",
  of,
});

export const not = (of: Condition): Condition => ({ kind: "not", of });
