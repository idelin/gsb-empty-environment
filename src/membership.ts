import { canonicalize, sha256Hex } from "./canonical.ts";
import { signPayload, verifyPayload, verifyTenantRoot } from "./signature.ts";
import { toEpochMs } from "./time.ts";
import type {
  AssetRootClaims,
  MembershipProof,
  MerkleSibling,
  ResourceIdentity,
  SignedAssetRoot,
  SignedTenantRoot,
  TrustOptions,
} from "./types.ts";

export function resourceIdentityDigest(identity: ResourceIdentity): string {
  return sha256Hex({
    tenantRoot: {
      kind: identity.tenantRoot.kind,
      tenantId: identity.tenantRoot.tenantId,
      rootId: identity.tenantRoot.rootId,
      publicKey: identity.tenantRoot.publicKey,
      issuedAt: identity.tenantRoot.issuedAt,
      expiresAt: identity.tenantRoot.expiresAt,
      signature: identity.tenantRoot.signature,
    },
    resourceId: identity.resourceId,
    path: identity.path,
    pathVersion: identity.pathVersion,
  });
}

export const hashResourceIdentity = resourceIdentityDigest;

export function resourceIdentityKey(identity: ResourceIdentity): string {
  return canonicalize({
    tenantId: identity.tenantRoot.tenantId,
    rootId: identity.tenantRoot.rootId,
    rootSignature: identity.tenantRoot.signature,
    resourceId: identity.resourceId,
    path: identity.path,
    pathVersion: identity.pathVersion,
  });
}

function hashPair(left: string, right: string): string {
  return sha256Hex(`${left}:${right}`);
}

export interface MerkleTree {
  levels: string[][];
  root: string;
}

export function buildMerkleTree(leaves: readonly string[]): MerkleTree {
  if (leaves.length === 0) {
    const root = sha256Hex("empty-asset-set");
    return { levels: [[root]], root };
  }
  const levels: string[][] = [[...leaves]];
  while ((levels[levels.length - 1]?.length ?? 0) > 1) {
    const current = levels[levels.length - 1] ?? [];
    const next: string[] = [];
    for (let index = 0; index < current.length; index += 2) {
      const left = current[index];
      const right = current[index + 1] ?? left;
      next.push(hashPair(left, right));
    }
    levels.push(next);
  }
  return { levels, root: levels[levels.length - 1]?.[0] ?? sha256Hex("empty-asset-set") };
}

export function createMembershipProof(
  identities: readonly ResourceIdentity[],
  identity: ResourceIdentity,
  setId: string,
): MembershipProof {
  const leaves = [...new Set(identities.map(resourceIdentityDigest))].sort();
  const leaf = resourceIdentityDigest(identity);
  let index = leaves.indexOf(leaf);
  if (index < 0) {
    throw new Error(`Identity ${identity.resourceId} is not in asset set ${setId}`);
  }
  const tree = buildMerkleTree(leaves);
  const siblings: MerkleSibling[] = [];
  for (let levelIndex = 0; levelIndex < tree.levels.length - 1; levelIndex += 1) {
    const level = tree.levels[levelIndex] ?? [];
    const siblingIndex = index % 2 === 0 ? index + 1 : index - 1;
    const sibling = level[siblingIndex] ?? level[index];
    siblings.push({
      hash: sibling,
      side: index % 2 === 0 ? "right" : "left",
    });
    index = Math.floor(index / 2);
  }
  return {
    setId,
    assetHash: leaf,
    leaf,
    siblings,
    root: tree.root,
  };
}

export function computeProofRoot(proof: MembershipProof): string {
  let current = proof.leaf;
  for (const sibling of proof.siblings) {
    current =
      sibling.side === "left"
        ? hashPair(sibling.hash, current)
        : hashPair(current, sibling.hash);
  }
  return current;
}

export function createAssetRoot(options: {
  tenantRoot: SignedTenantRoot;
  tenantPrivateKey: string;
  identities: readonly ResourceIdentity[];
  setId: string;
  issuedAt?: number;
  expiresAt?: number;
}): SignedAssetRoot {
  const leaves = [...new Set(options.identities.map(resourceIdentityDigest))].sort();
  const claims: AssetRootClaims = {
    kind: "asset-set-root",
    tenantId: options.tenantRoot.tenantId,
    tenantRootId: options.tenantRoot.rootId,
    setId: options.setId,
    membersRoot: buildMerkleTree(leaves).root,
    issuedAt: options.issuedAt ?? 0,
    expiresAt: options.expiresAt,
  };
  return {
    ...claims,
    signature: signPayload(options.tenantPrivateKey, claims),
  };
}

export function verifyAssetRoot(
  assetRoot: SignedAssetRoot,
  tenantRoot: SignedTenantRoot,
  options: { at?: number | string | Date; trust?: TrustOptions } = {},
): { ok: boolean; reason?: "invalid-asset-root" | "expired-asset-root" | "invalid-tenant-root" } {
  const rootCheck = verifyTenantRoot(tenantRoot, {
    at: options.at,
    trust: options.trust,
  });
  if (!rootCheck.ok) {
    return { ok: false, reason: "invalid-tenant-root" };
  }
  const claims: AssetRootClaims = {
    kind: "asset-set-root",
    tenantId: assetRoot.tenantId,
    tenantRootId: assetRoot.tenantRootId,
    setId: assetRoot.setId,
    membersRoot: assetRoot.membersRoot,
    issuedAt: assetRoot.issuedAt,
    expiresAt: assetRoot.expiresAt,
  };
  if (!verifySignature(tenantRoot.publicKey, claims, assetRoot.signature)) {
    return { ok: false, reason: "invalid-asset-root" };
  }
  if (
    assetRoot.tenantId !== tenantRoot.tenantId ||
    assetRoot.tenantRootId !== tenantRoot.rootId
  ) {
    return { ok: false, reason: "invalid-asset-root" };
  }
  if (
    assetRoot.expiresAt !== undefined &&
    options.at !== undefined &&
    toEpochMs(options.at) >= assetRoot.expiresAt
  ) {
    return { ok: false, reason: "expired-asset-root" };
  }
  if (options.at !== undefined && toEpochMs(options.at) < assetRoot.issuedAt) {
    return { ok: false, reason: "invalid-asset-root" };
  }
  return { ok: true };
}

function verifySignature(publicKey: string, payload: unknown, signature: string): boolean {
  return verifyPayload(publicKey, payload, signature);
}

export function verifyMembershipProof(
  proof: MembershipProof,
  identity: ResourceIdentity,
  assetRoot: SignedAssetRoot,
  tenantRoot: SignedTenantRoot,
  options: { at?: number | string | Date; trust?: TrustOptions } = {},
): boolean {
  const rootCheck = verifyAssetRoot(assetRoot, tenantRoot, options);
  if (!rootCheck.ok) {
    return false;
  }
  const leaf = resourceIdentityDigest(identity);
  return (
    proof.setId === assetRoot.setId &&
    proof.leaf === leaf &&
    proof.assetHash === leaf &&
    proof.root === assetRoot.membersRoot &&
    computeProofRoot(proof) === assetRoot.membersRoot
  );
}

export function findMembershipProof(
  identity: ResourceIdentity,
  proofs: readonly MembershipProof[] | undefined,
  setId: string,
): MembershipProof | undefined {
  const leaf = resourceIdentityDigest(identity);
  return (proofs ?? []).find(
    (proof) => proof.setId === setId && proof.leaf === leaf && proof.assetHash === leaf,
  );
}
