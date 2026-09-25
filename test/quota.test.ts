import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  InsufficientQuotaError,
  OperationConflictError,
  PolicyViolationError,
  StalePolicyError,
  UnknownOperationError,
} from "../src/errors.ts";
import { QuotaLedger } from "../src/ledger.ts";
import { definePolicy, evaluatePolicy } from "../src/policy.ts";
import { QuotaService } from "../src/service.ts";

const POLICY_V1 = {
  version: 1,
  condition: {
    op: "and",
    conditions: [
      { op: "in", attr: "tier", set: ["pro", "enterprise"] },
      { op: "gte", attr: "ageDays", value: 30 },
      { op: "not", condition: { op: "eq", attr: "riskLevel", value: 3 } },
    ],
  },
} as const;

const GOOD_ATTRS = { tier: "pro", ageDays: 90, riskLevel: 1 } as const;

function makeService() {
  return new QuotaService({ userCredits: 10, gpuHours: 4 }, POLICY_V1);
}

describe("multi-dimensional atomic deduction", () => {
  it("deducts every dimension when all fit", () => {
    const svc = makeService();
    const permit = svc.acquire({
      operationId: "op-1",
      requirements: { userCredits: 3, gpuHours: 1 },
      attributes: GOOD_ATTRS,
      policyVersion: 1,
    });
    assert.equal(permit.status, "active");
    assert.deepEqual(svc.snapshot(), {
      userCredits: { capacity: 10, used: 3 },
      gpuHours: { capacity: 4, used: 1 },
    });
  });

  it("deducts nothing on any dimension when one dimension is short", () => {
    const svc = makeService();
    // userCredits fits (6 <= 10) but gpuHours does not (5 > 4).
    assert.throws(
      () =>
        svc.acquire({
          operationId: "op-2",
          requirements: { userCredits: 6, gpuHours: 5 },
          attributes: GOOD_ATTRS,
          policyVersion: 1,
        }),
      (err: unknown) => {
        assert.ok(err instanceof InsufficientQuotaError);
        assert.deepEqual(err.shortages, { gpuHours: { requested: 5, available: 4 } });
        return true;
      },
    );
    assert.deepEqual(svc.snapshot(), {
      userCredits: { capacity: 10, used: 0 },
      gpuHours: { capacity: 4, used: 0 },
    });
  });

  it("rejects unknown dimensions without side effects", () => {
    const svc = makeService();
    assert.throws(
      () =>
        svc.acquire({
          operationId: "op-3",
          requirements: { userCredits: 1, tapes: 1 },
          attributes: GOOD_ATTRS,
          policyVersion: 1,
        }),
      InsufficientQuotaError,
    );
    assert.equal(svc.snapshot().userCredits?.used, 0);
  });
});

describe("idempotent operation ids", () => {
  it("deducts only once when the same id is retried with the same requirements", () => {
    const svc = makeService();
    const req = {
      operationId: "op-10",
      requirements: { userCredits: 4, gpuHours: 2 },
      attributes: GOOD_ATTRS,
      policyVersion: 1,
    } as const;
    const first = svc.acquire(req);
    const second = svc.acquire(req);
    assert.deepEqual(second, first);
    assert.deepEqual(svc.snapshot(), {
      userCredits: { capacity: 10, used: 4 },
      gpuHours: { capacity: 4, used: 2 },
    });
  });

  it("treats key order as irrelevant for replay detection", () => {
    const svc = makeService();
    svc.acquire({
      operationId: "op-11",
      requirements: { userCredits: 1, gpuHours: 1 },
      attributes: GOOD_ATTRS,
      policyVersion: 1,
    });
    const replay = svc.acquire({
      operationId: "op-11",
      requirements: { gpuHours: 1, userCredits: 1 },
      attributes: GOOD_ATTRS,
      policyVersion: 1,
    });
    assert.equal(replay.status, "active");
    assert.equal(svc.snapshot().userCredits?.used, 1);
  });

  it("rejects the same id submitted with different requirements", () => {
    const svc = makeService();
    svc.acquire({
      operationId: "op-12",
      requirements: { userCredits: 2 },
      attributes: GOOD_ATTRS,
      policyVersion: 1,
    });
    assert.throws(
      () =>
        svc.acquire({
          operationId: "op-12",
          requirements: { userCredits: 3 },
          attributes: GOOD_ATTRS,
          policyVersion: 1,
        }),
      OperationConflictError,
    );
    assert.equal(svc.snapshot().userCredits?.used, 2);
  });

  it("does not deduct again when a closed permit id is replayed", () => {
    const svc = makeService();
    svc.acquire({
      operationId: "op-13",
      requirements: { userCredits: 5 },
      attributes: GOOD_ATTRS,
      policyVersion: 1,
    });
    svc.cancel("op-13");
    const replay = svc.acquire({
      operationId: "op-13",
      requirements: { userCredits: 5 },
      attributes: GOOD_ATTRS,
      policyVersion: 1,
    });
    assert.equal(replay.status, "cancelled");
    assert.equal(svc.snapshot().userCredits?.used, 0);
  });
});

