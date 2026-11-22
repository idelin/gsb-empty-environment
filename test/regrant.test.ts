import test from "node:test";
import assert from "node:assert/strict";
import {
  analyzeAuthorizations,
  analysisToJSON,
  ClockConstraintError,
  decideRegrant,
  leaseDigest,
  rat,
  scopeContains,
  type DeviceClock,
  type DeviceIdentity,
  type Lease,
} from "../index.ts";

const d1: DeviceIdentity = { id: "device-1", generation: 1, name: "shared-name" };
const d2: DeviceIdentity = { id: "device-2", generation: 1, name: "shared-name" };

function lease(
  id: string,
  device: DeviceIdentity,
  start: string | number,
  end: string | number,
  extra: Partial<Lease> = {},
): Lease {
  return {
    id,
    device,
    resources: [{ prefix: "res" }],
    term: { start, end },
    ...extra,
  };
}

test("open and closed term ends produce different safe boundaries", () => {
  const open = decideRegrant({
    leases: [lease("open", d1, 0, 10)],
    resource: { prefix: "res" },
    now: 10,
  });
  assert.equal(open.safeFrom.time, "10");
  assert.equal(open.safeFrom.inclusive, true);
  assert.equal(open.status, "reclaimed");
  assert.equal(open.safeAtNow, true);

  const closedLease = lease("closed", d1, 0, 10);
  closedLease.term.endInclusive = true;
  const closed = decideRegrant({
    leases: [closedLease],
    resource: { prefix: "res" },
    now: 10,
  });
  assert.equal(closed.safeFrom.time, "10");
  assert.equal(closed.safeFrom.inclusive, false);
  assert.equal(closed.status, "definitely-valid");
  assert.equal(closed.safeAtNow, false);
});

test("parent early return does not end a still-valid child lease", () => {
  const parent = lease("P", d1, 0, 10);
  const child = lease("C", d2, 5, 8, {
    parentId: "P",
    parentDigest: leaseDigest(parent),
  });
  const earlyReturn = {
    id: "RP",
    leaseId: "P",
    device: d1,
    returnedAt: 6,
  };

  const withoutProof = decideRegrant({
    leases: [parent, child],
    returns: [earlyReturn],
    resource: { prefix: "res" },
    now: 7,
  });
  assert.equal(withoutProof.status, "definitely-valid");
  assert.equal(withoutProof.safeFrom.time, "10");
  assert.ok(withoutProof.blockers.some((blocker) => blocker.reason === "unseen-subtree"));
  assert.ok(withoutProof.blockers.some((blocker) => blocker.blockerId === "C"));

  const withProof = decideRegrant({
    leases: [parent, child],
    returns: [earlyReturn],
    completenessProofs: [
      {
        id: "proof-P",
        leaseId: "P",
        device: d1,
        at: 6,
        descendants: ["C"],
      },
    ],
    resource: { prefix: "res" },
    now: 7,
  });
  assert.equal(withProof.safeFrom.time, "8");
  assert.equal(withProof.blockers.some((blocker) => blocker.reason === "unseen-subtree"), false);
});

test("partial return is evaluated per symbolic resource cell", () => {
  const parent = lease("P", d1, 0, 10);
  const earlyReturn = {
    id: "partial",
    leaseId: "P",
    device: d1,
    returnedAt: 4,
    resources: [{ prefix: "res/hot" }],
  };

  const result = analyzeAuthorizations({
    leases: [parent],
    returns: [earlyReturn],
    queries: [{ prefix: "res" }],
    now: 5,
  });
  assert.equal(result.decisions.length, 2);
  const hot = result.decisions.find((decision) =>
    scopeContains([decision.resource], "res/hot/key"),
  )!;
  const cold = result.decisions.find((decision) =>
    scopeContains([decision.resource], "res/cold/key"),
  )!;
  assert.equal(hot.safeFrom.time, "10");
  assert.equal(hot.status, "possibly-valid");
  assert.equal(cold.safeFrom.time, "10");
  assert.equal(cold.status, "definitely-valid");

  const withProof = analyzeAuthorizations({
    leases: [parent],
    returns: [earlyReturn],
    completenessProofs: [
      {
        id: "proof",
        leaseId: "P",
        device: d1,
        at: 4,
        descendants: [],
      },
    ],
    queries: [{ prefix: "res" }],
    now: 5,
  });
  const hotProved = withProof.decisions.find((decision) =>
    scopeContains([decision.resource], "res/hot/key"),
  )!;
  assert.equal(hotProved.safeFrom.time, "4");
  assert.equal(hotProved.safeFrom.inclusive, true);
});

test("duplicate display names are distinct; generation is part of identity", () => {
  const left = lease("L", d1, 0, 5);
  const right = lease("R", d2, 0, 9);
  const result = decideRegrant({
    leases: [left, right],
    resource: { prefix: "res" },
    now: 6,
  });
  assert.equal(result.safeFrom.time, "9");
  assert.equal(result.blockers.length, 2);

  const newer: DeviceIdentity = { id: "device-1", generation: 2, name: "shared-name" };
  const ignored = analyzeAuthorizations({
    leases: [lease("old", d1, 0, 10)],
    returns: [
      {
        id: "new-generation-return",
        leaseId: "old",
        device: newer,
        returnedAt: 3,
      },
    ],
    queries: [{ prefix: "res" }],
    now: 4,
  });
  assert.equal(ignored.ignoredReturns.length, 1);
  assert.equal(ignored.decisions[0].safeFrom.time, "10");
});

