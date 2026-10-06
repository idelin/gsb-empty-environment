import { AttributeValue, Condition } from "./types";

export function hasOwn(obj: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(obj, key);
}

/**
 * A missing attribute is NOT the empty string: every value comparison fails
 * when the attribute is absent. Only `exists` / `notExists` look at presence.
 */
export function matchesCondition(
  condition: Condition,
  attributes: Record<string, AttributeValue>
): boolean {
  const present = hasOwn(attributes, condition.attr);
  switch (condition.op) {
    case "exists":
      return present;
    case "notExists":
      return !present;
  }
  if (!present) return false;

  const actual = attributes[condition.attr];
  const expected = condition.value;
  switch (condition.op) {
    case "eq":
      return actual === expected;
    case "neq":
      return actual !== expected;
    case "in":
      return Array.isArray(expected) && expected.some((v) => v === actual);
    case "gt":
      return typeof actual === "number" && typeof expected === "number" && actual > expected;
    case "gte":
      return typeof actual === "number" && typeof expected === "number" && actual >= expected;
    case "lt":
      return typeof actual === "number" && typeof expected === "number" && actual < expected;
    case "lte":
      return typeof actual === "number" && typeof expected === "number" && actual <= expected;
    case "contains":
      return (
        typeof actual === "string" &&
        typeof expected === "string" &&
        actual.includes(expected)
      );
    default:
      return false;
  }
}

export function matchesAll(
  conditions: ReadonlyArray<Condition>,
  attributes: Record<string, AttributeValue>
): boolean {
  return conditions.every((c) => matchesCondition(c, attributes));
}
