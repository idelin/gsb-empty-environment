import assert from "node:assert/strict";
import test from "node:test";

import {
  DEVICE_ID_MAX,
  analyzeAcceptance,
  emptyRange,
  evaluateDomain,
  evaluateShares,
  exhaustiveCheck,
  normalizeRangeSet,
  rangeContains,
  rangeSize,
  recomputeAcceptance,
  signBridge,
  verifyAcceptance,
  verifyCertificate,
} from "../src/index.ts";

const SECRET = "test-bridge-authority-secret";

function group(
  groupId: string,
  tenant: string,
  contentGeneration: number,
  keyGeneration: number,
  interval: [number, number],
) {
  return {
    groupId,
    tenant,
    contentGeneration,
    keyGeneration,
    purpose: "content",
    interval,
  };
}

function message(
  tenant: string,
  contentGeneration: number,
  keyGeneration: number,
  threshold = 1,
) {
  return { tenant, contentGeneration, keyGeneration, purpose: "content", threshold };
}

test("symbolic interval algebra handles the whole 48-bit domain without enumeration", () => {
  const sparse = normalizeRangeSet({
    start: 0,
    end: DEVICE_ID_MAX,
    exceptions: [{ start: 10, end: 19 }],
  });
  assert.equal(rangeSize(sparse), 2n ** 48n - 10n);
  assert.equal(rangeContains(sparse, 9n), true);
  assert.equal(rangeContains(sparse, 10n), false);
  assert.equal(rangeContains(sparse, 19n), false);
  assert.equal(rangeContains(sparse, 20n), true);
  assert.equal(rangeContains(sparse, DEVICE_ID_MAX), true);
  assert.deepEqual(emptyRange(), []);
});

test("direct reads are limited to the exact tenant, generation, purpose, and interval", () => {
  const policy = {
    groups: [group("g1", "tenant-a", 1, 1, [0, 100])],
    devices: [
      { id: 5, groups: ["g1"] },
      { id: 50, groups: ["g1"] },
      { id: 101, groups: ["g1"] },
    ],
    message: message("tenant-a", 1, 1),
  };
  const result = analyzeAcceptance(policy);
  assert.deepEqual(
    result.decisions.map((decision) => [decision.ids[0], decision.authorization]),
    [
      ["101", "denied"],
      ["5", "authorized"],
      ["50", "authorized"],
    ],
  );
  assert.equal(result.certificate.coverage.complete, true);
  assert.equal(result.certificate.coverage.total, (2n ** 48n).toString(10));
  assert.equal(verifyAcceptance(result, policy).ok, true);
  assert.equal(verifyAcceptance(result).ok, true);
  assert.equal(verifyAcceptance(result.certificate).ok, true);
  assert.equal(verifyCertificate(result.certificate).ok, true);
});

