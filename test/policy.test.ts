import { test } from "node:test";
import assert from "node:assert/strict";
import {
  assertPolicySatisfied,
  evaluatePolicy,
  requireMinimumTcb,
  resolveClaim,
  strictJsonEqual,
  validatePolicy,
} from "../index.ts";
import {
  InvalidPolicyError,
  PolicyNotSatisfiedError,
} from "../src/errors.ts";
import type { JsonObject, PolicyNode } from "../src/types.ts";

const claims: JsonObject = {
  tcb: {
    version: 12,
    debug: false,
  },
  "attester.nonce": "abc",
  policy_ids: ["policy-1"],
  hardware_allowed: "true",
  instance_id: "vm-A",
  measured_boot: true,
  disclaimers: { confidential: false },
};

test("missing claim never satisfies any operator", () => {
  const result = evaluatePolicy(
    { claim: "tcb.firmware_version", gte: 5 },
    claims,
  );
  assert.equal(result.satisfied, false);
  assert.match(result.trace[0].reason, /missing/);

  const existsCheck = evaluatePolicy(
    { claim: "tcb.nonexistent", exists: true },
    claims,
  );
  assert.equal(existsCheck.satisfied, false);

  // exists:false is the one rule a missing claim does satisfy.
  const absent = evaluatePolicy(
    { claim: "tcb.nonexistent", exists: false },
    claims,
  );
  assert.equal(absent.satisfied, true);
});

test("type confusion: string \"false\" is not boolean false", () => {
  const booleanRule: PolicyNode = {
    claim: "tcb.debug",
    equals: false,
  };
  assert.equal(evaluatePolicy(booleanRule, claims).satisfied, true);

  const confusedClaims: JsonObject = { tcb: { debug: "false" } };
  const result = evaluatePolicy(booleanRule, confusedClaims);
  assert.equal(result.satisfied, false);
  assert.match(result.trace[0].reason, /string.*!==.*boolean/);

  // Numeric strings must not satisfy numeric comparisons either.
  const numericClaims: JsonObject = { tcb: { version: "12" } };
  assert.equal(
    evaluatePolicy({ claim: "tcb.version", gte: 12 }, numericClaims)
      .satisfied,
    false,
  );

  // String "true" is not boolean true.
  assert.equal(
    evaluatePolicy(
      { claim: "hardware_allowed", equals: true },
      claims,
    ).satisfied,
    false,
  );
});

test("strict JSON equality across types and structures", () => {
  assert.equal(strictJsonEqual(false, false), true);
  assert.equal(strictJsonEqual("false", false), false);
  assert.equal(strictJsonEqual(1, "1"), false);
  assert.equal(strictJsonEqual(null, undefined as never), false);
  assert.equal(
    strictJsonEqual({ a: 1, b: 2 }, { b: 2, a: 1 }),
    true,
  );
  assert.equal(strictJsonEqual([1, 2], [1, 2]), true);
  assert.equal(strictJsonEqual([1, 2], [2, 1]), false);
});

test("TCB lower bound: stale TCB is rejected, current TCB accepted", () => {
  assert.equal(requireMinimumTcb(claims, "tcb.version", 12).satisfied, true);
  assert.equal(requireMinimumTcb(claims, "tcb.version", 13).satisfied, false);

  const stale: JsonObject = { tcb: { version: 3 } };
  const result = requireMinimumTcb(stale, "tcb.version", 12);
  assert.equal(result.satisfied, false);
  assert.match(result.trace[0].reason, /gte 12/);

  // A missing TCB claim cannot pass the lower bound.
  assert.equal(
    requireMinimumTcb({}, "tcb.version", 12).satisfied,
    false,
  );
});

