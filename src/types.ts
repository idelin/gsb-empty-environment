import type { RangeSet, ScalarDeviceId, RangeInput } from "./interval.ts";

export type { RangeSet, RangeInput, ScalarDeviceId } from "./interval.ts";

export type Generation = number;

export interface CapabilityInput {
  [key: string]: unknown;
  capabilityId?: string;
  keyId?: string;
  groupId?: string;
  tenant?: string;
  tenantId?: string;
  contentGeneration?: number | string;
  contentGen?: number | string;
  keyGeneration?: number | string;
  keyGen?: number | string;
  generation?: number | string;
  purpose?: string;
  usage?: string;
  interval?: RangeInput;
  deviceInterval?: RangeInput;
  range?: RangeInput;
}

export interface EncapsulationGroupInput {
  [key: string]: unknown;
  groupId?: string;
  id?: string;
  tenant?: string;
  tenantId?: string;
  contentGeneration?: number | string;
  contentGen?: number | string;
  keyGeneration?: number | string;
  keyGen?: number | string;
  purpose?: string;
  usage?: string;
  interval?: RangeInput;
  deviceInterval?: RangeInput;
  range?: RangeInput;
}

export interface DeviceInput {
  [key: string]: unknown;
  id?: ScalarDeviceId;
  deviceId?: ScalarDeviceId;
  physicalId?: string;
  physicalDeviceId?: string;
  identity?: string;
  aliases?: readonly (ScalarDeviceId | string)[];
  capabilities?: readonly CapabilityInput[];
  keys?: readonly CapabilityInput[];
  groups?: readonly (string | EncapsulationGroupInput)[];
  revoked?: boolean;
  label?: string;
  className?: string;
  class?: string;
}

export interface DeviceClassInput {
  [key: string]: unknown;
  name?: string;
  className?: string;
  label?: string;
  members?: readonly (string | ScalarDeviceId | DeviceInput)[];
  devices?: readonly DeviceInput[];
}

export interface BridgeDeclarationInput {
  [key: string]: unknown;
  bridgeId?: string;
  id?: string;
  tenant?: string;
  tenantId?: string;
  contentGeneration?: number | string;
  contentGen?: number | string;
  /** Generation of the old capability that may be bridged. */
  keyGeneration?: number | string;
  keyGen?: number | string;
  generation?: number | string;
  purpose?: string;
  usage?: string;
  interval?: RangeInput;
  deviceInterval?: RangeInput;
  range?: RangeInput;
  /** Target content generation of the round, when the declaration binds one. */
  round?: number | string;
  roundGeneration?: number | string;
  signature?: string | { value?: string; algorithm?: string; [key: string]: unknown };
  signed?: boolean;

  // Explicitly forbidden shortcuts.  These are surfaced in the audit rather
  // than silently treated as a scope.
  name?: string;
  deviceName?: string;
  membership?: unknown;
  members?: unknown;
  member?: unknown;
  groupName?: string;
}

export interface MessageRequirementInput {
  [key: string]: unknown;
  tenant?: string;
  tenantId?: string;
  contentGeneration?: number | string;
  contentGen?: number | string;
  keyGeneration?: number | string;
  keyGen?: number | string;
  purpose?: string;
  usage?: string;
  threshold?: number;
  requiredShares?: number;
  requiredSources?: number;
  shares?: number;
  interval?: RangeInput;
  recipients?: RangeInput;
  deviceInterval?: RangeInput;
}

export interface CoalitionInput {
  [key: string]: unknown;
  coalitionId?: string;
  id?: string;
  name?: string;
  members?: readonly (string | ScalarDeviceId)[];
  devices?: readonly (string | ScalarDeviceId)[];
}

