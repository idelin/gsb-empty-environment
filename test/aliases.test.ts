import test from "node:test";
import assert from "node:assert/strict";
import { evaluateTrust, verifyDomainCertificate } from "../src/index.ts";
import { T0 } from "./helpers.ts";

test("common host field aliases are accepted", () => {
  const snapshot = {
    keys: [{ material: "R" }, { material: "T" }],
    userIds: [{ id: "A <a@example.com>", email: "a@example.com", key: "T" }],
    trustSignatures: [
      {
        id: "e1",
        sender: "R",
        receiver: "T",
        issued: T0,
        validUntil: T0 + 10_000,
        maxDepth: 0,
        trustWeight: 7,
        domainRegex: "example\\.com",
      },
    ],
    trustAnchors: ["R"],
  };
  const evaluation = evaluateTrust(snapshot, {
    target: "a@example.com",
    at: T0 + 1,
    threshold: 7,
  });
  assert.equal(evaluation.status, "trusted");
  assert.equal(evaluation.maxWeight, 7);
  assert.equal(verifyDomainCertificate(snapshot, evaluation.certificates[0]).ok, true);
});
