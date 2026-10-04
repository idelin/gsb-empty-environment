import { test } from "node:test";
import assert from "node:assert/strict";
import { solve } from "../src/solver";
import { buildReport } from "../src/report";
import { verifyPolicyFull } from "../src/verifier";
import { enumerateDomain } from "../src/domain";
import { evaluate, sortByPrecedence } from "../src/policy";
import { Effect, ExceptionRule, Policy, Request } from "../src/types";

function makePolicy(overrides: Partial<Policy>): Policy {
  return {
    baseEffect: "deny",
    domain: { role: ["admin", "user"], action: ["read", "write"] },
    property: [],
    exceptions: [],
    ...overrides,
  };
}

function rule(partial: Partial<ExceptionRule> & { id: string }): ExceptionRule {
  return { effect: "allow", priority: 1, cost: 1, match: {}, ...partial };
}

test("revokes the single exception that breaks the property", () => {
  const policy = makePolicy({
    property: [{ id: "p1", match: { role: "user" }, must: "deny" }],
    exceptions: [rule({ id: "bad", effect: "allow", cost: 5, match: { role: "user" } })],
  });
  const result = solve(policy);
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.totalCost, 5);
  assert.deepEqual(
    result.revoked.map((r) => r.id),
    ["bad"]
  );
});

test("keeps everything when the property already holds", () => {
  const policy = makePolicy({
    property: [{ id: "p1", match: { role: "user" }, must: "deny" }],
    exceptions: [rule({ id: "ok", effect: "allow", match: { role: "admin" } })],
  });
  const result = solve(policy);
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.totalCost, 0);
  assert.equal(result.revoked.length, 0);
});

test("revokes every allow rule that can decide a constrained request", () => {
  const policy = makePolicy({
    domain: { role: ["user"], action: ["read", "write"] },
    property: [{ id: "p1", match: {}, must: "deny" }],
    exceptions: [
      rule({ id: "broad-allow", priority: 1, cost: 6, match: {} }),
      rule({ id: "read-allow", priority: 2, cost: 2, match: { action: "read" } }),
      rule({ id: "write-allow", priority: 3, cost: 2, match: { action: "write" } }),
    ],
  });
  const result = solve(policy);
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.totalCost, 10);
  assert.deepEqual(
    result.revoked.map((r) => r.id).sort(),
    ["broad-allow", "read-allow", "write-allow"]
  );
});

test("cascades revocation through shadowing rules until the property holds", () => {
  const policy = makePolicy({
    domain: { role: ["user"], action: ["read", "write"] },
    property: [{ id: "p1", match: {}, must: "allow" }],
    exceptions: [
      rule({ id: "broad-deny", effect: "deny", priority: 1, cost: 3, match: {} }),
      rule({ id: "hidden-deny", effect: "deny", priority: 2, cost: 2, match: {} }),
      rule({ id: "restore-allow", effect: "allow", priority: 4, cost: 50, match: {} }),
    ],
  });
  const result = solve(policy);
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.totalCost, 5);
  assert.deepEqual(
    result.revoked.map((r) => r.id).sort(),
    ["broad-deny", "hidden-deny"]
  );
});

test("does not revoke shadowed rules that never decide anything", () => {
  const policy = makePolicy({
    property: [{ id: "p1", match: { role: "user" }, must: "deny" }],
    exceptions: [
      rule({ id: "guard-deny", effect: "deny", priority: 1, cost: 1, match: {} }),
      rule({ id: "shadowed-allow", effect: "allow", priority: 2, cost: 7, match: { role: "user" } }),
    ],
  });
  const result = solve(policy);
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.totalCost, 0);
  assert.equal(result.revoked.length, 0);
});

test("respects first-match-by-priority semantics", () => {
  const policy = makePolicy({
    domain: { role: ["user"], action: ["read"] },
    property: [{ id: "p1", match: {}, must: "deny" }],
    exceptions: [
      rule({ id: "top-allow", effect: "allow", priority: 1, cost: 9, match: {} }),
      rule({ id: "shadowed-deny", effect: "deny", priority: 2, cost: 1, match: {} }),
    ],
  });
  const result = solve(policy);
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.equal(result.totalCost, 9);
  assert.deepEqual(
    result.revoked.map((r) => r.id),
    ["top-allow"]
  );
});

test("reports infeasible when no exception can restore the property", () => {
  const policy = makePolicy({
    baseEffect: "allow",
    property: [{ id: "p1", match: { role: "user" }, must: "deny" }],
    exceptions: [rule({ id: "noop", effect: "allow", match: { role: "admin" } })],
  });
  const result = solve(policy);
  assert.equal(result.status, "infeasible");
});

test("rejects policies with more than 24 exceptions", () => {
  const exceptions = Array.from({ length: 25 }, (_, i) =>
    rule({ id: `e${i}`, priority: i + 1 })
  );
  const result = solve(makePolicy({ exceptions }));
  assert.equal(result.status, "too-many-exceptions");
});