export interface AcceptancePolicy {
  [key: string]: unknown;
  domainBits?: number;
  groups?: readonly EncapsulationGroupInput[];
  encapsulationGroups?: readonly EncapsulationGroupInput[];
  devices?: readonly DeviceInput[];
  deviceClasses?: readonly DeviceClassInput[];
  bridges?: readonly BridgeDeclarationInput[];
  bridgeDeclarations?: readonly BridgeDeclarationInput[];
  revoked?: RangeInput;
  revocation?: RangeInput;
  revokedIntervals?: RangeInput;
  message?: MessageRequirementInput;
  requirement?: MessageRequirementInput;
  threshold?: number;

  /** HMAC-SHA-256 authority key.  Never copied into a result or certificate. */
  bridgeSecret?: string;
  /** Alias accepted for the same HMAC authority key. */
  authorityKey?: string;
  /** Optional Ed25519 public key (PEM or KeyObject). */
  bridgePublicKey?: unknown;
  /** Full replacement verifier; receives the canonical signed payload. */
  verifyBridgeSignature?: (declaration: NormalizedBridge, payload: string) => boolean;
  /** Explicitly trusted bridge ids (useful for independently replayed manifests). */
  trustedBridgeIds?: readonly string[];
  /** Test/replay escape hatch: treat a present signature/signed flag as valid. */
  assumeSignedBridges?: boolean;

  allowedCoalitions?: readonly CoalitionInput[];
  coalitions?: readonly CoalitionInput[];
  maxCoalitionSize?: number;
  enumerateRevokedCoalitions?: boolean;

  /** Maximum ids that may be materialised by evaluateDomain. Default 65536. */
  exhaustiveLimit?: number;
  /** Derive capabilities from covering groups when a device declares none. */
  deriveCapabilities?: boolean;
}

export interface NormalizedGroup {
  readonly groupId: string;
  readonly tenant: string;
  readonly contentGeneration: Generation;
  readonly keyGeneration: Generation;
  readonly purpose: string;
  readonly interval: RangeSet;
}

export interface NormalizedCapability {
  readonly capabilityId: string;
  readonly owner: string;
  readonly groupId?: string;
  readonly tenant: string;
  readonly contentGeneration: Generation;
  readonly keyGeneration: Generation;
  readonly purpose: string;
  readonly interval: RangeSet;
}

export interface NormalizedDevice {
  readonly key: string;
  readonly ids: readonly bigint[];
  readonly aliases: readonly string[];
  readonly label?: string;
  readonly className?: string;
  readonly capabilities: readonly NormalizedCapability[];
  readonly revoked: boolean;
  readonly explicitCapabilities: boolean;
}

export type BridgeRejectionCode =
  | "missing-tenant"
  | "missing-interval"
  | "missing-purpose"
  | "missing-generation"
  | "name-or-membership-shortcut"
  | "wildcard-scope"
  | "unsigned"
  | "bad-signature"
  | "not-old"
  | "duplicate-bridge-id";

export interface NormalizedBridge {
  readonly bridgeId: string;
  readonly tenant: string | null;
  readonly contentGeneration: Generation | null;
  readonly keyGeneration: Generation | null;
  readonly purpose: string | null;
  readonly interval: RangeSet;
  readonly signature: string | null;
  readonly signedFlag: boolean;
  readonly shortcutFields: readonly string[];
  readonly tenantInferred: boolean;
  readonly roundUnbound: boolean;
}

export interface BridgeAudit {
  readonly bridge: NormalizedBridge;
  readonly accepted: boolean;
  readonly applicable: boolean;
  readonly rejection?: { readonly code: BridgeRejectionCode; readonly detail: string };
  readonly payload: string;
}

export interface NormalizedMessage {
  readonly tenant: string;
  readonly contentGeneration: Generation;
  readonly keyGeneration: Generation;
  readonly purpose: string;
  readonly threshold: number;
  readonly interval: RangeSet | null;
}

export interface NormalizedCoalition {
  readonly coalitionId: string;
  readonly memberRefs: readonly string[];
}

