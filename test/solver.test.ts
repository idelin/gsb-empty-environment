import test from "node:test";
import assert from "node:assert/strict";
import {
  MAX_EXCEPTIONS,
  enumerateRequests,
  evaluate,
  matches,
  validateScenario,
} from "../src/model.ts";
import type { Effect, ExceptionRule, Rule, Scenario, SafetyProperty } from "../src/model.ts";
import { buildConstraints, retainedExceptions, solveExact, solveFast } from "../src/solver.ts";
import { verifyPlan } from "../src/verify.ts";
import { demoScenario } from "../src/scenario.ts";

// ---------- helpers ----------

/** Optimum by brute force over all 2^n revocation subsets, checking the full domain. */
function bruteForceOptimum(scenario: Scenario): number | null {
  const exceptions = scenario.policy.exceptions;
  const n = exceptions.length;
  const requests = enumerateRequests(scenario.domains);
  const relevant = requests.filter((r) => matches(scenario.property.when, r));
  let best: number | null = null;
  for (let mask = 0; mask < 1 << n; mask++) {
    const retained = exceptions.filter((_, i) => !(mask & (1 << i)));
    const feasible = relevant.every(
      (r) => evaluate(scenario.policy, retained, r).effect === scenario.property.mustBe,
    );
    if (!feasible) continue;
    let cost = 0;
    for (let i = 0; i < n; i++) if (mask & (1 << i)) cost += exceptions[i].cost;
    if (best === null || cost < best) best = cost;
  }
  return best;
}

/** Deterministic PRNG (mulberry32). */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomScenario(rand: () => number, index: number): Scenario {
  const pick = <T>(arr: T[]): T => arr[Math.floor(rand() * arr.length)];
  const subset = (arr: readonly string[]): string[] => {
    const out = arr.filter(() => rand() < 0.6);
    return out.length === 0 ? [arr[0]] : out;
  };
  const domains = {
    role: ["admin", "staff", "guest"],
    network: ["internal", "public"],
    resource: ["docs", "billing", "secrets"],
  } as const;
  const cond = (): Record<string, readonly string[]> => {
    const c: Record<string, readonly string[]> = {};
    if (rand() < 0.8) c.role = subset(domains.role);
    if (rand() < 0.6) c.network = subset(domains.network);
    if (rand() < 0.8) c.resource = subset(domains.resource);
    return c;
  };
  const effect = (): Effect => (rand() < 0.5 ? "allow" : "deny");
  const nEx = 4 + Math.floor(rand() * 5); // 4..8 exceptions
  const exceptions: ExceptionRule[] = [];
  for (let i = 0; i < nEx; i++) {
    exceptions.push({
      id: `ex${index}-${i}`,
      effect: effect(),
      priority: Math.floor(rand() * 100),
      when: cond(),
      cost: 1 + Math.floor(rand() * 9),
    });
  }
  const baseRules: Rule[] = [];
  const nBase = Math.floor(rand() * 3);
  for (let i = 0; i < nBase; i++) {
    baseRules.push({
      id: `base${index}-${i}`,
      effect: effect(),
      priority: Math.floor(rand() * 60),
      when: cond(),
    });
  }
  const property: SafetyProperty = {
    when: cond(),
    mustBe: effect(),
    description: "random property",
  };
  return {
    name: `random-${index}`,
    domains,
    policy: { defaultEffect: effect(), baseRules, exceptions },
    property,
  };
}

// ---------- tests ----------

test("demo scenario: optimal revocation set and cost", () => {
  validateScenario(demoScenario);
  const outcome = solveFast(demoScenario);
  assert.equal(outcome.status, "optimal");
  if (outcome.status !== "optimal") return;
  const ids = outcome.revoked.map((i) => demoScenario.policy.exceptions[i].id).sort();
  assert.deepEqual(ids, ["ex-audit-temp", "ex-incident-77", "ex-vendor-access"]);
  assert.equal(outcome.totalCost, 13);
});

test("demo scenario: exhaustive verification passes and summaries are consistent", () => {
  const outcome = solveFast(demoScenario);
  assert.equal(outcome.status, "optimal");
  if (outcome.status !== "optimal") return;
  const report = verifyPlan(demoScenario, outcome);
  assert.equal(report.propertyHolds, true);
  assert.equal(report.violationsAfter, 0);
  assert.ok(report.violationsBefore > 0);
  assert.equal(report.retained.length, demoScenario.policy.exceptions.length - 3);
  for (const r of report.retained) assert.equal(r.propertyViolations, 0, r.id);
  for (const r of report.revoked) assert.equal(r.necessityChecked, true, r.id);
});

test("first-match by priority semantics", () => {
  const scenario: Scenario = {
    name: "priority",
    domains: { x: ["a"] },
    policy: {
      defaultEffect: "deny",
      baseRules: [],
      exceptions: [
        { id: "low", effect: "deny", priority: 1, when: {}, cost: 1 },
        { id: "high", effect: "allow", priority: 2, when: {}, cost: 1 },
      ],
    },
    property: { when: {}, mustBe: "deny", description: "all denied" },
  };
  // Higher priority allow wins while retained; revoking it restores deny.
  assert.equal(evaluate(scenario.policy, scenario.policy.exceptions, { x: "a" }).effect, "allow");
  const outcome = solveFast(scenario);
  assert.equal(outcome.status, "optimal");
  if (outcome.status !== "optimal") return;
  assert.deepEqual(outcome.revoked, [1]); // only "high" must go
  assert.equal(outcome.totalCost, 1);
});