test("same interval with a different key generation needs a correctly scoped signature", () => {
  const groups = [
    group("old", "tenant-a", 1, 0, [0, 99]),
    group("new", "tenant-a", 1, 1, [0, 99]),
  ];
  const devices = [
    { id: 5, groups: ["old"] },
    { id: 6, groups: ["new"] },
  ];
  const unsigned = {
    bridgeId: "unsigned",
    tenant: "tenant-a",
    contentGeneration: 1,
    keyGeneration: 0,
    purpose: "content",
    interval: [0, 99] as [number, number],
  };
  const unsignedResult = analyzeAcceptance({
    groups,
    devices,
    bridges: [unsigned],
    message: message("tenant-a", 1, 1),
  });
  assert.equal(unsignedResult.decisions.find((decision) => decision.ids[0] === "5")!.authorization, "denied");
  assert.equal(unsignedResult.risk.unsignedBridge, true);

  const signed = signBridge(unsigned, SECRET);
  const signedResult = analyzeAcceptance({
    bridgeSecret: SECRET,
    groups,
    devices,
    bridges: [signed],
    message: message("tenant-a", 1, 1),
  });
  assert.equal(signedResult.decisions.find((decision) => decision.ids[0] === "5")!.authorization, "bridged");
  assert.equal(signedResult.decisions.find((decision) => decision.ids[0] === "6")!.authorization, "authorized");
  assert.equal(signedResult.risk.safe, true);

  const notOld = signBridge({ ...unsigned, bridgeId: "not-old", keyGeneration: 1 }, SECRET);
  const notOldResult = analyzeAcceptance({
    bridgeSecret: SECRET,
    groups,
    devices,
    bridges: [notOld],
    message: message("tenant-a", 1, 1),
  });
  assert.equal(notOldResult.decisions.find((decision) => decision.ids[0] === "5")!.authorization, "denied");
  assert.equal(notOldResult.risk.generationConfusion, true);

  const nameOnly = signBridge(
    { bridgeId: "name-only", tenant: "tenant-a", name: "device-five" },
    SECRET,
  );
  const nameOnlyResult = analyzeAcceptance({
    bridgeSecret: SECRET,
    groups,
    devices,
    bridges: [nameOnly],
    message: message("tenant-a", 1, 1),
  });
  assert.equal(nameOnlyResult.decisions.find((decision) => decision.ids[0] === "5")!.authorization, "denied");
  assert.ok(nameOnlyResult.certificate.bridges.rejected.some((entry) => entry.code === "name-or-membership-shortcut"));
});

test("partial migration stays inside the permanent tenant boundary", () => {
  const policy = {
    groups: [
      group("old", "tenant-old", 0, 0, [0, 99]),
      group("new", "tenant-new", 1, 1, [50, 99]),
    ],
    devices: [
      { id: 10, groups: ["old"] },
      { id: 60, groups: ["new", "old"] },
    ],
    bridges: [
      signBridge(
        {
          bridgeId: "cross-tenant",
          tenant: "tenant-new",
          contentGeneration: 1,
          keyGeneration: 0,
          purpose: "content",
          interval: [0, 99],
        },
        SECRET,
      ),
    ],
    bridgeSecret: SECRET,
    message: message("tenant-new", 1, 1),
  };
  const result = analyzeAcceptance(policy);
  assert.equal(result.decisions.find((decision) => decision.ids[0] === "10")!.authorization, "denied");
  assert.equal(result.decisions.find((decision) => decision.ids[0] === "60")!.authorization, "authorized");
});

test("two aliases of one physical device cannot satisfy a two-source message", () => {
  const policy = {
    groups: [group("g", "tenant-a", 1, 1, [0, 99])],
    devices: [
      { id: 1, physicalId: "phone", aliases: ["left", "right"], groups: ["g"] },
      { id: 2, physicalId: "tablet", groups: ["g"] },
    ],
    message: message("tenant-a", 1, 1, 2),
  };
  const aliases = evaluateShares(policy, [{ source: "left" }, { source: "right" }]);
  assert.equal(aliases.distinctSources, 1);
  assert.equal(aliases.aliasCollapsed, true);
  assert.equal(aliases.canOpen, false);

  const distinct = evaluateShares(policy, [{ source: "left" }, { source: "tablet" }]);
  assert.equal(distinct.distinctSources, 2);
  assert.equal(distinct.canOpen, true);
});

test("revoked coalitions escalate only when distinct physical sources pool shares", () => {
  const base = {
    groups: [group("g", "tenant-a", 1, 1, [0, 99])],
    message: message("tenant-a", 1, 1, 2),
  };
  const twoRevoked = analyzeAcceptance({
    ...base,
    devices: [
      { id: 20, groups: ["g"] },
      { id: 21, groups: ["g"] },
    ],
    revoked: [20, 21],
  });
  assert.equal(twoRevoked.risk.revokedCoalitionEscalation, true);
  assert.equal(twoRevoked.risk.level, "unsafe");
  assert.ok(twoRevoked.coalitions.some((coalition) => coalition.newReadRights));

  const onePhysicalTwoAliases = analyzeAcceptance({
    ...base,
    devices: [{ id: 20, physicalId: "phone", aliases: ["a", "b"], groups: ["g"] }],
    revoked: [20],
    allowedCoalitions: [{ coalitionId: "aliases", members: ["a", "b"] }],
  });
  const aliasCoalition = onePhysicalTwoAliases.coalitions.find(
    (coalition) => coalition.coalitionId === "aliases",
  )!;
  assert.equal(aliasCoalition.aliasCollapsed, true);
  assert.equal(aliasCoalition.distinctPhysicalSources, 1);
  assert.equal(aliasCoalition.revokedRisk, false);
  assert.equal(onePhysicalTwoAliases.risk.revokedCoalitionEscalation, false);
});