describe("cancel and failure rollback", () => {
  it("cancel releases every dimension", () => {
    const svc = makeService();
    svc.acquire({
      operationId: "op-20",
      requirements: { userCredits: 7, gpuHours: 3 },
      attributes: GOOD_ATTRS,
      policyVersion: 1,
    });
    const permit = svc.cancel("op-20");
    assert.equal(permit.status, "cancelled");
    assert.deepEqual(svc.snapshot(), {
      userCredits: { capacity: 10, used: 0 },
      gpuHours: { capacity: 4, used: 0 },
    });
  });

  it("fail releases every dimension", () => {
    const svc = makeService();
    svc.acquire({
      operationId: "op-21",
      requirements: { userCredits: 2, gpuHours: 2 },
      attributes: GOOD_ATTRS,
      policyVersion: 1,
    });
    const permit = svc.fail("op-21");
    assert.equal(permit.status, "failed");
    assert.deepEqual(svc.snapshot(), {
      userCredits: { capacity: 10, used: 0 },
      gpuHours: { capacity: 4, used: 0 },
    });
  });

  it("rejects closing an unknown or already-closed permit", () => {
    const svc = makeService();
    assert.throws(() => svc.cancel("nope"), UnknownOperationError);
    svc.acquire({
      operationId: "op-22",
      requirements: { userCredits: 1 },
      attributes: GOOD_ATTRS,
      policyVersion: 1,
    });
    svc.cancel("op-22");
    assert.throws(() => svc.cancel("op-22"), UnknownOperationError);
    assert.throws(() => svc.fail("op-22"), UnknownOperationError);
  });
});

describe("versioned read-only policies", () => {
  it("rejects permits submitted against an old policy version", () => {
    const svc = makeService();
    svc.setPolicy({
      version: 2,
      condition: { op: "in", attr: "tier", set: ["enterprise"] },
    });
    assert.throws(
      () =>
        svc.acquire({
          operationId: "op-30",
          requirements: { userCredits: 1 },
          attributes: { tier: "enterprise" },
          policyVersion: 1,
        }),
      (err: unknown) => {
        assert.ok(err instanceof StalePolicyError);
        assert.match(err.message, /version 1.*current version is 2/);
        return true;
      },
    );
    assert.equal(svc.snapshot().userCredits?.used, 0);
  });

  it("accepts permits at the current version and enforces the new rules", () => {
    const svc = makeService();
    svc.setPolicy({
      version: 2,
      condition: { op: "in", attr: "tier", set: ["enterprise"] },
    });
    assert.throws(
      () =>
        svc.acquire({
          operationId: "op-31",
          requirements: { userCredits: 1 },
          attributes: { tier: "pro" },
          policyVersion: 2,
        }),
      PolicyViolationError,
    );
    const permit = svc.acquire({
      operationId: "op-32",
      requirements: { userCredits: 1 },
      attributes: { tier: "enterprise" },
      policyVersion: 2,
    });
    assert.equal(permit.policyVersion, 2);
  });

  it("refuses non-increasing policy versions", () => {
    const svc = makeService();
    assert.throws(
      () => svc.setPolicy({ version: 1, condition: { op: "in", attr: "tier", set: ["pro"] } }),
      RangeError,
    );
  });

  it("deep-freezes policies so callers cannot mutate them", () => {
    const policy = definePolicy(POLICY_V1);
    assert.ok(Object.isFrozen(policy));
    assert.ok(Object.isFrozen(policy.condition));
    const andNode = policy.condition as { conditions: unknown };
    assert.ok(Object.isFrozen(andNode.conditions));
    assert.throws(() => {
      (policy as { version: number }).version = 99;
    }, TypeError);
  });

  it("evaluates finite sets, integer conditions, and boolean combinators", () => {
    const policy = definePolicy(POLICY_V1);
    assert.equal(evaluatePolicy(policy, GOOD_ATTRS), true);
    assert.equal(evaluatePolicy(policy, { ...GOOD_ATTRS, tier: "free" }), false);
    assert.equal(evaluatePolicy(policy, { ...GOOD_ATTRS, ageDays: 29 }), false);
    assert.equal(evaluatePolicy(policy, { ...GOOD_ATTRS, riskLevel: 3 }), false);
    // Non-integer values never satisfy integer conditions.
    assert.equal(evaluatePolicy(policy, { ...GOOD_ATTRS, ageDays: 30.5 }), false);
    // Missing attributes fail closed.
    assert.equal(evaluatePolicy(policy, {}), false);
  });

  it("reads only explicitly supplied attributes (no external identity lookups)", () => {
    const policy = definePolicy({
      version: 1,
      condition: { op: "eq", attr: "clearance", value: 2 },
    });
    // The engine has no way to resolve "clearance" itself: absent => denied.
    assert.equal(evaluatePolicy(policy, {}), false);
    assert.equal(evaluatePolicy(policy, { clearance: 2 }), true);
  });

  it("validates the restricted condition language", () => {
    assert.throws(
      () => definePolicy({ version: 1, condition: { op: "regex", attr: "x" } as never }),
      TypeError,
    );
    assert.throws(
      () =>
        definePolicy({
          version: 1,
          condition: { op: "gte", attr: "x", value: 1.5 },
        }),
      TypeError,
    );
    assert.throws(
      () => definePolicy({ version: 1, condition: { op: "in", attr: "x", set: [] } }),
      TypeError,
    );
  });
});

describe("ledger invariants", () => {
  it("never lets usage go negative on release", () => {
    const ledger = new QuotaLedger({ a: 5 });
    ledger.deduct({ a: 2 });
    assert.throws(() => ledger.release({ a: 3 }), RangeError);
  });

  it("rejects empty or non-positive requirements", () => {
    const ledger = new QuotaLedger({ a: 5 });
    assert.throws(() => ledger.deduct({}), TypeError);
    assert.throws(() => ledger.deduct({ a: 0 }), TypeError);
    assert.throws(() => ledger.deduct({ a: 1.5 }), TypeError);
  });
});