export interface NormalizedModel {
  readonly domainBits: 48;
  readonly groups: readonly NormalizedGroup[];
  readonly devices: readonly NormalizedDevice[];
  readonly bridges: readonly NormalizedBridge[];
  readonly revocation: RangeSet;
  readonly message: NormalizedMessage;
  readonly allowedCoalitions: readonly NormalizedCoalition[];
  readonly aliasToDevice: ReadonlyMap<string, string>;
  readonly deviceById: ReadonlyMap<string, NormalizedDevice>;
  readonly aliasConflicts: readonly string[];
  readonly deriveCapabilities: boolean;
}

export type AuthorizationClass = "authorized" | "bridged" | "partial" | "revoked" | "denied";
export type RiskLevel = "safe" | "elevated" | "unsafe";
export type RiskFindingLevel = "info" | "elevated" | "unsafe";

export interface RiskFinding {
  readonly code: string;
  readonly level: RiskFindingLevel;
  readonly detail: string;
}

export interface RiskVerdict {
  readonly level: RiskLevel;
  readonly safe: boolean;
  readonly revokedDirectRead: boolean;
  readonly revokedCoalitionEscalation: boolean;
  readonly revokedBridgeRead: boolean;
  readonly bridgeOverreach: boolean;
  readonly unsignedBridge: boolean;
  readonly aliasDoubleCount: boolean;
  readonly generationConfusion: boolean;
  readonly boundaryLeak: boolean;
  readonly findings: readonly RiskFinding[];
}

export interface DeviceDecision {
  readonly device: string;
  readonly ids: readonly string[];
  readonly label?: string;
  readonly className?: string;
  readonly revoked: boolean;
  readonly direct: boolean;
  readonly bridged: boolean;
  readonly eligible: boolean;
  readonly alone: boolean;
  readonly distinctSources: number;
  readonly authorization: AuthorizationClass;
  readonly risk: {
    readonly revokedRead: boolean;
    readonly boundaryAdjacent: boolean;
  };
}

export interface ClassDecision {
  readonly name: string;
  readonly members: readonly string[];
  readonly authorization: AuthorizationClass;
  readonly canReadTogether: boolean;
  readonly allMembersAlone: boolean;
  readonly someMemberAlone: boolean;
  readonly distinctEligibleSources: number;
}

export interface CoalitionDecision {
  readonly coalitionId: string;
  readonly members: readonly string[];
  readonly rawMembers: readonly string[];
  readonly distinctPhysicalSources: number;
  readonly eligibleSources: number;
  readonly threshold: number;
  readonly canRead: boolean;
  readonly individualMemberCouldRead: boolean;
  readonly newReadRights: boolean;
  readonly aliasCollapsed: boolean;
  readonly onlyRevoked: boolean;
  readonly revokedRisk: boolean;
}

export interface PartitionCell {
  readonly start: string;
  readonly end: string;
  readonly revoked: boolean;
  readonly directGroups: readonly string[];
  readonly directCapabilities: readonly string[];
  readonly bridgedCapabilities: readonly string[];
  readonly devices: readonly string[];
  readonly authorization: AuthorizationClass;
}

export interface AcceptanceManifest {
  readonly domainBits: 48;
  readonly groups: readonly {
    readonly groupId: string;
    readonly tenant: string;
    readonly contentGeneration: number;
    readonly keyGeneration: number;
    readonly purpose: string;
    readonly interval: readonly [string, string][];
  }[];
  readonly devices: readonly {
    readonly key: string;
    readonly ids: readonly string[];
    readonly aliases: readonly string[];
    readonly label?: string;
    readonly className?: string;
    readonly revoked: boolean;
    readonly capabilities: readonly {
      readonly capabilityId: string;
      readonly groupId?: string;
      readonly tenant: string;
      readonly contentGeneration: number;
      readonly keyGeneration: number;
      readonly purpose: string;
      readonly interval: readonly [string, string][];
    }[];
  }[];
  readonly bridges: readonly {
    readonly bridgeId: string;
    readonly tenant: string | null;
    readonly contentGeneration: number | null;
    readonly keyGeneration: number | null;
    readonly purpose: string | null;
    readonly interval: readonly [string, string][];
    readonly signature: string | null;
    readonly signedFlag: boolean;
    readonly shortcutFields: readonly string[];
    readonly tenantInferred: boolean;
    readonly roundUnbound: boolean;
  }[];
  readonly revocation: readonly [string, string][];
  readonly message: {
    readonly tenant: string;
    readonly contentGeneration: number;
    readonly keyGeneration: number;
    readonly purpose: string;
    readonly threshold: number;
    readonly interval: readonly [string, string][] | null;
  };
  readonly allowedCoalitions: readonly {
    readonly coalitionId: string;
    readonly memberRefs: readonly string[];
  }[];
}

