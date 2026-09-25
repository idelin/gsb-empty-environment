import { test } from "node:test";
import assert from "node:assert/strict";
import { RuleRegistry, applyPatchOps, applyRules, deepEqual, getAt } from "../src/index.ts";
import type { Rule } from "../src/index.ts";

const baseRules: Rule[] = [
  { id: "ws", kind: "whitespace", mode: "trim", targets: [["name"]] },
  { id: "enum", kind: "enum-alias", aliases: { na: "N/A" }, targets: [["status"]] },
];

const doc = { name: "  Ada ", status: "na", note: "keep me " };

test("publishes valid rule sets with increasing versions", () => {
  const reg = new RuleRegistry();
  const p1 = reg.publish(baseRules);
  assert.ok(p1.ok);
  assert.equal(p1.version, 1);
  const p2 = reg.publish([...baseRules]);
  assert.ok(p2.ok);
  assert.equal(p2.version, 2);
});

test("rejects invalid publications and keeps the previous version's results", () => {
  const reg = new RuleRegistry();
  assert.ok(reg.publish(baseRules).ok);
  const before = reg.normalize(doc);

  const invalid: Rule[] = [
    ...baseRules,
    { id: "bad", kind: "enum-alias", aliases: { na: "Different" }, targets: [["status"]] },
  ];
  const outcome = reg.publish(invalid);
  assert.ok(!outcome.ok);
  assert.ok(outcome.issues.some((i) => i.type === "alias-conflict"));
  assert.equal(outcome.activeVersion, 1);
  assert.equal(reg.activeVersion(), 1);

  const after = reg.normalize(doc);
  assert.ok(deepEqual(before.output, after.output));
  assert.equal(after.version, 1);
});

test("rejects rule updates that would introduce a cycle", () => {
  const reg = new RuleRegistry();
  assert.ok(
    reg.publish([
      { id: "c1", kind: "copy", from: ["a"], to: ["b"], targets: [] },
    ]).ok,
  );
  const outcome = reg.updateRule("c1", {
    id: "c1",
    kind: "copy",
    from: ["b"],
    to: ["a"],
    targets: [],
  });
  // Single copy b->a alone is acyclic, so this must succeed...
  assert.ok(outcome.ok);
  // ...but adding a second copy that closes a cycle must be rejected.
  const cycle = reg.publish([
    { id: "c1", kind: "copy", from: ["b"], to: ["a"], targets: [] },
    { id: "c2", kind: "copy", from: ["a"], to: ["b"], targets: [] },
  ]);
  assert.ok(!cycle.ok);
  assert.ok(cycle.issues.some((i) => i.type === "trigger-cycle"));
  assert.equal(reg.activeVersion(), 2);
});

test("local rule update recomputes only dependent fields", () => {
  const reg = new RuleRegistry();
  assert.ok(reg.publish(baseRules).ok);
  const prev = reg.normalize(doc);
  assert.equal(getAt(prev.output, ["status"]).value, "N/A");
  assert.equal(getAt(prev.output, ["name"]).value, "Ada");

  const update = reg.updateRule("enum", {
    id: "enum",
    kind: "enum-alias",
    aliases: { na: "NOT-APPLICABLE" },
    targets: [["status"]],
  });
  assert.ok(update.ok);
  assert.deepEqual(update.affectedRuleIds, ["enum"]);

  const inc = reg.recompute(prev, update.affectedRuleIds);
  assert.equal(getAt(inc.output, ["status"]).value, "NOT-APPLICABLE");
  assert.equal(getAt(inc.output, ["name"]).value, "Ada");
  // The untouched whitespace change record survives with its identity.
  assert.ok(inc.changes.some((c) => c.ruleId === "ws" && c.field === "/name"));
  // Incremental result equals a full recompute with the new rules.
  const full = applyRules(doc, reg.activeRules());
  assert.ok(deepEqual(inc.output, full.output));
});

test("incremental recompute propagates through dependency chains", () => {
  const reg = new RuleRegistry();
  const rules: Rule[] = [
    { id: "src", kind: "enum-alias", aliases: { a: "A" }, targets: [["status"]] },
    { id: "cp", kind: "copy", from: ["status"], to: ["shadow"], targets: [] },
    { id: "ws", kind: "whitespace", mode: "trim", targets: [["shadow"]] },
  ];
  assert.ok(reg.publish(rules).ok);
  const prev = reg.normalize({ status: "a" });
  assert.equal(getAt(prev.output, ["shadow"]).value, "A");

  const update = reg.updateRule("src", {
    id: "src",
    kind: "enum-alias",
    aliases: { a: "Alpha" },
    targets: [["status"]],
  });
  assert.ok(update.ok);
  assert.deepEqual(update.affectedRuleIds.sort(), ["cp", "src", "ws"]);

  const inc = reg.recompute(prev, update.affectedRuleIds);
  assert.equal(getAt(inc.output, ["status"]).value, "Alpha");
  assert.equal(getAt(inc.output, ["shadow"]).value, "Alpha");
  const full = applyRules({ status: "a" }, reg.activeRules());
  assert.ok(deepEqual(inc.output, full.output));
});

test("inverse patches still restore the input after incremental recompute", () => {
  const reg = new RuleRegistry();
  assert.ok(reg.publish(baseRules).ok);
  const prev = reg.normalize(doc);
  const update = reg.updateRule("enum", {
    id: "enum",
    kind: "enum-alias",
    aliases: { na: "NOT-APPLICABLE" },
    targets: [["status"]],
  });
  assert.ok(update.ok);
  const inc = reg.recompute(prev, update.affectedRuleIds);
  const restored = applyPatchOps(inc.output, inc.inverse);
  assert.ok(deepEqual(restored, doc));
});
