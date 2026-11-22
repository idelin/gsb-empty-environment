import { createAssetRoot, createMembershipProof } from "./membership.ts";
import { signWaiver } from "./signature.ts";
import { universalDFA } from "./dfa.ts";
import type {
  AssetSetScope,
  ConstraintDeclaration,
  ConstraintValue,
  InheritanceGraph,
  PolicyNode,
  PolicyState,
  ResourceIdentity,
  SignedAssetRoot,
  SignedPolicySource,
  SignedTenantRoot,
  SignedWaiver,
  TimeInterval,
  WaiverTarget,
} from "./types.ts";

export function resourceIdentity(
  tenantRoot: SignedTenantRoot,
  resourceId: string,
  path: readonly string[],
  pathVersion: number | string,
): ResourceIdentity {
  return { tenantRoot, resourceId, path, pathVersion };
}

export function constraintDeclaration(options: {
  id: string;
  key: string;
  allowed: readonly ConstraintValue[];
  source: SignedPolicySource;
  literal?: string;
  valid?: TimeInterval;
  scope?: ConstraintDeclaration["scope"];
}): ConstraintDeclaration {
  return {
    id: options.id,
    key: options.key,
    allowed: [...new Set(options.allowed)].sort(),
    source: options.source,
    literal: options.literal,
    valid: options.valid,
    scope: options.scope,
  };
}

export const makeConstraint = constraintDeclaration;

export function inheritanceGraph(nodes: readonly PolicyNode[]): InheritanceGraph {
  return { nodes };
}

export function policyState(
  graph: InheritanceGraph,
  at: number | string | Date,
  waivers: readonly SignedWaiver[] = [],
): PolicyState {
  return { graph, waivers, at };
}

export function issueAssetSetScope(options: {
  tenantRoot: SignedTenantRoot;
  tenantPrivateKey: string;
  identities: readonly ResourceIdentity[];
  setId: string;
  path?: AssetSetScope["path"];
  issuedAt?: number;
  expiresAt?: number;
  proofFor?: readonly ResourceIdentity[];
}): AssetSetScope {
  const assets: SignedAssetRoot = createAssetRoot({
    tenantRoot: options.tenantRoot,
    tenantPrivateKey: options.tenantPrivateKey,
    identities: options.identities,
    setId: options.setId,
    issuedAt: options.issuedAt,
    expiresAt: options.expiresAt,
  });
  const proofFor = options.proofFor ?? options.identities;
  return {
    path: options.path ?? universalDFA(),
    assets,
    proofs: proofFor.map((identity) =>
      createMembershipProof(options.identities, identity, options.setId),
    ),
  };
}

export function createWaiver(options: {
  id: string;
  issuer: SignedPolicySource;
  issuerPrivateKey: string;
  target: WaiverTarget;
  scope: AssetSetScope;
  issuedAt: number;
  expiresAt: number;
}): SignedWaiver {
  return signWaiver(
    {
      kind: "waiver",
      id: options.id,
      issuer: options.issuer,
      target: options.target,
      scope: options.scope,
      issuedAt: options.issuedAt,
      expiresAt: options.expiresAt,
    },
    options.issuerPrivateKey,
  );
}
