import { InvalidPolicyError, PolicyNotSatisfiedError } from "./errors.ts";
import type {
  ClaimRule,
  JsonObject,
  JsonValue,
  PolicyEvaluation,
  PolicyNode,
  TraceEntry,
} from "./types.ts";

const RULE_OPERATORS = [
  "equals",
  "notEquals",
  "in",
  "notIn",
  "exists",
  "gt",
  "gte",
  "lt",
  "lte",
] as const;

const COMPARISON_OPS = ["gt", "gte", "lt", "lte"] as const;
type ComparisonOp = (typeof COMPARISON_OPS)[number];

export function isClaimRule(node: PolicyNode): node is ClaimRule {
  return typeof (node as ClaimRule).claim === "string";
}

/**
 * Reject malformed policies up front (at policy load time) instead of silently
 * treating them as unmatched at evaluation time.
 */
export function validatePolicy(
  node: unknown,
  path: ReadonlyArray<string | number> = [],
): asserts node is PolicyNode {
  if (node === null || typeof node !== "object") {
    throw new InvalidPolicyError("policy node must be an object", path);
  }
  const record = node as Record<string, unknown>;
  const keys = Object.keys(record);

  if (keys.includes("allOf") || keys.includes("anyOf")) {
    if (keys.length !== 1) {
      throw new InvalidPolicyError(
        "allOf/anyOf node cannot carry extra fields",
        path,
      );
    }
    const branch = keys.includes("allOf") ? "allOf" : "anyOf";
    const children = record[branch];
    if (!Array.isArray(children) || children.length === 0) {
      throw new InvalidPolicyError(`${branch} must be a non-empty array`, path);
    }
    children.forEach((child, index) =>
      validatePolicy(child, [...path, branch, index]),
    );
    return;
  }

  if (typeof record.claim !== "string" || record.claim.length === 0) {
    throw new InvalidPolicyError("claim rule needs a non-empty claim path", path);
  }
  const unknownKeys = keys.filter(
    (key) =>
      key !== "claim" &&
      !(RULE_OPERATORS as ReadonlyArray<string>).includes(key),
  );
  if (unknownKeys.length > 0) {
    throw new InvalidPolicyError(
      `unknown operator(s) in claim rule: ${unknownKeys.join(", ")}`,
      path,
    );
  }
  const operatorKeys = keys.filter((key) =>
    (RULE_OPERATORS as ReadonlyArray<string>).includes(key),
  );
  if (operatorKeys.length === 0) {
    throw new InvalidPolicyError(
      `claim rule for "${record.claim}" has no operator`,
      path,
    );
  }
  for (const key of operatorKeys) {
    if (key === "equals" || key === "notEquals") {
      assertJsonValue(record[key], [...path, key]);
    } else if (key === "in" || key === "notIn") {
      if (!Array.isArray(record[key]) || record[key].length === 0) {
        throw new InvalidPolicyError(`${key} must be a non-empty array`, [
          ...path,
          key,
        ]);
      }
      (record[key] as unknown[]).forEach((item, index) =>
        assertJsonValue(item, [...path, key, index]),
      );
    } else if (key === "exists") {
      if (typeof record[key] !== "boolean") {
        throw new InvalidPolicyError("exists must be a boolean", [
          ...path,
          key,
        ]);
      }
    } else {
      if (
        typeof record[key] !== "number" ||
        !Number.isFinite(record[key] as number)
      ) {
        throw new InvalidPolicyError(`${key} must be a finite number`, [
          ...path,
          key,
        ]);
      }
    }
  }
}

function assertJsonValue(
  value: unknown,
  path: ReadonlyArray<string | number>,
): asserts value is JsonValue {
  if (
    value === null ||
    value === undefined ||
    typeof value === "string" ||
    typeof value === "number" ||
    typeof value === "boolean"
  ) {
    if (value === undefined) {
      throw new InvalidPolicyError("undefined is not a JSON value", path);
    }
    return;
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) => assertJsonValue(item, [...path, index]));
    return;
  }
  if (typeof value === "object") {
    for (const [key, nested] of Object.entries(value as Record<string, unknown>)) {
      assertJsonValue(nested, [...path, key]);
    }
    return;
  }
  throw new InvalidPolicyError("value is not JSON-representable", path);
}

/**
 * Resolve a dot-separated claim path. Numeric segments index arrays. A missing
 * segment (including lookup inside a non-container value) returns found=false;
 * an explicit JSON null still counts as found.
 */
export function resolveClaim(
  claims: JsonObject,
  path: string,
): { found: boolean; value: JsonValue | undefined } {
  let current: JsonValue = claims;
  for (const segment of path.split(".")) {
    if (current === null || typeof current !== "object") {
      return { found: false, value: undefined };
    }
    if (Array.isArray(current)) {
      const index = Number(segment);
      if (!Number.isInteger(index) || index < 0 || index >= current.length) {
        return { found: false, value: undefined };
      }
      current = current[index];
    } else {
      if (!Object.prototype.hasOwnProperty.call(current, segment)) {
        return { found: false, value: undefined };
      }
      current = (current as JsonObject)[segment];
    }
  }
  return { found: true, value: current };
}

