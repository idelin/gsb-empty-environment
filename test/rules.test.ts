import { test } from "node:test";
import assert from "node:assert/strict";
import { requirementForPath, requirementsForFile } from "../src/rules.ts";
import type { Rule } from "../src/types.ts";

test("override replaces owners, later rule wins", () => {
  const rules: Rule[] = [
    { pattern: "src/**", owners: ["a", "b"] },
    { pattern: "src/core/**", owners: ["c"] },
  ];
  assert.deepEqual(requirementForPath("src/core/x.ts", rules, "me"), {
    path: "src/core/x.ts",
    owners: ["c"],
    threshold: 1,
  });
  assert.deepEqual(requirementForPath("src/other/x.ts", rules, "me")!.owners, [
    "a",
    "b",
  ]);
});

test("append unions owners into the accumulated set", () => {
  const rules: Rule[] = [
    { pattern: "src/**", owners: ["a"] },
    { pattern: "src/core/**", owners: ["b"], mode: "append" },
  ];
  const req = requirementForPath("src/core/x.ts", rules, "me")!;
  assert.deepEqual(new Set(req.owners), new Set(["a", "b"]));
});

test("threshold comes from the last matching rule", () => {
  const rules: Rule[] = [
    { pattern: "src/**", owners: ["a", "b"], threshold: 2 },
    { pattern: "src/core/**", owners: ["c"], mode: "append" },
  ];
  const req = requirementForPath("src/core/x.ts", rules, "me")!;
  assert.equal(req.threshold, 1);
  assert.deepEqual(new Set(req.owners), new Set(["a", "b", "c"]));
});

test("author is removed from the owner set", () => {
  const rules: Rule[] = [{ pattern: "**", owners: ["me", "you"] }];
  assert.deepEqual(requirementForPath("x.ts", rules, "me")!.owners, ["you"]);
});

test("no matching rule means no requirement", () => {
  const rules: Rule[] = [{ pattern: "src/**", owners: ["a"] }];
  assert.equal(requirementForPath("docs/readme.md", rules, "me"), null);
});

test("renamed file requires both old and new path", () => {
  const rules: Rule[] = [
    { pattern: "old/**", owners: ["old-owner"] },
    { pattern: "new/**", owners: ["new-owner"] },
  ];
  const reqs = requirementsForFile(
    { path: "new/x.ts", oldPath: "old/x.ts" },
    rules,
    "me",
  );
  assert.equal(reqs.length, 2);
  assert.deepEqual(
    reqs.map((r) => r.path).sort(),
    ["new/x.ts", "old/x.ts"],
  );
});

test("non-renamed file yields a single requirement", () => {
  const rules: Rule[] = [{ pattern: "**", owners: ["a"] }];
  const reqs = requirementsForFile({ path: "x.ts" }, rules, "me");
  assert.equal(reqs.length, 1);
});