test("the device exactly on a revocation boundary is revoked and its neighbour is not", () => {
  const policy = {
    groups: [group("g", "tenant-a", 1, 1, [0, 30])],
    devices: [
      { id: 9, groups: ["g"] },
      { id: 10, groups: ["g"] },
    ],
    revoked: { start: 0, end: 9 },
    message: message("tenant-a", 1, 1),
  };
  const result = analyzeAcceptance(policy);
  assert.equal(result.decisions.find((decision) => decision.ids[0] === "9")!.authorization, "revoked");
  assert.equal(result.decisions.find((decision) => decision.ids[0] === "10")!.authorization, "authorized");
  assert.deepEqual(
    result.certificate.boundaryWitnesses.find((witness) => witness.at === "10"),
    { at: "10", revoked: false },
  );

  const sparse = analyzeAcceptance({
    groups: [group("g", "tenant-a", 1, 1, [0, 30])],
    devices: [9, 10, 19, 20].map((id) => ({ id, groups: ["g"] })),
    revoked: { start: 0, end: DEVICE_ID_MAX, exceptions: [{ start: 10, end: 19 }] },
    message: message("tenant-a", 1, 1),
  });
  assert.deepEqual(
    sparse.decisions.map((decision) => [decision.ids[0], decision.authorization]),
    [
      ["10", "authorized"],
      ["19", "authorized"],
      ["20", "revoked"],
      ["9", "revoked"],
    ],
  );
  assert.equal(sparse.certificate.coverage.complete, true);
});

test("small-domain exhaustion matches symbolic partitions and revoked coalitions", () => {
  const bridge = signBridge(
    {
      bridgeId: "limited",
      tenant: "tenant-a",
      contentGeneration: 1,
      keyGeneration: 0,
      purpose: "content",
      interval: [0, 11],
    },
    SECRET,
  );
  const policy = {
    bridgeSecret: SECRET,
    groups: [
      group("old", "tenant-a", 1, 0, [0, 15]),
      group("new", "tenant-a", 1, 1, [0, 15]),
    ],
    devices: Array.from({ length: 16 }, (_, id) => ({ id, groups: ["old"] })),
    revoked: [8, 9],
    bridges: [bridge],
    message: message("tenant-a", 1, 1, 2),
  };
  const domain = evaluateDomain(policy, { start: 0, end: 15 });
  assert.equal(domain.partitionMismatches.length, 0);
  assert.deepEqual(domain.summary, {
    authorized: 0,
    bridged: 0,
    partial: 10,
    revoked: 2,
    denied: 4,
  });
  const ids = new Map(domain.ids!.map((entry) => [entry.id, entry.authorization]));
  for (let id = 0; id <= 7; id += 1) assert.equal(ids.get(String(id)), "partial");
  for (const id of [8, 9]) assert.equal(ids.get(String(id)), "revoked");
  for (const id of [10, 11]) assert.equal(ids.get(String(id)), "partial");
  for (const id of [12, 13, 14, 15]) assert.equal(ids.get(String(id)), "denied");

  const exhaustive = exhaustiveCheck(policy, { start: 0, end: 15, coalitionMaxSize: 2 });
  assert.equal(exhaustive.ok, true);
  assert.deepEqual(exhaustive.escalationCases.map((entry) => entry.members), [["8", "9"]]);
  assert.throws(() => evaluateDomain(policy, { start: 0, end: 1_000_000 }), /refusing to enumerate/);
});

