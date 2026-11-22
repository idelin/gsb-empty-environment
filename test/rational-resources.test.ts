import test from "node:test";
import assert from "node:assert/strict";
import {
  intersectResourceScopes,
  normalizeResourceScope,
  partitionScopeByCuts,
  rat,
  ratFromJSON,
  scopeContains,
  scopeIsSubset,
  subtractResourceInterval,
  subtractResourceScopes,
} from "../index.ts";

test("rational arithmetic is exact", () => {
  assert.equal(rat("1/3").add("2/3").toString(), "1");
  assert.equal(rat("0.1").toString(), "1/10");
  assert.equal(rat("1e-3").toString(), "1/1000");
  assert.equal(ratFromJSON(rat("7/9").toJSON()).toString(), "7/9");
  assert.equal(rat(1).div(3).mul(3).toString(), "1");
});

test("prefix difference and nested cuts", () => {
  assert.deepEqual(
    subtractResourceInterval({ prefix: "a" }, { prefix: "a/x" }),
    [{ prefix: "a", excludes: ["a/x"] }],
  );
  assert.deepEqual(
    normalizeResourceScope([
      { prefix: "a/x" },
      { prefix: "a", excludes: ["a/x"] },
    ]),
    [{ prefix: "a/x" }, { prefix: "a", excludes: ["a/x"] }],
  );
  assert.deepEqual(
    partitionScopeByCuts([{ prefix: "a" }], [
      { prefix: "a" },
      { prefix: "a/x" },
    ]),
    [{ prefix: "a/x" }, { prefix: "a", excludes: ["a/x"] }],
  );
});

test("sublease intersection cannot widen a parent scope", () => {
  const parent = [{ prefix: "a/b" }];
  const broadChild = [{ prefix: "a" }];
  const effective = intersectResourceScopes(parent, broadChild);
  assert.deepEqual(effective, [{ prefix: "a/b" }]);
  assert.equal(scopeContains(effective, "a/b/z"), true);
  assert.equal(scopeContains(effective, "a/x"), false);
  assert.equal(scopeIsSubset(effective, parent), true);
});

test("prefix subtraction is a true set difference", () => {
  const scope = [{ prefix: "root" }];
  const holes = [
    { prefix: "root/private" },
    { prefix: "root/cache" },
  ];
  const remaining = subtractResourceScopes(scope, holes);
  assert.equal(scopeContains(remaining, "root/public"), true);
  assert.equal(scopeContains(remaining, "root/private/key"), false);
  assert.equal(scopeContains(remaining, "root/cache/x"), false);
});
