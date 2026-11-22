import assert from "node:assert/strict";
import test from "node:test";
import {
  createAssetRoot,
  createMembershipProof,
  createTenantRoot,
  resourceIdentity,
  verifyMembershipProof,
} from "../src/index.ts";

test("membership is bound to tenant root, immutable id, path and path version", () => {
  const tenant = createTenantRoot({ tenantId: "tenant-a", issuedAt: 0 });
  const a = resourceIdentity(tenant.root, "asset-1", ["prod", "a"], 1);
  const b = resourceIdentity(tenant.root, "asset-2", ["prod", "a"], 1);
  const renamed = resourceIdentity(tenant.root, "asset-1", ["prod", "renamed"], 2);
  const assetRoot = createAssetRoot({
    tenantRoot: tenant.root,
    tenantPrivateKey: tenant.privateKey,
    identities: [a, b],
    setId: "issued-set",
    issuedAt: 0,
  });
  const proof = createMembershipProof([a, b], a, "issued-set");

  assert.equal(verifyMembershipProof(proof, a, assetRoot, tenant.root), true);
  assert.equal(verifyMembershipProof(proof, b, assetRoot, tenant.root), false);
  assert.equal(verifyMembershipProof(proof, renamed, assetRoot, tenant.root), false);

  const newAtOldPath = resourceIdentity(tenant.root, "asset-3", ["prod", "a"], 1);
  assert.equal(verifyMembershipProof(proof, newAtOldPath, assetRoot, tenant.root), false);

  const tampered = { ...assetRoot, membersRoot: "0".repeat(64) };
  assert.equal(verifyMembershipProof(proof, a, tampered, tenant.root), false);
});