test("late return after the true term end does not shorten the lease", () => {
  const result = decideRegrant({
    leases: [lease("L", d1, 0, 10)],
    returns: [{ id: "late", leaseId: "L", device: d1, returnedAt: 12 }],
    resource: { prefix: "res" },
    now: 11,
  });
  assert.equal(result.safeFrom.time, "10");
  assert.equal(result.safeFrom.inclusive, true);
  assert.equal(result.status, "reclaimed");
});

test("an offline device is reclaimed at its true term, not assumed expired", () => {
  const result = decideRegrant({
    leases: [lease("offline", d1, 0, "100/3")],
    resource: { prefix: "res" },
    now: 20,
  });
  assert.equal(result.status, "definitely-valid");
  assert.equal(result.safeFrom.time, "100/3");
  assert.equal(result.safeFrom.inclusive, true);
});

test("bounded offset and drift widen possible validity without widening belongs-to", () => {
  const clock: DeviceClock = {
    device: d1,
    offset: { lo: -1, hi: 1 },
    drift: { lo: "-1/10", hi: "1/10" },
  };
  const result = decideRegrant({
    leases: [lease("L", d1, 0, 10)],
    clocks: [clock],
    resource: { prefix: "res" },
    now: 8,
  });
  assert.equal(result.status, "definitely-valid");
  assert.equal(result.blockers[0].definiteUntil?.time, "9");
  assert.equal(result.safeFrom.time, "11");
  assert.equal(result.safeFrom.inclusive, true);
});

test("an empty definite interval does not erase possible validity", () => {
  const clock: DeviceClock = {
    device: d1,
    offset: { lo: 0, hi: 100 },
    drift: { lo: 0, hi: 0 },
  };
  const result = decideRegrant({
    leases: [lease("L", d1, 0, 10)],
    clocks: [clock],
    resource: { prefix: "res" },
    now: 5,
  });
  assert.equal(result.status, "possibly-valid");
  assert.equal(result.safeFrom.time, "10");
  assert.equal(result.blockers[0].definiteFrom, null);
  assert.equal(result.blockers[0].definiteUntil, null);
});

test("observations constrain one coherent clock and reject contradictory data", () => {
  const clock: DeviceClock = {
    device: d1,
    offset: { lo: -2, hi: 2 },
    drift: { lo: 0, hi: 0 },
  };
  const coherent = analyzeAuthorizations({
    leases: [lease("L", d1, 0, 10)],
    clocks: [clock],
    observations: [
      { device: d1, serverTime: 0, deviceTime: { lo: 1, hi: 1 } },
      { device: d1, serverTime: 10, deviceTime: { lo: 11, hi: 11 } },
    ],
    queries: [{ prefix: "res" }],
    now: 8,
  });
  assert.equal(coherent.decisions[0].safeFrom.time, "9");

  assert.throws(
    () =>
      analyzeAuthorizations({
        leases: [lease("L", d1, 0, 10)],
        clocks: [clock],
        observations: [
          { device: d1, serverTime: 0, deviceTime: { lo: 1, hi: 1 } },
          { device: d1, serverTime: 10, deviceTime: { lo: 1, hi: 1 } },
        ],
        queries: [{ prefix: "res" }],
        now: 0,
      }),
    ClockConstraintError,
  );
});

test("drift is enforced across observations, not only per observation", () => {
  const clock: DeviceClock = {
    device: d1,
    offset: { lo: -20, hi: 20 },
    drift: { lo: 0, hi: 0 },
  };
  assert.throws(
    () =>
      analyzeAuthorizations({
        leases: [lease("L", d1, 0, 10)],
        clocks: [clock],
        observations: [
          { device: d1, serverTime: 0, deviceTime: { lo: 0, hi: 0 } },
          { device: d1, serverTime: 10, deviceTime: { lo: 5, hi: 5 } },
        ],
        queries: [{ prefix: "res" }],
      }),
    ClockConstraintError,
  );
});

test("a child cannot outlive the original parent term even if its clock says so", () => {
  const parent = lease("P", d1, 0, 10);
  const child = lease("C", d2, 0, 20, {
    parentId: "P",
    parentDigest: leaseDigest(parent),
  });
  const result = decideRegrant({
    leases: [parent, child],
    resource: { prefix: "res" },
    now: 9,
  });
  assert.equal(result.safeFrom.time, "10");
  assert.deepEqual(result.blockers[0].leaseIds, ["P"]);
  assert.ok(result.blockers.some((blocker) => blocker.leaseIds.join(">") === "P>C"));
});

test("certificates are JSON-readable and carry one coherent witness per device", () => {
  const result = decideRegrant({
    leases: [lease("L", d1, 0, 10)],
    resource: { prefix: "res" },
    now: 5,
  });
  const text = analysisToJSON({
    now: "5",
    decisions: [result],
    ignoredReturns: [],
    ignoredCompletenessProofs: [],
    leases: [],
    notes: [],
  });
  const parsed = JSON.parse(text);
  assert.equal(parsed.decisions[0].safeFrom.time, "10");
  assert.equal(parsed.decisions[0].lowerBoundCertificate.kind, "lower");
  assert.equal(parsed.decisions[0].upperBoundCertificate.kind, "upper");
  const witness = parsed.decisions[0].blockers[0].clockWitnesses[0];
  assert.equal(witness.deviceKey, "device-1#1");
  assert.equal(witness.events.length, 2);
});

test("all times remain rationals", () => {
  const result = decideRegrant({
    leases: [lease("L", d1, "1/7", "22/7")],
    resource: { prefix: "res" },
    now: "11/7",
  });
  assert.equal(result.safeFrom.time, "22/7");
  assert.equal(rat(result.safeFrom.time!).eq("22/7"), true);
});
