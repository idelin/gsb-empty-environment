import assert from "node:assert/strict";
import { test } from "node:test";
import { compareRuleSets, regionForInput, type Region } from "../src/compare.ts";
import { domain, newPolicy, oldPolicy } from "../src/policies.ts";
import type { Input, RuleSet } from "../src/types.ts";

const result = compareRuleSets(oldPolicy, newPolicy, domain);

const region = (input: Input): Region =>
  regionForInput(result, oldPolicy, newPolicy, input);

test("analysis universe is the declared finite product", () => {
  assert.equal(result.totalInputs, 4 * 3 * 4 * 2 * 3 * 2);
});

test("summary partitions every input point exactly once", () => {
  const s = result.summary;
  assert.equal(
    s.allowToDeny + s.denyToAllow + s.explanationOnly + s.unchanged,
    result.totalInputs,
  );
});

test("allow -> deny and deny -> allow are separate categories", () => {
  assert.ok(result.summary.allowToDeny > 0);
  assert.ok(result.summary.denyToAllow > 0);
  for (const r of result.regions) {
    if (r.changeType === "decision-changed") {
      assert.ok(r.direction === "allow->deny" || r.direction === "deny->allow");
    } else {
      assert.equal(r.direction, null);
    }
  }
});

test("admin prod report flips allow -> deny when rule tightens to editor only", () => {
  const r = region({
    role: "admin", env: "prod", resource: "report", mfa: true, age: 0, risk: 0,
  });
  assert.equal(r.changeType, "decision-changed");
  assert.equal(r.direction, "allow->deny");
  assert.deepEqual([...r.oldDecision.matchedRuleIds], ["r_editor_report_allow"]);
  assert.equal(r.newDecision.defaulted, true);
});

test("editor staging report flips allow -> deny via the new explicit deny", () => {
  const r = region({
    role: "editor", env: "staging", resource: "report", mfa: true, age: 0, risk: 0,
  });
  assert.equal(r.direction, "allow->deny");
  assert.deepEqual([...r.newDecision.matchedRuleIds], ["r_staging_editor_report_deny"]);
});

test("viewer staging dashboard without mfa flips deny -> allow (broadened rule)", () => {
  const r = region({
    role: "viewer", env: "staging", resource: "dashboard", mfa: false, age: 0, risk: 0,
  });
  assert.equal(r.direction, "deny->allow");
  assert.equal(r.oldDecision.defaulted, true);
  assert.deepEqual([...r.newDecision.matchedRuleIds], ["r_viewer_dashboard_broadened"]);
});

test("prod dashboard without mfa stays deny: new deny replaces a former default", () => {
  const r = region({
    role: "viewer", env: "prod", resource: "dashboard", mfa: false, age: 0, risk: 0,
  });
  assert.equal(r.changeType, "explanation-only");
  assert.equal(r.oldEffect, "deny");
  assert.equal(r.newEffect, "deny");
  assert.equal(r.oldDecision.defaulted, true);
  assert.deepEqual([...r.newDecision.matchedRuleIds], ["r_prod_no_mfa_deny"]);
});

test("age gate relaxation yields deny -> allow where an allow rule now fires", () => {
  const r = region({
    role: "admin", env: "prod", resource: "admin_panel", mfa: true, age: 2, risk: 0,
  });
  assert.equal(r.direction, "deny->allow");
  assert.deepEqual([...r.oldDecision.matchedRuleIds], ["r_age_deny"]);
  assert.deepEqual([...r.newDecision.matchedRuleIds], ["r_admin_panel_allow_v2"]);
});

test("renaming admin rules is explanation-only", () => {
  const r = region({
    role: "admin", env: "prod", resource: "admin_panel", mfa: true, age: 0, risk: 0,
  });
  assert.equal(r.changeType, "explanation-only");
  assert.equal(r.oldEffect, "allow");
  assert.deepEqual([...r.oldDecision.matchedRuleIds], ["r_admin_panel_allow"]);
  assert.deepEqual([...r.newDecision.matchedRuleIds], ["r_admin_panel_allow_v2"]);
});

test("dev logs split is explanation-only for the non-admin branch", () => {
  const r = region({
    role: "viewer", env: "dev", resource: "logs", mfa: true, age: 0, risk: 0,
  });
  assert.equal(r.changeType, "explanation-only");
  assert.deepEqual([...r.oldDecision.matchedRuleIds], ["r_dev_logs_allow"]);
  assert.deepEqual([...r.newDecision.matchedRuleIds], ["r_dev_logs_general"]);
});

test("guest inputs remain denied regardless of the allow-side edits", () => {
  const r = region({
    role: "guest", env: "dev", resource: "logs", mfa: true, age: 0, risk: 0,
  });
  assert.equal(r.oldEffect, "deny");
  assert.equal(r.newEffect, "deny");
  assert.notEqual(r.changeType, "decision-changed");
});

test("shuffling the NEW rule set produces exactly the same comparison", () => {
  const shuffled: RuleSet = { ...newPolicy, rules: [...newPolicy.rules].reverse() };
  const shuffledResult = compareRuleSets(oldPolicy, shuffled, domain);
  assert.deepEqual(shuffledResult.summary, result.summary);
  assert.equal(shuffledResult.regions.length, result.regions.length);
});