test("infeasible when base policy violates and no exception can fix it", () => {
  const scenario: Scenario = {
    name: "infeasible",
    domains: { x: ["a", "b"] },
    policy: {
      defaultEffect: "allow",
      baseRules: [],
      exceptions: [{ id: "ex", effect: "deny", priority: 1, when: { x: ["a"] }, cost: 1 }],
    },
    property: { when: {}, mustBe: "deny", description: "deny all" },
  };
  const outcome = solveFast(scenario);
  assert.equal(outcome.status, "infeasible");
  const exact = solveExact(scenario);
  assert.equal(exact.status, "infeasible");
});

test("solveFast matches brute-force optimum on random scenarios", () => {
  const rand = rng(20261005);
  for (let i = 0; i < 200; i++) {
    const scenario = randomScenario(rand, i);
    validateScenario(scenario);
    const brute = bruteForceOptimum(scenario);
    const fast = solveFast(scenario);
    if (brute === null) {
      assert.equal(fast.status, "infeasible", `${scenario.name}: expected infeasible`);
    } else {
      assert.equal(fast.status, "optimal", `${scenario.name}: expected optimal`);
      if (fast.status === "optimal") {
        assert.equal(fast.totalCost, brute, `${scenario.name}: cost mismatch`);
      }
    }
  }
});

test("solveExact agrees with solveFast on random scenarios", () => {
  const rand = rng(777);
  for (let i = 0; i < 100; i++) {
    const scenario = randomScenario(rand, i);
    const fast = solveFast(scenario);
    const exact = solveExact(scenario);
    assert.equal(exact.status === "optimal" ? "optimal" : exact.status, fast.status, scenario.name);
    if (fast.status === "optimal" && exact.status === "optimal") {
      assert.equal(exact.totalCost, fast.totalCost, scenario.name);
    }
  }
});

test("verified plan keeps property on every retained exception (random scenarios)", () => {
  const rand = rng(4242);
  for (let i = 0; i < 60; i++) {
    const scenario = randomScenario(rand, i);
    const fast = solveFast(scenario);
    if (fast.status !== "optimal") continue;
    const report = verifyPlan(scenario, fast);
    assert.equal(report.propertyHolds, true, scenario.name);
    for (const r of report.retained) assert.equal(r.propertyViolations, 0, r.id);
    for (const r of report.revoked) assert.equal(r.necessityChecked, true, r.id);
  }
});

test("necessity: removing any exception from the plan breaks some request", () => {
  const outcome = solveFast(demoScenario);
  assert.equal(outcome.status, "optimal");
  if (outcome.status !== "optimal") return;
  const requests = enumerateRequests(demoScenario.domains);
  const relevant = requests.filter((r) => matches(demoScenario.property.when, r));
  for (const idx of outcome.revoked) {
    const smaller = outcome.revoked.filter((i) => i !== idx);
    const retained = retainedExceptions(demoScenario, smaller);
    const stillSafe = relevant.every(
      (r) => evaluate(demoScenario.policy, retained, r).effect === demoScenario.property.mustBe,
    );
    assert.equal(stillSafe, false, `exception ${idx} should be necessary`);
  }
});

test("constraint extraction covers the whole finite domain", () => {
  const stats = buildConstraints(demoScenario);
  assert.equal(stats.totalRequests, 36);
  assert.equal(stats.relevantRequests, 6);
  assert.ok(stats.constraints.length > 0);
});

test("rejects more than MAX_EXCEPTIONS exceptions", () => {
  const exceptions: ExceptionRule[] = [];
  for (let i = 0; i < MAX_EXCEPTIONS + 1; i++) {
    exceptions.push({ id: `ex${i}`, effect: "allow", priority: i, when: {}, cost: 1 });
  }
  const scenario: Scenario = {
    name: "too-many",
    domains: { x: ["a"] },
    policy: { defaultEffect: "deny", baseRules: [], exceptions },
    property: { when: {}, mustBe: "deny", description: "" },
  };
  assert.throws(() => validateScenario(scenario), /too many exceptions/);
});

test("handles the 24-exception maximum efficiently", () => {
  const rand = rng(99);
  const exceptions: ExceptionRule[] = [];
  for (let i = 0; i < MAX_EXCEPTIONS; i++) {
    exceptions.push({
      id: `big-${i}`,
      effect: rand() < 0.5 ? "allow" : "deny",
      priority: i,
      when: { role: ["staff", "guest"], resource: ["secrets"] },
      cost: 1 + Math.floor(rand() * 9),
    });
  }
  const scenario: Scenario = {
    name: "max-size",
    domains: { role: ["admin", "staff", "guest"], resource: ["docs", "secrets"] },
    policy: { defaultEffect: "deny", baseRules: [], exceptions },
    property: {
      when: { role: ["staff", "guest"], resource: ["secrets"] },
      mustBe: "deny",
      description: "secrets denied to non-admins",
    },
  };
  validateScenario(scenario);
  const start = Date.now();
  const outcome = solveFast(scenario);
  assert.equal(outcome.status, "optimal");
  assert.ok(Date.now() - start < 1000, "solveFast should be fast at the 24-exception cap");
  if (outcome.status !== "optimal") return;
  const report = verifyPlan(scenario, outcome);
  assert.equal(report.propertyHolds, true);
});
