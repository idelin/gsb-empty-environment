import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { parseProblem } from "../src/model.ts";
import type { ProblemSpec } from "../src/model.ts";
import { solve } from "../src/solver.ts";
import { buildCertificate, verifyCertificate } from "../src/certificate.ts";
import type { Certificate } from "../src/certificate.ts";

const sampleSpec = JSON.parse(readFileSync(new URL("../examples/sample.json", import.meta.url), "utf8")) as ProblemSpec;

function solveSample() {
  const problem = parseProblem(sampleSpec);
  const result = solve(problem);
  return { problem, cert: buildCertificate(problem, result) };
}

test("a genuine certificate verifies", () => {
  const { problem, cert } = solveSample();
  const v = verifyCertificate(problem, cert);
  assert.equal(v.valid, true, v.reasons.join("; "));
  assert.equal(v.stale, false);
});

test("a tampered upper bound is rejected", () => {
  const { problem, cert } = solveSample();
  const forged: Certificate = { ...cert, upperBound: "26" };
  const v = verifyCertificate(problem, forged);
  assert.equal(v.valid, false);
  assert.ok(v.reasons.some((r) => r.includes("26")));
});

test("a tampered node log is rejected", () => {
  const { problem, cert } = solveSample();
  const forged: Certificate = { ...cert, log: cert.log.slice(1) };
  const v = verifyCertificate(problem, forged);
  assert.equal(v.valid, false);
  assert.ok(v.reasons.some((r) => r.includes("replay")));
});

test("objective change makes the old certificate stale and invalid", () => {
  const { problem, cert } = solveSample();
  // Objective change: reprice one grade. The old certificate must not carry over.
  const changed: ProblemSpec = JSON.parse(JSON.stringify(sampleSpec));
  changed.components[0].grades[0].cost = "100";
  const v = verifyCertificate(parseProblem(changed), cert);
  assert.equal(v.stale, true);
  assert.equal(v.valid, false);
  assert.ok(v.reasons.some((r) => r.includes("hash mismatch")));
});

test("budget-exhausted run certifies only 'undecided', never 'optimal'", () => {
  const problem = parseProblem(sampleSpec);
  const result = solve(problem, { maxNodes: 3 });
  assert.equal(result.status, "undecided");
  const cert = buildCertificate(problem, result, 3);
  const v = verifyCertificate(problem, cert);
  assert.equal(v.valid, true, v.reasons.join("; "));
  // Forging an optimality claim on the same budget is caught.
  const forged: Certificate = { ...cert, status: "optimal" };
  const v2 = verifyCertificate(problem, forged);
  assert.equal(v2.valid, false);
});

test("an infeasible instance certificate verifies", () => {
  const problem = parseProblem({
    errorLimit: "0.001",
    components: [{ id: "a", sensitivities: { s: "1" }, grades: [{ id: "g", cost: "1", errors: { s: ["-0.5", "0.5"] } }] }],
  });
  const result = solve(problem);
  assert.equal(result.status, "infeasible");
  const cert = buildCertificate(problem, result);
  assert.equal(verifyCertificate(problem, cert).valid, true);
});
