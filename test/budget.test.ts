import test from "node:test";
import assert from "node:assert/strict";

import {
  BudgetRejectedError,
  assertWithinBudget,
  estimateBudget,
  parseGraphQL,
} from "../src/index.ts";

test("deep field chains increase cost and report the exact offending path", () => {
  const query = `
    query DeepLink {
      a {
        b {
          c {
            d {
              e
            }
          }
        }
      }
    }
  `;

  const rejected = estimateBudget(query, { maxCost: 10 });
  assert.equal(rejected.cost, 15);
  assert.equal(rejected.ok, false);
  assert.equal(rejected.path, "a.b.c.d.e");
  assert.equal(rejected.overBudgetPath, "a.b.c.d.e");
  assert.deepEqual(rejected.violations[0]?.path, [
    "a",
    "b",
    "c",
    "d",
    "e",
  ]);
  assert.equal(rejected.violations[0]?.code, "BUDGET_EXCEEDED");
  assert.deepEqual(
    rejected.breakdown.map((node) => node.depth),
    [1, 2, 3, 4, 5],
  );

  const allowed = estimateBudget(query, { maxCost: 15 });
  assert.equal(allowed.ok, true);
  assert.equal(allowed.path, null);
});

test("nested list multipliers apply only to their descendants", () => {
  const query = `
    query NestedLists {
      users(first: 10) {
        nodes {
          id
          posts(first: 5) {
            nodes {
              title
            }
          }
        }
      }
    }
  `;

  const result = estimateBudget(query);
  assert.equal(result.cost, 531);
  assert.deepEqual(
    result.breakdown.map((node) => [
      node.path,
      node.depth,
      node.multiplier,
      node.cost,
    ]),
    [
      ["users", 1, 1, 1],
      ["users.nodes", 2, 10, 20],
      ["users.nodes.id", 3, 10, 30],
      ["users.nodes.posts", 3, 10, 30],
      ["users.nodes.posts.nodes", 4, 50, 200],
      ["users.nodes.posts.nodes.title", 5, 50, 250],
    ],
  );

  const rejected = estimateBudget(query, { maxCost: 530 });
  assert.equal(rejected.ok, false);
  assert.equal(rejected.path, "users.nodes.posts.nodes.title");
});

test("fragment spreads merge within a selection set and are not double charged", () => {
  const directReuse = `
    query Reuse {
      viewer {
        ...UserFields
        ...UserFields
      }
    }
    fragment UserFields on User {
      id
      friend {
        name
      }
    }
  `;

  const direct = estimateBudget(directReuse);
  assert.equal(direct.cost, 8);
  assert.deepEqual(direct.fragmentExpansions, ["UserFields"]);
  assert.equal(direct.fragmentExpansionCount, 1);

  const transitiveReuse = `
    query Transitive {
      viewer {
        ...A
        ...B
      }
    }
    fragment A on User {
      ...UserFields
    }
    fragment B on User {
      ...UserFields
      email
    }
    fragment UserFields on User {
      id
      friend {
        name
      }
    }
  `;

  const transitive = estimateBudget(transitiveReuse);
  assert.equal(transitive.cost, 10);
  assert.deepEqual(transitive.fragmentExpansions, [
    "A",
    "UserFields",
    "B",
  ]);
  assert.equal(
    transitive.fragmentExpansions.filter((name) => name === "UserFields")
      .length,
    1,
  );
});

test("the same fragment is charged separately for distinct response branches", () => {
  const query = `
    query Branches {
      left: viewer {
        ...UserFields
      }
      right: viewer {
        ...UserFields
      }
    }
    fragment UserFields on User {
      id
      friend {
        name
      }
    }
  `;

  const result = estimateBudget(query);
  assert.equal(result.cost, 16);
  assert.deepEqual(result.fragmentExpansions, ["UserFields", "UserFields"]);
  assert.deepEqual(
    result.breakdown.map((node) => node.path),
    [
      "left",
      "left.id",
      "left.friend",
      "left.friend.name",
      "right",
      "right.id",
      "right.friend",
      "right.friend.name",
    ],
  );
});

test("fragment cycles and unknown fragments are rejected without recursing forever", () => {
  const cycle = estimateBudget(`
    query Cyclic {
      viewer {
        ...A
      }
    }
    fragment A on User {
      id
      ...B
    }
    fragment B on User {
      ...A
    }
  `);
  assert.equal(cycle.ok, false);
  assert.equal(cycle.violations[0]?.code, "FRAGMENT_CYCLE");
  assert.equal(cycle.path, "viewer");

  const unknown = estimateBudget(`
    query Missing {
      viewer {
        ...DoesNotExist
      }
    }
  `);
  assert.equal(unknown.ok, false);
  assert.equal(unknown.violations[0]?.code, "UNKNOWN_FRAGMENT");
  assert.equal(unknown.path, "viewer");
});