test("nested anyOf inside allOf: one branch may rescue the token", () => {
  const policy: PolicyNode = {
    allOf: [
      { claim: "tcb.debug", equals: false },
      {
        anyOf: [
          { claim: "instance_id", equals: "vm-UNKNOWN" },
          { claim: "policy_ids", in: ["policy-1", "policy-2"] },
          { claim: "measured_boot", equals: false },
        ],
      },
      { claim: "tcb.version", gte: 10 },
    ],
  };
  assert.equal(evaluatePolicy(policy, claims).satisfied, true);

  // When every anyOf branch fails, the outer allOf fails and the trace
  // explains each failed branch.
  const noBranch: JsonObject = {
    tcb: { debug: false, version: 12 },
    policy_ids: ["other"],
    measured_boot: true,
  };
  const failed = evaluatePolicy(policy, noBranch);
  assert.equal(failed.satisfied, false);
  const anyOfNode = failed.trace[0];
  assert.equal(anyOfNode.kind, "allOf");
  assert.equal(anyOfNode.children[1].kind, "anyOf");
  assert.equal(anyOfNode.children[1].satisfied, false);
});

test("nested anyOf with missing claim branch still passes via another branch", () => {
  const policy: PolicyNode = {
    anyOf: [
      { claim: "absent.debug_disabled", equals: true },
      { claim: "disclaimers.confidential", equals: false },
    ],
  };
  assert.equal(evaluatePolicy(policy, claims).satisfied, true);

  const bothFail = evaluatePolicy(policy, {
    disclaimers: { confidential: "false" },
  });
  assert.equal(bothFail.satisfied, false);
});

test("replay defense: token from another VM fails the identity rule", () => {
  const policy: PolicyNode = {
    allOf: [
      { claim: "instance_id", equals: "vm-A" },
      { claim: "tcb.debug", equals: false },
    ],
  };
  const replayed: JsonObject = {
    ...claims,
    instance_id: "vm-VICTIM",
  };
  assert.throws(
    () => assertPolicySatisfied(policy, replayed),
    (error: unknown) =>
      error instanceof PolicyNotSatisfiedError &&
      /instance_id/.test(error.message),
  );
  assert.doesNotThrow(() => assertPolicySatisfied(policy, claims));
});

test("debug VM token is rejected", () => {
  const policy: PolicyNode = { claim: "tcb.debug", equals: false };
  const debugClaims: JsonObject = { tcb: { debug: true } };
  assert.equal(evaluatePolicy(policy, debugClaims).satisfied, false);
});

test("in/notIn and notEquals are strictly typed", () => {
  assert.equal(
    evaluatePolicy(
      { claim: "hardware_allowed", in: [true, false] },
      claims,
    ).satisfied,
    false,
  );
  assert.equal(
    evaluatePolicy(
      { claim: "hardware_allowed", notIn: [true] },
      claims,
    ).satisfied,
    true,
  );
  assert.equal(
    evaluatePolicy(
      { claim: "hardware_allowed", notEquals: true },
      claims,
    ).satisfied,
    true,
  );
});

test("dot paths traverse objects and arrays", () => {
  assert.deepEqual(resolveClaim(claims, "tcb.debug"), {
    found: true,
    value: false,
  });
  assert.deepEqual(resolveClaim(claims, "policy_ids.0"), {
    found: true,
    value: "policy-1",
  });
  assert.equal(resolveClaim(claims, "tcb.debug.nope").found, false);
  assert.equal(resolveClaim(claims, "policy_ids.9").found, false);
  // A literal dotted key is reachable as a single segment only.
  assert.equal(resolveClaim(claims, "attester.nonce").found, false);
});

test("malformed policies are rejected at validation time", () => {
  assert.throws(
    () => validatePolicy({}),
    InvalidPolicyError,
  );
  assert.throws(
    () => validatePolicy({ allOf: [] }),
    InvalidPolicyError,
  );
  assert.throws(
    () => validatePolicy({ claim: "x", allOf: [] }),
    InvalidPolicyError,
  );
  assert.throws(
    () => validatePolicy({ claim: "x", gte: "12" }),
    InvalidPolicyError,
  );
  assert.throws(
    () => validatePolicy({ claim: "x", exists: "yes" }),
    InvalidPolicyError,
  );
  assert.throws(
    () => validatePolicy({ claim: "x", in: [] }),
    InvalidPolicyError,
  );
});
