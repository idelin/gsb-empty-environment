// End-to-end demo: normalization, change records, inverse patches,
// dependency analysis, idempotency search, versioned publication and
// incremental recompute. Run with: npm run sample

import {
  RuleRegistry,
  analyzeRules,
  applyPatchOps,
  applyRules,
  deepEqual,
  getAt,
  searchIdempotency,
} from "../src/index.ts";
import type { JsonValue, Rule } from "../src/index.ts";

function show(title: string): void {
  console.log(`\n=== ${title} ===`);
}

const rules: Rule[] = [
  {
    id: "ws-name",
    kind: "whitespace",
    mode: "collapse",
    targets: [["user", "name"], ["user", "tags", "*"]],
  },
  {
    id: "date-when",
    kind: "date",
    formats: ["YYYY-MM-DD", "MM/DD/YYYY"],
    targets: [["events", "*", "when"]],
  },
  {
    id: "enum-status",
    kind: "enum-alias",
    aliases: { na: "N/A", "n/a": "N/A" },
    targets: [["status"]],
  },
];

const doc: JsonValue = {
  user: { name: "  Ada   Lovelace ", bio: null, tags: ["  math ", "poetry  "] },
  events: [
    { when: "03/04/2025", kind: "talk" },
    { when: "2025-13-01", kind: "deadline" },
    { when: "whenever", kind: "note" },
  ],
  status: "na",
};

show("1. Normalize with per-field change records");
const result = applyRules(doc, rules);
console.log("output:", JSON.stringify(result.output));
for (const c of result.changes) {
  console.log(
    `  #${c.seq} [${c.ruleId}] ${c.field}: ${JSON.stringify(c.before)} -> ${JSON.stringify(c.after)}`,
  );
}

show("2. Errors keep original values and locate the exact path");
for (const e of result.errors) {
  console.log(`  [${e.ruleId}] ${e.field}: ${e.message} (kept ${JSON.stringify(e.value)})`);
}
console.log("  /user/bio is still null:", getAt(result.output, ["user", "bio"]).value === null);

show("3. Inverse patch restores the input exactly");
const restored = applyPatchOps(result.output, result.inverse);
console.log("  restored === input:", deepEqual(restored, doc));

show("4. Normalizing twice changes nothing further");
const twice = applyRules(result.output, rules);
console.log("  stable:", deepEqual(result.output, twice.output), "| 2nd-pass changes:", twice.changes.length);

show("5. Static dependency analysis of a problematic rule set");
const problematic: Rule[] = [
  { id: "e1", kind: "enum-alias", aliases: { na: "N/A" }, targets: [["status"]] },
  { id: "e2", kind: "enum-alias", aliases: { na: "Unknown" }, targets: [["status"]] },
  { id: "chain", kind: "enum-alias", aliases: { a: "b", b: "c" }, targets: [["level"]] },
  { id: "c1", kind: "copy", from: ["x"], to: ["y"], targets: [] },
  { id: "c2", kind: "copy", from: ["y"], to: ["x"], targets: [] },
];
const analysis = analyzeRules(problematic);
console.log("  edges:", analysis.edges.map((e) => `${e.from}->${e.to}`).join(", "));
for (const issue of analysis.issues) {
  console.log(`  [${issue.type}] (${issue.ruleIds.join(", ")}) ${issue.message}`);
}

show("6. Idempotency counterexample search over finite domains");
const shape = { user: { name: "", tags: [""] }, events: [{ when: "" }], status: "" };
const proven = searchIdempotency(rules, shape, 2_000_000);
console.log(`  good rules:  status=${proven.status} checked=${proven.checked}/${proven.total}`);
const refuted = searchIdempotency(
  [{ id: "chain", kind: "enum-alias", aliases: { a: "b", b: "c" }, targets: [["status"]] }],
  { status: "" },
  10_000,
);
console.log(
  `  chain rule:  status=${refuted.status} witness: input=${JSON.stringify(refuted.counterexample?.input)}` +
    ` once=${JSON.stringify(refuted.counterexample?.once)} twice=${JSON.stringify(refuted.counterexample?.twice)} at ${refuted.counterexample?.path}`,
);
const unknown = searchIdempotency(rules, shape, 10);
console.log(`  tiny budget: status=${unknown.status} checked=${unknown.checked}/${unknown.total} (never "proven")`);

show("7. Versioned publication: invalid publish cannot break the last good version");
const registry = new RuleRegistry();
console.log("  publish v1:", registry.publish(rules));
const before = registry.normalize(doc);
const rejected = registry.publish([
  ...rules,
  { id: "clash", kind: "enum-alias", aliases: { na: "DIFFERENT" }, targets: [["status"]] },
]);
console.log("  publish v2 (conflicting):", rejected.ok ? "accepted" : `rejected: ${rejected.issues[0].message}`);
const after = registry.normalize(doc);
console.log("  active version still:", after.version, "| results identical:", deepEqual(before.output, after.output));

show("8. Local rule update recomputes only dependent fields");
const update = registry.updateRule("enum-status", {
  id: "enum-status",
  kind: "enum-alias",
  aliases: { na: "NOT-APPLICABLE", "n/a": "NOT-APPLICABLE" },
  targets: [["status"]],
});
if (update.ok) {
  console.log(`  published v${update.version}; affected rules: ${update.affectedRuleIds.join(", ")}`);
  const incremental = registry.recompute(before, update.affectedRuleIds);
  const full = applyRules(doc, registry.activeRules());
  console.log("  /status now:", JSON.stringify(getAt(incremental.output, ["status"]).value));
  console.log("  incremental === full recompute:", deepEqual(incremental.output, full.output));
  console.log(
    "  untouched change records kept:",
    incremental.changes.filter((c) => c.ruleId !== "enum-status").length,
  );
}