/** Strict deep equality: different JSON types never compare equal. */
export function strictJsonEqual(actual: JsonValue, expected: JsonValue): boolean {
  if (typeof actual !== typeof expected) {
    return false;
  }
  if (actual === null || expected === null) {
    return actual === expected;
  }
  if (Array.isArray(actual) || Array.isArray(expected)) {
    return (
      Array.isArray(actual) &&
      Array.isArray(expected) &&
      actual.length === expected.length &&
      actual.every((item, index) => strictJsonEqual(item, expected[index]))
    );
  }
  if (typeof actual === "object" && typeof expected === "object") {
    const actualKeys = Object.keys(actual).sort();
    const expectedKeys = Object.keys(expected).sort();
    return (
      actualKeys.length === expectedKeys.length &&
      actualKeys.every((key, index) => key === expectedKeys[index]) &&
      actualKeys.every((key) =>
        strictJsonEqual(
          (actual as JsonObject)[key],
          (expected as JsonObject)[key],
        ),
      )
    );
  }
  return actual === expected;
}

function compare(
  op: ComparisonOp,
  actual: JsonValue,
  bound: number,
): boolean {
  if (typeof actual !== "number" || Number.isNaN(actual)) {
    return false;
  }
  switch (op) {
    case "gt":
      return actual > bound;
    case "gte":
      return actual >= bound;
    case "lt":
      return actual < bound;
    case "lte":
      return actual <= bound;
  }
}

function evaluateRule(rule: ClaimRule, claims: JsonObject): TraceEntry {
  const { found, value } = resolveClaim(claims, rule.claim);
  const failures: string[] = [];

  if (rule.exists === false) {
    if (found) failures.push(`claim "${rule.claim}" must be absent`);
  } else if (!found) {
    failures.push(`claim "${rule.claim}" is missing`);
  }

  if (found && value !== undefined && rule.equals !== undefined) {
    if (!strictJsonEqual(value, rule.equals)) {
      failures.push(
        `claim "${rule.claim}" ${JSON.stringify(value)} (${jsonType(value)}) !== ${JSON.stringify(rule.equals)} (${jsonType(rule.equals)})`,
      );
    }
  }

  if (found && rule.notEquals !== undefined) {
    if (strictJsonEqual(value as JsonValue, rule.notEquals)) {
      failures.push(
        `claim "${rule.claim}" must not equal ${JSON.stringify(rule.notEquals)}`,
      );
    }
  }

  if (found && rule.in) {
    if (!membershipMatch(value as JsonValue, rule.in)) {
      failures.push(
        `claim "${rule.claim}" ${JSON.stringify(value)} has no element in the allowed list`,
      );
    }
  }

  if (found && rule.notIn) {
    if (membershipMatch(value as JsonValue, rule.notIn)) {
      failures.push(
        `claim "${rule.claim}" ${JSON.stringify(value)} matches the denied list`,
      );
    }
  }

  for (const op of COMPARISON_OPS) {
    const bound = rule[op];
    if (bound !== undefined) {
      if (!found || !compare(op, value as JsonValue, bound)) {
        failures.push(
          `claim "${rule.claim}" must be ${op} ${bound}${found ? "" : " (claim missing)"}`,
        );
      }
    }
  }

  return {
    kind: "rule",
    rule,
    satisfied: failures.length === 0,
    reason: failures.length === 0 ? "matched" : failures.join("; "),
  };
}

/**
 * `in` / `notIn` semantics:
 *  - scalar claim: strict equality against any list entry
 *    (still strictly typed: "false" never matches false);
 *  - array claim: satisfied when at least one element matches a list entry.
 */
function membershipMatch(
  value: JsonValue,
  list: ReadonlyArray<JsonValue>,
): boolean {
  if (Array.isArray(value)) {
    return value.some((element) =>
      list.some((candidate) => strictJsonEqual(element, candidate)),
    );
  }
  return list.some((candidate) => strictJsonEqual(value, candidate));
}

function evaluateNode(node: PolicyNode, claims: JsonObject): TraceEntry {
  if (isClaimRule(node)) {
    return evaluateRule(node, claims);
  }
  if ("allOf" in node) {
    const children = node.allOf.map((child) => evaluateNode(child, claims));
    const failed = children.filter((child) => !child.satisfied);
    return {
      kind: "allOf",
      satisfied: failed.length === 0,
      reason:
        failed.length === 0
          ? "all conditions matched"
          : `${failed.length} of ${children.length} conditions failed`,
      children,
    };
  }
  const children = node.anyOf.map((child) => evaluateNode(child, claims));
  const matched = children.some((child) => child.satisfied);
  return {
    kind: "anyOf",
    satisfied: matched,
    reason: matched
      ? "at least one condition matched"
      : "none of the conditions matched",
    children,
  };
}

/** Evaluate a validated policy against verified token claims. */
export function evaluatePolicy(
  policy: PolicyNode,
  claims: JsonObject,
): PolicyEvaluation {
  validatePolicy(policy);
  const trace = [evaluateNode(policy, claims)];
  return {
    satisfied: trace[0].satisfied,
    trace,
  };
}

/** Convenience: enforce that a numeric TCB/version claim is at least `minimum`. */
export function requireMinimumTcb(
  claims: JsonObject,
  tcbClaim: string,
  minimum: number,
): PolicyEvaluation {
  return evaluatePolicy({ claim: tcbClaim, gte: minimum, exists: true }, claims);
}

/** Throw with a readable trace when a policy does not match. */
export function assertPolicySatisfied(
  policy: PolicyNode,
  claims: JsonObject,
): void {
  const result = evaluatePolicy(policy, claims);
  if (!result.satisfied) {
    throw new PolicyNotSatisfiedError(
      `attestation policy not satisfied: ${summarize(result.trace)}`,
      result.trace,
    );
  }
}

function summarize(trace: ReadonlyArray<TraceEntry>): string {
  return trace
    .flatMap(function walk(entry): string[] {
      if (entry.kind === "rule") {
        return entry.satisfied ? [] : [entry.reason];
      }
      return entry.satisfied ? [] : entry.children.flatMap(walk);
    })
    .join("; ");
}

function jsonType(value: JsonValue): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}
