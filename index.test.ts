import { strict as assert } from "node:assert";
import { test } from "node:test";

import {
  DEFAULT_DENY_PREDICATE_ID,
  UnsafePredicateOrderError,
  checkPredicateOrder,
  isLeakproofFunction,
  isLeakproofPredicate,
  mergePolicies,
  orderPredicates,
  planPredicateOrder,
  validatePredicateOrder,
} from "./index.ts";

const permissivePolicy = {
  id: "tenant_policy",
  kind: "policy",
  policyMode: "permissive",
  expression: "tenant_id = current_setting('app.tenant')::int",
};

test("leaky user predicates run after the policy gate", () => {
  const leaky = {
    id: "leaky_user_filter",
    kind: "user",
    expression: "leaky_probe(value)",
    function: { name: "leaky_probe", leakproof: false },
  };
  const builtin = {
    id: "builtin_comparison",
    kind: "user",
    expression: "value > 10",
    function: { name: ">", leakproof: true },
  };

  const order = orderPredicates([leaky, builtin, permissivePolicy]);
  assert.deepEqual(
    order.map((predicate) => predicate.id),
    ["builtin_comparison", "tenant_policy", "leaky_user_filter"],
  );
  assert.equal(order.policyExpression, permissivePolicy.expression);
  assert.equal(order.defaultDeny, false);
  assert.equal(order.safe, true);
});

test("built-in comparisons are leakproof and can move ahead of the gate", () => {
  const builtin = {
    id: "builtin_gt",
    kind: "user",
    expression: "hidden_value > 10",
  };
  const custom = {
    id: "custom_gt",
    kind: "user",
    expression: "custom_gt(hidden_value)",
    function: { name: "custom_gt", leakproof: false },
  };

  assert.equal(isLeakproofPredicate(builtin), true);
  assert.equal(isLeakproofPredicate(custom), false);
  assert.equal(
    isLeakproofPredicate({
      kind: "user",
      operator: "=",
      expression: "leaky_probe(value) = 1",
    }),
    false,
  );
  assert.equal(isLeakproofFunction({ name: "=" }), true);
  assert.equal(isLeakproofFunction({ name: "audit(value)" }), false);

  const order = orderPredicates({
    predicates: [custom, builtin],
    policies: [permissivePolicy],
  });
  assert.deepEqual(
    order.map((predicate) => predicate.id),
    ["builtin_gt", "tenant_policy", "custom_gt"],
  );
});

test("permissive policies OR together and restrictive policies AND into the gate", () => {
  const policies = [
    {
      id: "p1",
      kind: "policy",
      policyMode: "permissive",
      expression: "owner = current_user",
    },
    {
      id: "p2",
      kind: "policy",
      policyMode: "permissive",
      expression: "is_public",
    },
    {
      id: "r1",
      kind: "policy",
      policyMode: "restrictive",
      expression: "deleted_at IS NULL",
    },
    {
      id: "r2",
      kind: "policy",
      policyMode: "restrictive",
      expression: "region = current_region",
    },
  ];

  const merged = mergePolicies(policies);
  assert.equal(merged.defaultDeny, false);
  assert.equal(
    merged.expression,
    "((owner = current_user OR is_public) AND deleted_at IS NULL AND region = current_region)",
  );
  assert.equal(merged.ast.type, "and");
  assert.deepEqual(
    merged.permissive.map((policy) => policy.id),
    ["p1", "p2"],
  );
  assert.deepEqual(
    merged.restrictive.map((policy) => policy.id),
    ["r1", "r2"],
  );
});

test("policy type aliases and function lists are honored", () => {
  const policies = [
    {
      id: "p1",
      type: "PERMISSIVE",
      expression: "owner = current_user",
    },
    {
      id: "r1",
      type: "RESTRICTIVE",
      expression: "deleted_at IS NULL",
    },
  ];
  const user = {
    id: "user",
    type: "user",
    expression: "safe_fn(value) AND risky_fn(value)",
    functions: [
      { name: "safe_fn", leakproof: true },
      { name: "risky_fn", leakproof: false },
    ],
  };

  const merged = mergePolicies(policies);
  assert.equal(
    merged.expression,
    "(owner = current_user AND deleted_at IS NULL)",
  );
  assert.deepEqual(
    orderPredicates({ policies, userPredicates: [user] }).map(
      (predicate) => predicate.id,
    ),
    ["p1", "r1", "user"],
  );

  assert.equal(
    mergePolicies([
      {
        id: "p2",
        type: "PERMISSIVE",
        expression: "a = 1",
      },
      {
        id: "r2",
        type: "RESTRICTIVE",
        expression: "x = 1 OR y = 2",
      },
    ]).expression,
    "(a = 1 AND (x = 1 OR y = 2))",
  );
});

test("restrictive-only policies default to deny and insert a false gate", () => {
  const restrictive = {
    id: "r1",
    kind: "policy",
    policyMode: "restrictive",
    expression: "deleted_at IS NULL",
  };
  const leaky = {
    id: "leaky",
    kind: "user",
    expression: "leaky(value)",
    function: { name: "leaky", leakproof: false },
  };

  const plan = planPredicateOrder({
    policies: [restrictive],
    userPredicates: [leaky],
  });
  assert.equal(plan.defaultDeny, true);
  assert.equal(plan.policyExpression, "false");
  assert.deepEqual(
    plan.order.map((predicate) => predicate.id),
    [DEFAULT_DENY_PREDICATE_ID, "leaky"],
  );
  const validation = checkPredicateOrder(plan.order);
  assert.equal(validation.valid, true);
  assert.equal(validation.defaultDeny, true);
});

test("no policies means default deny, including for user predicates", () => {
  const leaky = {
    id: "leaky",
    kind: "user",
    expression: "leaky(value)",
    leakproof: false,
  };
  const safe = {
    id: "safe",
    kind: "user",
    expression: "value = 1",
  };

  const plan = planPredicateOrder({ predicates: [leaky, safe] });
  assert.equal(plan.defaultDeny, true);
  assert.equal(plan.policyExpression, "false");
  assert.deepEqual(
    plan.order.map((predicate) => predicate.id),
    ["safe", DEFAULT_DENY_PREDICATE_ID, "leaky"],
  );
});

test("unsafe existing plans are rejected", () => {
  const leaky = {
    id: "leaky",
    kind: "user",
    expression: "leaky(value)",
    function: { name: "leaky", leakproof: false },
  };
  const restrictive = {
    id: "r1",
    kind: "policy",
    policyMode: "restrictive",
    expression: "deleted_at IS NULL",
  };

  assert.throws(
    () => validatePredicateOrder([leaky, permissivePolicy]),
    UnsafePredicateOrderError,
  );
  assert.throws(
    () => validatePredicateOrder([leaky, restrictive]),
    UnsafePredicateOrderError,
  );
  assert.throws(
    () => validatePredicateOrder([leaky]),
    UnsafePredicateOrderError,
  );
  assert.equal(validatePredicateOrder([permissivePolicy, leaky]), true);
  assert.equal(
    validatePredicateOrder([
      {
        id: "safe",
        kind: "user",
        expression: "value = 1",
      },
      permissivePolicy,
      leaky,
    ]),
    true,
  );
});

test("plan input can reject an unsafe proposed order", () => {
  const leaky = {
    id: "leaky",
    kind: "user",
    expression: "leaky(value)",
    function: { name: "leaky", leakproof: false },
  };

  assert.throws(
    () =>
      planPredicateOrder({
        policies: [permissivePolicy],
        userPredicates: [leaky],
        proposedOrder: [leaky, permissivePolicy],
      }),
    UnsafePredicateOrderError,
  );
});
