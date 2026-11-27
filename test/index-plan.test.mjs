import assert from "node:assert/strict";
import test from "node:test";

import {
  evaluateJsonPath,
  extractIndexPlan,
  extractIndexTerms,
  indexPlanMatchesDocument,
  jsonEqual,
  matchesIndexPlan,
  matchesPlan,
  toJsonbContainment,
} from "../dist/index.js";

test("strict equality plans compile to nested jsonb containment", () => {
  const plan = extractIndexPlan("strict $.a.b ? (@.c == 1)");
  assert.equal(plan.kind, "and");
  assert.deepEqual(toJsonbContainment(plan), {
    kind: "contains",
    value: { a: { b: { c: 1 } } },
  });
});

test("lax plans preserve lax unwrap segments instead of bare key paths", () => {
  const plan = extractIndexPlan("lax $.a.b ? (@.c == 1)");
  const serialized = JSON.stringify(plan);
  assert.match(serialized, /laxUnwrap/);
  assert.ok(matchesIndexPlan({ a: [[{ b: [{ c: 1 }] }]] }, plan));
  assert.ok(
    !matchesIndexPlan({ a: [[{ b: [{ c: 2 }] }]] }, plan),
  );
});

test("AND merges conjuncts and OR produces a disjunction", () => {
  const andPlan = extractIndexPlan(
    "strict $ ? (@.a == 1 && @.b == 2)",
  );
  assert.equal(andPlan.kind, "and");
  assert.ok(
    matchesIndexPlan({ a: 1, b: 2 }, andPlan),
  );

  const orPlan = extractIndexPlan(
    "strict $ ? (@.a == 1 || @.b == 2)",
  );
  assert.equal(orPlan.kind, "or");
  assert.ok(matchesIndexPlan({ b: 2 }, orPlan));
  assert.ok(!matchesIndexPlan({ b: 3 }, orPlan));
});

test("OR with an unsafe branch falls back to full scan", () => {
  const plan = extractIndexPlan(
    "strict $ ? (@.a == 1 || !(@.b == 2))",
  );
  assert.equal(plan.kind, "all");
  assert.deepEqual(toJsonbContainment(plan), { kind: "true" });
});

test("negation never contributes positive index facts", () => {
  const plan = extractIndexPlan("strict $ ? (!(@.a == 1))");
  assert.equal(plan.kind, "all");
});

test("negative literal equality remains a value pushdown", () => {
  const plan = extractIndexPlan("strict $ ? (@.a == -1)");
  assert.deepEqual(toJsonbContainment(plan), {
    kind: "contains",
    value: { a: -1 },
  });
});

test("extractIndexTerms returns top-level conjunctive terms", () => {
  const terms = extractIndexTerms("strict $.a ? (@.b == 1)");
  assert.ok(terms.length >= 1);
  assert.ok(terms.some((term) => term.test === "equals"));
  assert.ok(terms.some((term) => term.test === "exists"));
});

test("plan-first and document-first matchers agree", () => {
  const plan = extractIndexPlan("strict $.a ? (@.b == 1)");
  const document = { a: { b: 1 } };
  assert.equal(
    matchesPlan(plan, document),
    indexPlanMatchesDocument(document, plan),
  );
});

