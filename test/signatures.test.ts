import test from "node:test";
import assert from "node:assert/strict";
import { generateKeyPairSync, sign } from "node:crypto";
import {
  analyzeAuthorizations,
  completenessPayload,
  deviceKey,
  earlyReturnPayload,
  ed25519Verifier,
  leaseDigest,
  leasePayload,
  type DeviceIdentity,
  type EarlyReturn,
  type Lease,
  type CompletenessProof,
} from "../index.ts";

test("Ed25519 lease and return signatures bind the authorization graph", () => {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const pem = publicKey.export({ type: "spki", format: "pem" }).toString();
  const device: DeviceIdentity = { id: "signed-device", generation: 3, publicKey: pem };
  const lease: Lease = {
    id: "signed-lease",
    device,
    resources: [{ prefix: "vault" }],
    term: { start: 0, end: 10 },
  };
  lease.signature = sign(null, Buffer.from(leasePayload(lease)), privateKey).toString("base64");
  const earlyReturn: EarlyReturn = {
    id: "signed-return",
    leaseId: lease.id,
    device,
    returnedAt: 4,
  };
  earlyReturn.signature = sign(
    null,
    Buffer.from(earlyReturnPayload(earlyReturn, leaseDigest(lease))),
    privateKey,
  ).toString("base64");

  const verifier = ed25519Verifier({ [deviceKey(device)]: pem });
  const result = analyzeAuthorizations({
    leases: [lease],
    returns: [earlyReturn],
    completenessProofs: [],
    queries: [{ prefix: "vault" }],
    now: 5,
    verifier,
    requireSignatures: true,
  });
  assert.equal(result.decisions[0].safeFrom.time, "10");
  assert.equal(result.ignoredReturns.length, 0);

  const proof: CompletenessProof = {
    id: "signed-proof",
    leaseId: lease.id,
    device,
    at: 4,
    descendants: [],
  };
  proof.signature = sign(
    null,
    Buffer.from(completenessPayload(proof, [])),
    privateKey,
  ).toString("base64");
  const proved = analyzeAuthorizations({
    leases: [lease],
    returns: [earlyReturn],
    completenessProofs: [proof],
    queries: [{ prefix: "vault" }],
    now: 5,
    verifier,
    requireSignatures: true,
  });
  assert.equal(proved.decisions[0].safeFrom.time, "4");

  assert.throws(() =>
    analyzeAuthorizations({
      leases: [{ ...lease, signature: "AAAA" }],
      queries: [{ prefix: "vault" }],
      now: 5,
      verifier,
      requireSignatures: true,
    }),
  );
});
