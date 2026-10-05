import { test } from "node:test";
import assert from "node:assert/strict";
import { checkApprovals } from "../src/checker.ts";
import { solveMinimumSet, type Constraint } from "../src/solver.ts";
import type { CheckerInput } from "../src/types.ts";

const base: CheckerInput = {
  author: "alice",
  files: [{ path: "src/a.ts" }],
  rules: [{ pattern: "src/**", owners: ["bob", "carol"] }],
};

test("existing approvals satisfy the threshold, nothing to add", () => {
  const out = checkApprovals({ ...base, approvals: ["bob"] });
  assert.equal(out.ok, true);
  assert.deepEqual(out.added, []);
});

test("author approval never counts", () => {
  const out = checkApprovals({
    ...base,
    approvals: ["alice"],
    candidates: ["bob"],
  });
  assert.equal(out.ok, true);
  assert.deepEqual(out.added, ["bob"]);
});

test("minimum set prefers one candidate covering several files", () => {
  const out = checkApprovals({
    author: "alice",
    files: [{ path: "src/a.ts" }, { path: "lib/b.ts" }],
    rules: [
      { pattern: "src/**", owners: ["bob", "dave"] },
      { pattern: "lib/**", owners: ["carol", "dave"] },
    ],
    candidates: ["bob", "carol", "dave"],
  });
  assert.equal(out.ok, true);
  assert.deepEqual(out.added, ["dave"]);
});

test("per-file thresholds are enforced independently", () => {
  const out = checkApprovals({
    author: "alice",
    files: [{ path: "src/a.ts" }, { path: "src/core/b.ts" }],
    rules: [
      { pattern: "src/**", owners: ["bob"] },
      { pattern: "src/core/**", owners: ["carol", "dave"], threshold: 2 },
    ],
    candidates: ["bob", "carol", "dave"],
  });
  assert.equal(out.ok, true);
  assert.deepEqual(new Set(out.added), new Set(["bob", "carol", "dave"]));
  const core = out.files.find((f) => f.path === "src/core/b.ts")!;
  assert.equal(core.requirements[0].approvals, 2);
});

test("rename requires approvals for both old and new path", () => {
  const out = checkApprovals({
    author: "alice",
    files: [{ path: "new/x.ts", oldPath: "old/x.ts" }],
    rules: [
      { pattern: "old/**", owners: ["bob"] },
      { pattern: "new/**", owners: ["carol"] },
    ],
    candidates: ["bob", "carol"],
  });
  assert.equal(out.ok, true);
  assert.deepEqual(new Set(out.added), new Set(["bob", "carol"]));
  assert.equal(out.files[0].requirements.length, 2);
});

test("rename with a single candidate owning both paths needs one addition", () => {
  const out = checkApprovals({
    author: "alice",
    files: [{ path: "new/x.ts", oldPath: "old/x.ts" }],
    rules: [{ pattern: "**", owners: ["bob"] }],
    candidates: ["bob"],
  });
  assert.equal(out.ok, true);
  assert.deepEqual(out.added, ["bob"]);
});

test("blocking files are reported when no candidate set suffices", () => {
  const out = checkApprovals({
    author: "alice",
    files: [{ path: "src/a.ts" }, { path: "src/core/b.ts" }],
    rules: [
      { pattern: "src/**", owners: ["bob"] },
      { pattern: "src/core/**", owners: ["carol", "dave"], threshold: 2 },
    ],
    candidates: ["bob", "carol"],
  });
  assert.equal(out.ok, false);
  assert.deepEqual(out.added, []);
  assert.equal(out.blocking.length, 1);
  assert.equal(out.blocking[0].path, "src/core/b.ts");
  assert.equal(out.blocking[0].threshold, 2);
  assert.equal(out.blocking[0].maxApprovals, 1);
});

test("rename blocking reports the failing old path", () => {
  const out = checkApprovals({
    author: "alice",
    files: [{ path: "new/x.ts", oldPath: "old/x.ts" }],
    rules: [
      { pattern: "old/**", owners: ["bob", "carol"], threshold: 2 },
      { pattern: "new/**", owners: ["dave"] },
    ],
    candidates: ["bob", "dave"],
  });
  assert.equal(out.ok, false);
  assert.equal(out.blocking[0].failedPath, "old/x.ts");
});

test("more than twenty candidates is rejected", () => {
  const candidates = Array.from({ length: 21 }, (_, i) => `u${i}`);
  assert.throws(() => checkApprovals({ ...base, candidates }), /at most 20/);
});

test("files without a matching rule impose no requirement", () => {
  const out = checkApprovals({
    author: "alice",
    files: [{ path: "docs/readme.md" }],
    rules: [{ pattern: "src/**", owners: ["bob"] }],
  });
  assert.equal(out.ok, true);
  assert.deepEqual(out.added, []);
  assert.deepEqual(out.files[0].requirements, []);
});

test("solver finds the minimum subset", () => {
  const candidates = ["a", "b", "c", "d"];
  const constraint = (
    ownerCandidates: number[],
    threshold: number,
  ): Constraint => ({
    fileIndex: 0,
    requirement: { path: "p", owners: [], threshold },
    base: 0,
    ownerCandidates,
  });
  // {a} covers c1, {b} covers c2, {c} covers both: minimum is [c].
  const chosen = solveMinimumSet(candidates, [
    constraint([0, 2], 1),
    constraint([1, 2], 1),
  ]);
  assert.deepEqual(chosen, [2]);
  // Threshold 2 with disjoint owners forces two additions.
  const two = solveMinimumSet(candidates, [constraint([0, 1], 2)]);
  assert.equal(two!.length, 2);
  // Infeasible constraint returns null.
  assert.equal(solveMinimumSet(candidates, [constraint([0], 2)]), null);
});
