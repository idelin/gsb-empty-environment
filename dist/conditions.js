"use strict";
Object.defineProperty(exports, "__esModule", { value: true });

function hasOwn(obj, key) {
  return Object.prototype.hasOwnProperty.call(obj, key);
}

function matchesCondition(condition, attributes) {
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

function matchesAll(conditions, attributes) {
  return conditions.every((c) => matchesCondition(c, attributes));
}

module.exports = { hasOwn, matchesCondition, matchesAll };