test("solved policy verifies exhaustively and retained rules have clean summaries", () => {
  const policy = makePolicy({
    domain: { role: ["admin", "user", "guest"], action: ["read", "write"] },
    property: [
      { id: "p1", match: { role: "guest" }, must: "deny" },
      { id: "p2", match: { action: "write", role: "user" }, must: "deny" },
    ],
    exceptions: [
      rule({ id: "admin-all", priority: 1, cost: 10, match: { role: "admin" } }),
      rule({ id: "any-read", priority: 2, cost: 3, match: { action: "read" } }),
      rule({ id: "user-write-temp", priority: 3, cost: 2, match: { role: "user", action: "write" } }),
    ],
  });
  const result = solve(policy);
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  assert.deepEqual(
    result.revoked.map((r) => r.id).sort(),
    ["any-read", "user-write-temp"]
  );
  assert.equal(result.totalCost, 5);
  const requests = enumerateDomain(policy.domain);
  const verification = verifyPolicyFull(policy, result.kept, requests);
  assert.equal(verification.ok, true);
  assert.equal(verification.checkedRequests, 6);

  const report = buildReport(policy, result);
  assert.equal(report.verification.violations, 0);
  assert.equal(report.verification.domainSize, 6);
  assert.equal(report.retained.length, 1);
  for (const entry of report.retained) {
    assert.equal(entry.violations, 0);
    assert.ok(entry.summary.includes(entry.ruleId));
  }
  const adminAll = report.retained.find((r) => r.ruleId === "admin-all")!;
  assert.equal(adminAll.decisiveForRequests, 2);
});

test("brute-force cross-check on randomized small policies", () => {
  let seed = 123456789;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };
  const pick = <T,>(items: readonly T[]): T => items[Math.floor(rand() * items.length)]!;

  for (let trial = 0; trial < 200; trial++) {
    const roles = ["a", "b"];
    const actions = ["r", "w"];
    const domain = { role: roles, action: actions };
    const requests = enumerateDomain(domain);
    const n = 1 + Math.floor(rand() * 6);
    const exceptions: ExceptionRule[] = Array.from({ length: n }, (_, i) => ({
      id: `e${i}`,
      effect: pick(["allow", "deny"] as const),
      priority: i + 1,
      cost: 1 + Math.floor(rand() * 9),
      match: (rand() < 0.5 ? {} : { role: pick(roles) }) as ExceptionRule["match"],
    }));
    const policy = makePolicy({
      baseEffect: pick(["allow", "deny"] as const),
      domain,
      property: [
        {
          id: "p1",
          match: { role: pick(roles), action: pick(actions) },
          must: pick(["allow", "deny"] as const),
        },
      ],
      exceptions,
    });

    const result = solve(policy);
    const sorted = sortByPrecedence(exceptions);

    let bestCost = Number.POSITIVE_INFINITY;
    let feasible = false;
    for (let mask = 0; mask < 1 << n; mask++) {
      const kept = sorted.filter((_, i) => (mask & (1 << i)) === 0);
      let cost = 0;
      for (let i = 0; i < n; i++) if (mask & (1 << i)) cost += sorted[i]!.cost;
      if (cost >= bestCost) continue;
      let ok = true;
      for (const req of requests) {
        const satisfied = policy.property.every((c) => {
          const hit = Object.entries(c.match).every(([k, v]) => req[k] === v);
          return !hit || evaluate(req, kept, policy.baseEffect).effect === c.must;
        });
        if (!satisfied) {
          ok = false;
          break;
        }
      }
      if (ok) {
        bestCost = cost;
        feasible = true;
      }
    }

    if (!feasible) {
      assert.equal(result.status, "infeasible", `trial ${trial} should be infeasible`);
    } else {
      assert.equal(result.status, "ok", `trial ${trial} should be solvable`);
      if (result.status !== "ok") return;
      assert.equal(result.totalCost, bestCost, `trial ${trial} cost mismatch`);
    }
  }
});

test("handles the 24-exception upper bound within a reasonable node budget", () => {
  const exceptions = Array.from({ length: 24 }, (_, i) =>
    rule({
      id: `e${i}`,
      priority: i + 1,
      cost: (i % 5) + 1,
      match: i % 2 === 0 ? { role: "user" } : { action: "read" },
      effect: i % 3 === 0 ? "deny" : "allow",
    })
  );
  const policy = makePolicy({
    property: [{ id: "p1", match: { role: "user", action: "read" }, must: "deny" }],
    exceptions,
  });
  const result = solve(policy);
  assert.equal(result.status, "ok");
  if (result.status !== "ok") return;
  const requests = enumerateDomain(policy.domain);
  assert.equal(verifyPolicyFull(policy, result.kept, requests).ok, true);
});

test("request typing stays consistent with evaluated decisions", () => {
  const req: Request = { role: "admin", action: "read" };
  const decision = evaluate(req, [], "deny");
  const effect: Effect = decision.effect;
  assert.equal(effect, "deny");
});