test("randomized documents have no index false negatives", () => {
  const paths = [
    "lax $.a",
    "strict $.a",
    "lax $.a.b",
    "strict $.a.b",
    "lax $.a[*]",
    "strict $.a[*]",
    "lax $[*].a",
    "strict $[*].a",
    "lax $.**.a",
    "strict $.**.a",
    "lax $.*",
    "strict $.*",
    "lax $ ? (@.a == 1)",
    "strict $ ? (@.a == 1)",
    "lax $ ? (@.a.b == 1)",
    "strict $ ? (@.a.b == 1)",
    "lax $ ? (@.a[*] == 1)",
    "strict $ ? (@.a[*] == 1)",
    "lax $ ? (@.a == 1 || @.b == 2)",
    "strict $ ? (@.a == 1 || @.b == 2)",
    "lax $ ? (@.a == 1 && @.b == 2)",
    "strict $ ? (@.a == 1 && @.b == 2)",
    "lax $ ? (!(@.a == 1))",
    "strict $ ? (!(@.a == 1))",
    "lax $ ? (exists(@.a))",
    "strict $ ? (exists(@.a))",
    "lax $ ? (@.a < 2)",
    "strict $ ? (@.a < 2)",
    "lax $ ? (@.a.type() == \"array\")",
    "strict $ ? (@.a.type() == \"array\")",
    "lax $ ? (@.a.size() == 2)",
    "strict $ ? (@.a.size() == 2)",
    "lax $ ? (@.a + 1 == 2)",
    "strict $ ? (@.a + 1 == 2)",
    "lax $[last]",
    "strict $[last]",
    "lax $[0 to 1].a",
    "strict $[0 to 1].a",
    "lax $[*] ? (@.a == 1)",
    "strict $[*] ? (@.a == 1)",
    "lax $.a ? (@.b == 1)",
    "strict $.a ? (@.b == 1)",
    "lax $.a[0]",
    "strict $.a[0]",
    "lax $ ? (@.a ? (@ > 1).b == 2)",
    "strict $ ? (@.a ? (@ > 1).b == 2)",
    "lax $ ? (@.a like_regex \"^a\" flag \"i\")",
    "strict $ ? (@.a like_regex \"^a\" flag \"i\")",
  ];

  for (const path of paths) {
    const plan = extractIndexPlan(path);
    for (let index = 0; index < 250; index += 1) {
      const document = randomJson(index * 97 + path.length);
      let values;
      try {
        values = evaluateJsonPath(path, document);
      } catch {
        continue;
      }
      if (values.length > 0) {
        assert.ok(
          matchesIndexPlan(document, plan),
          `false negative for ${path} on ${JSON.stringify(document)}`,
        );
      }
    }
  }
});

test("randomized strict containment conversion has no false negatives", () => {
  const paths = [
    "strict $.a.b ? (@.c == 1)",
    "strict $.a ? (@.b == 1)",
    "strict $ ? (@.a == 1 || @.b == 2)",
    "strict $ ? (@.a == 1 && @.b == 2)",
    "strict $.a[*] ? (@.b == 1)",
    "strict $ ? (@.a[*] == 1)",
    "strict $ ? (@.a == 1 || !(@.b == 2))",
  ];

  for (const path of paths) {
    const query = toJsonbContainment(extractIndexPlan(path));
    for (let index = 0; index < 500; index += 1) {
      const document = randomJson(index * 131 + path.length);
      let values;
      try {
        values = evaluateJsonPath(path, document);
      } catch {
        continue;
      }
      if (values.length > 0 && query.kind !== "true") {
        assert.ok(
          containmentMatches(document, query),
          `containment false negative for ${path}`,
        );
      }
    }
  }
});

function randomJson(seed) {
  let state = seed >>> 0;
  const random = () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 2 ** 32;
  };
  const pick = (values) => values[Math.floor(random() * values.length)];
  const primitive = () =>
    pick([null, true, false, 0, 1, 2, 3, "a", "b", "x", -1, 1.5]);

  const build = (depth) => {
    if (depth > 3 || random() < 0.42) {
      return primitive();
    }
    if (random() < 0.52) {
      const array = [];
      const length = Math.floor(random() * 4);
      for (let index = 0; index < length; index += 1) {
        array.push(build(depth + 1));
      }
      return array;
    }
    const object = {};
    for (const key of ["a", "b", "c", "d"]) {
      if (random() < 0.58) {
        object[key] = build(depth + 1);
      }
    }
    return object;
  };

  return build(0);
}

function containmentMatches(document, query) {
  if (query.kind === "true") {
    return true;
  }
  if (query.kind === "contains") {
    return jsonbContains(document, query.value);
  }
  if (query.kind === "and") {
    return query.children.every((child) => containmentMatches(document, child));
  }
  return query.children.some((child) => containmentMatches(document, child));
}

function jsonbContains(left, right) {
  if (
    left === null ||
    right === null ||
    typeof left !== "object" ||
    typeof right !== "object"
  ) {
    return jsonEqual(left, right);
  }
  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right)) {
      return false;
    }
    return right.every((rightValue) =>
      left.some((leftValue) => jsonbContains(leftValue, rightValue)),
    );
  }
  for (const [key, rightValue] of Object.entries(right)) {
    if (
      !Object.prototype.hasOwnProperty.call(left, key) ||
      !jsonbContains(left[key], rightValue)
    ) {
      return false;
    }
  }
  return true;
}