export interface AcceptanceCertificate {
  readonly scheme: "gsb-header-acceptance/v1";
  readonly domainBits: 48;
  readonly policyDigest: string;
  readonly partitionDigest: string;
  readonly decisionDigest: string;
  readonly riskDigest: string;
  readonly bridgeDigest: string;
  readonly manifest: AcceptanceManifest;
  readonly boundaries: readonly string[];
  readonly coverage: {
    readonly cells: number;
    readonly total: string;
    readonly complete: boolean;
  };
  readonly bridges: {
    readonly accepted: readonly string[];
    readonly rejected: readonly { readonly bridgeId: string; readonly code: BridgeRejectionCode }[];
  };
  readonly boundaryWitnesses: readonly {
    readonly at: string;
    readonly revoked: boolean;
  }[];
}

export interface AcceptanceResult {
  readonly scheme: "gsb-header-acceptance/v1";
  readonly message: NormalizedMessage;
  readonly decisions: readonly DeviceDecision[];
  readonly classes: readonly ClassDecision[];
  readonly coalitions: readonly CoalitionDecision[];
  readonly partitions: readonly PartitionCell[];
  readonly certificate: AcceptanceCertificate;
  readonly risk: RiskVerdict;
}

export interface VerificationResult {
  readonly ok: boolean;
  readonly errors: readonly string[];
}

export interface DomainEvaluationOptions {
  readonly start?: ScalarDeviceId;
  readonly end?: ScalarDeviceId;
  readonly limit?: number;
  readonly includeIds?: boolean;
  readonly comparePartitions?: boolean;
}

export interface IdDecision {
  readonly id: string;
  readonly revoked: boolean;
  readonly direct: boolean;
  readonly bridged: boolean;
  readonly eligible: boolean;
  readonly authorization: AuthorizationClass;
}

export interface DomainEvaluation {
  readonly start: string;
  readonly end: string;
  readonly size: number;
  readonly ids?: readonly IdDecision[];
  readonly summary: Readonly<Record<AuthorizationClass, number>>;
  readonly partitionMismatches: readonly {
    readonly id: string;
    readonly symbol: AuthorizationClass;
    readonly expected: AuthorizationClass;
  }[];
}

export interface ShareInput {
  source?: string | ScalarDeviceId;
  device?: string | ScalarDeviceId;
  deviceId?: ScalarDeviceId;
  alias?: string;
  tenant?: string;
  tenantId?: string;
  contentGeneration?: number | string;
  contentGen?: number | string;
  keyGeneration?: number | string;
  keyGen?: number | string;
  purpose?: string;
  usage?: string;
  interval?: RangeInput;
  deviceInterval?: RangeInput;
  groupId?: string;
  bridgeId?: string;
}

export interface ShareEvaluation {
  readonly threshold: number;
  readonly rawShares: number;
  readonly distinctSources: number;
  readonly eligibleSources: number;
  readonly canOpen: boolean;
  readonly aliasCollapsed: boolean;
  readonly sources: readonly {
    readonly source: string;
    readonly eligible: boolean;
    readonly direct: boolean;
    readonly bridged: boolean;
    readonly revoked: boolean;
  }[];
}