test("pagination variables and directive variables propagate into the estimate", () => {
  const query = `
    query VariableDriven($pageSize: Int = 5, $withProfile: Boolean!) {
      users(first: $pageSize) {
        id
        profile @include(if: $withProfile) {
          name
        }
      }
    }
  `;

  const defaults = estimateBudget(query, { variables: { withProfile: false } });
  assert.equal(defaults.cost, 11);

  const expanded = estimateBudget(query, {
    variables: { pageSize: 20, withProfile: true },
  });
  assert.equal(expanded.cost, 141);

  const missing = estimateBudget(`
    query MissingPage($pageSize: Int!) {
      users(first: $pageSize) {
        id
      }
    }
  `);
  assert.equal(missing.ok, false);
  assert.equal(missing.violations[0]?.code, "UNRESOLVED_VARIABLE");
  assert.equal(missing.path, "users");
});

test("depth and page-size limits point at the path that broke the limit", () => {
  const deep = estimateBudget(
    "query { a { b { c { d } } } }",
    { maxDepth: 3 },
  );
  assert.equal(deep.violations[0]?.code, "DEPTH_EXCEEDED");
  assert.equal(deep.path, "a.b.c.d");

  const page = estimateBudget(
    "query { users(first: 50) { nodes { id } } }",
    { maxPageSize: 10 },
  );
  assert.equal(page.violations[0]?.code, "PAGE_SIZE_EXCEEDED");
  assert.equal(page.path, "users");
  assert.equal(page.violations[0]?.actual, 50);
});

test("cursor arguments do not reduce declared cost or amortize an over-budget query", () => {
  const query = `
    query CursorPage($after: String, $pageSize: Int!) {
      users(first: $pageSize, after: $after) {
        nodes {
          id
          posts(first: 5) {
            nodes {
              title
            }
          }
        }
      }
    }
  `;

  const firstPage = estimateBudget(query, {
    variables: { pageSize: 10, after: "cursor-a" },
  });
  const laterPage = estimateBudget(query, {
    variables: { pageSize: 10, after: "cursor-z" },
    cursor: "cursor-z",
  });

  assert.equal(firstPage.cost, 531);
  assert.equal(laterPage.cost, firstPage.cost);
  assert.deepEqual(firstPage.cursor.ignoredArguments, ["after"]);
  assert.equal(firstPage.cursor.independent, true);

  const overBudget = estimateBudget(query, {
    maxCost: 530,
    variables: { pageSize: 10, after: "cursor-z" },
  });
  assert.equal(overBudget.ok, false);
  assert.equal(overBudget.violations[0]?.code, "BUDGET_EXCEEDED");
  assert.equal(
    overBudget.path,
    "users.nodes.posts.nodes.title",
  );

  const attemptedFallback = estimateBudget(query, {
    maxCost: 530,
    variables: { pageSize: 10, after: "cursor-z" },
    cursorFallback: { requests: 3 },
  });
  assert.equal(attemptedFallback.ok, false);
  assert.equal(
    attemptedFallback.violations[0]?.code,
    "CURSOR_FALLBACK",
  );
  assert.equal(attemptedFallback.cursor.fallbackRejected, true);
});

test("assertWithinBudget throws a structured rejection", () => {
  assert.throws(
    () =>
      assertWithinBudget("query { a { b { c } } }", {
        maxCost: 3,
      }),
    (error: unknown) => {
      assert.ok(error instanceof BudgetRejectedError);
      assert.equal(error.code, "BUDGET_EXCEEDED");
      assert.equal(error.path, "a.b.c");
      assert.equal(error.result.cost, 6);
      return true;
    },
  );
});

test("the parser accepts aliases, inline fragments, block strings, and defaults", () => {
  const document = parseGraphQL(`
    query Reader($limit: Int = 3) {
      viewer {
        ... on User {
          displayName: name
          bio(format: """multi
            line""")
        }
      }
    }
  `);

  assert.equal(document.definitions.length, 1);
  const parsed = estimateBudget(document);
  assert.equal(parsed.ok, true);
  assert.equal(parsed.cost, 5);
  assert.equal(parsed.breakdown[1]?.path, "viewer.displayName");
});