test("certificates detect tampering and can be independently recomputed", () => {
  const policy = {
    groups: [group("g", "tenant-a", 1, 1, [0, 100])],
    devices: [{ id: 5, groups: ["g"] }, { id: 50, groups: ["g"] }],
    message: message("tenant-a", 1, 1),
  };
  const result = analyzeAcceptance(policy);
  assert.equal(verifyAcceptance(result, policy).ok, true);
  assert.equal(verifyAcceptance(policy, result).ok, true);
  assert.equal(verifyAcceptance(result).ok, true);
  const recomputed = recomputeAcceptance(result);
  assert.equal(recomputed.certificate.partitionDigest, result.certificate.partitionDigest);
  assert.equal(recomputed.certificate.decisionDigest, result.certificate.decisionDigest);

  const tampered = JSON.parse(JSON.stringify(result));
  tampered.partitions[0].authorization = "denied";
  assert.equal(verifyAcceptance(tampered).ok, false);

  const manifestTampered = JSON.parse(JSON.stringify(result));
  manifestTampered.certificate.manifest.message.threshold = 2;
  assert.equal(verifyAcceptance(manifestTampered).ok, false);

  const serialized = JSON.stringify(result);
  assert.equal(serialized.includes(SECRET), false);
});

test("input ordering does not change the certificate digest", () => {
  const groups = [
    group("g1", "tenant-a", 1, 1, [0, 50]),
    group("g2", "tenant-a", 1, 1, [51, 100]),
  ];
  const devices = [
    { id: 5, groups: ["g1"] },
    { id: 75, groups: ["g2"] },
  ];
  const first = analyzeAcceptance({
    groups,
    devices,
    message: message("tenant-a", 1, 1),
  });
  const second = analyzeAcceptance({
    groups: [...groups].reverse(),
    devices: [...devices].reverse(),
    message: message("tenant-a", 1, 1),
  });
  assert.equal(first.certificate.policyDigest, second.certificate.policyDigest);
  assert.equal(first.certificate.partitionDigest, second.certificate.partitionDigest);
});

test("a message recipient interval and a tampered bridge signature are enforced", () => {
  const scoped = analyzeAcceptance({
    groups: [group("g", "tenant-a", 1, 1, [0, 99])],
    devices: [{ id: 10, groups: ["g"] }, { id: 60, groups: ["g"] }],
    message: { ...message("tenant-a", 1, 1), interval: [50, 99] },
  });
  assert.equal(scoped.decisions.find((decision) => decision.ids[0] === "10")!.authorization, "denied");
  assert.equal(scoped.decisions.find((decision) => decision.ids[0] === "60")!.authorization, "authorized");

  const signed = signBridge(
    {
      bridgeId: "b",
      tenant: "tenant-a",
      contentGeneration: 1,
      keyGeneration: 0,
      purpose: "content",
      interval: [0, 99],
    },
    SECRET,
  );
  const tampered = analyzeAcceptance({
    bridgeSecret: SECRET,
    groups: [group("old", "tenant-a", 1, 0, [0, 99])],
    devices: [{ id: 10, groups: ["old"] }],
    bridges: [{ ...signed, interval: [0, 49] }],
    message: message("tenant-a", 1, 1),
  });
  assert.equal(tampered.decisions[0]!.authorization, "denied");
  assert.ok(tampered.certificate.bridges.rejected.some((entry) => entry.code === "bad-signature"));
});

test("a sparse revoked domain is reasoned about symbolically when no devices are enumerated", () => {
  const result = analyzeAcceptance({
    groups: [group("g", "tenant-a", 1, 1, [0, DEVICE_ID_MAX])],
    revoked: {
      start: 0,
      end: DEVICE_ID_MAX,
      exceptions: [{ start: 10, end: 19 }],
    },
    message: message("tenant-a", 1, 1, 2),
  });
  assert.equal(result.risk.revokedCoalitionEscalation, true);
  assert.equal(result.risk.level, "unsafe");
  assert.ok(result.risk.findings.some((finding) => finding.code === "symbolic-revoked-coalition"));
});
