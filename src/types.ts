/**
 * Public data model for the migration acceptance package.
 *
 * The package is deliberately transport agnostic: records may arrive from a
 * host, a key agent journal, a vendor certificate chain or a test fixture.
 * Every signed record declares exactly one action.  An action never inherits
 * authority from the record kind or from a different action's payload.
 */

export type Action =
  | "attest"
  | "deactivate-instance"
  | "revoke-key"
  | "accept-transfer"
  | "confirm-migration"
  | "confirm-recovery";

export type EventKind =
  | "attestation"
  | "instance-deactivation"
  | "deactivation"
  | "key-revocation"
  | "revocation"
  | "transfer-acceptance"
  | "transfer"
  | "acceptance"
  | "owner-confirmation"
  | "owner"
  | "recovery-confirmation"
  | "recovery"
  | string;

export type TcbValue = number | string | boolean;

export interface TcbDocument {
  components?: Record<string, TcbValue>;
  versions?: Record<string, TcbValue>;
  version?: Record<string, TcbValue>;
  epoch?: string;
  family?: string;
  namespace?: string;
  [key: string]: unknown;
}

export type TcbVector =
  | TcbDocument
  | Record<string, TcbValue>
  | string
  | Map<string, TcbValue>
  | Array<
      | {
          component?: string;
          name?: string;
          id?: string;
          version?: TcbValue;
          value?: TcbValue;
          [key: string]: unknown;
        }
      | [string, TcbValue]
    >;

export type TcbRule = "monotonic" | "exact" | "ignore" | "incomparable";
export type TcbComparison = "newer" | "equal" | "older" | "incomparable";

export interface TcbCompareOptions {
  rules?: Record<string, TcbRule>;
  requireExactComponentSet?: boolean;
  allowExtraComponents?: boolean;
}

export interface RecoveryPolicy {
  enabled?: boolean;
  deviceThreshold?: number;
  chipThreshold?: number;
  threshold?: number;
  requiredDevices?: number;
  adminDomainThreshold?: number;
  domainThreshold?: number;
  requiredDomains?: number;
  requireBoth?: boolean;
  [key: string]: unknown;
}

export interface MigrationPolicy {
  /** Policy digest that every attestation must bind. */
  digest?: string;
  /** Alias used by some hosts. */
  id?: string;
  workloadId?: string;
  generation?: number;
  baselineTcb?: TcbVector;
  minimumTcb?: TcbVector;
  tcbRules?: Record<string, TcbRule>;
  requireExactComponentSet?: boolean;
  allowExtraComponents?: boolean;
  ownerThreshold?: number;
  ownerDomainThreshold?: number;
  recovery?: RecoveryPolicy;
  allowRecovery?: boolean;
  recoveryThreshold?: number;
  deviceThreshold?: number;
  adminDomainThreshold?: number;
  requireDeactivation?: boolean;
  requireKeyRevocation?: boolean;
  requireOwnerConfirmation?: boolean;
  /** A host supplied closed-device manifest may prove that a device is off. */
  allowClosedDeviceAsDeactivation?: boolean;
  requireOldDeviceClosed?: boolean;
  allowDebug?: boolean;
  nonceUniqueness?: boolean;
  requireSignatures?: boolean;
  expectedNonce?: string;
  [key: string]: unknown;
}

export interface ClosedDevice {
  chipId?: string;
  deviceId?: string;
  physicalChipId?: string;
  bootCount?: number;
  instanceBootCount?: number;
  [key: string]: unknown;
}

export type ClosedDeviceInput = string | ClosedDevice;

export type ClosedDeviceManifest =
  | string
  | ClosedDeviceInput[]
  | Set<string | ClosedDevice>
  | Map<string, unknown>
  | Record<string, unknown>;

export interface SignedRecord {
  id?: string;
  eventId?: string;
  recordId?: string;
  action?: string;
  kind?: EventKind;
  signer?: string;
  signerId?: string;
  issuer?: string;
  author?: string;
  signature?: string;
  sig?: string;
  proof?: string;
  payload?: Record<string, unknown>;
  claims?: Record<string, unknown>;
  attestation?: Record<string, unknown>;
  quote?: Record<string, unknown>;
  evidence?: Record<string, unknown>;
  data?: Record<string, unknown>;
  causalParents?: string[] | string;
  parents?: string[] | string;
  causes?: string[] | string;
  dependsOn?: string[] | string;
  createdAt?: number;
  logicalTime?: number;
  [key: string]: unknown;
}

export type MigrationEvent = SignedRecord;

export type SignatureVerifier = (
  record: SignedRecord,
  canonical: string,
) => boolean;

export interface Lease {
  generation: number;
  workloadId: string;
  runtimeKey: string;
  chipId?: string;
  migrationId?: string;
  dependencies?: string[];
  releasedAt?: number;
}

export interface EvaluateInput {
  workloadId?: string;
  generation?: number;
  events?: MigrationEvent[] | Record<string, MigrationEvent>;
  policy?: MigrationPolicy;
  closedDevices?: ClosedDeviceManifest;
  leases?: Lease[];
  verifier?: SignatureVerifier;
  verify?: SignatureVerifier;
  signatureVerifier?: SignatureVerifier;
  publicKeys?: Record<string, unknown>;
  requireSignatures?: boolean;
  expectedNonce?: string;
  baselineTcb?: TcbVector;
  now?: number;
}

export interface MissingEvidence {
  action: Action | string;
  migrationId?: string;
  reason: string;
  detail?: Record<string, unknown>;
}

export interface ReleaseTarget {
  migrationId: string;
  workloadId: string;
  generation: number;
  chipId: string;
  runtimeKey: string;
  attestationId?: string;
}

export interface MigrationDecision {
  migrationId: string;
  workloadId: string;
  generation: number;
  status: "release" | "wait" | "reject";
  target: ReleaseTarget | null;
  releaseTo: ReleaseTarget | null;
  dependencies: string[];
  minimalDependencies: string[];
  missing: string[];
  missingEvidence: MissingEvidence[];
  reasons: string[];
  blockers: string[];
  invalidEvents: InvalidEvent[];
  conflictingMigrationId?: string;
  evidence?: string[];
  requiredRecords?: string[];
  causalDependencies?: string[];
  dependencyRecords?: MigrationEvent[];
}

export interface InvalidEvent {
  id: string;
  action?: string;
  migrationId?: string;
  reasons: string[];
}

export interface ReleaseDecision extends MigrationDecision {
  release: ReleaseTarget | null;
  targetKey: string | null;
  targetChipId: string | null;
  targetPublicKey: string | null;
  candidateTarget: ReleaseTarget | null;
  candidateTargetKey: string | null;
  releasable: boolean;
  allowed: boolean;
  outcome: "allow" | "pending" | "deny";
  runtimeKey: string | null;
  key: string | null;
  chipId: string | null;
  publicKey: string | null;
  reason: string;
  requiredSignatures: string[];
  requiredRecords: string[];
  evidence: string[];
  migrations: MigrationDecision[];
  rejected: MigrationDecision[];
  waiting: MigrationDecision[];
  released: ReleaseTarget[];
  conflicts: Array<{ migrationId: string; conflictingMigrationId: string }>;
  causalPrefixComplete: boolean;
  missingCausalParents: string[];
  currentHolder: Lease | null;
  currentHolderKey: string | null;
}

export interface EvaluateContext {
  workloadId: string;
  generation: number;
  policy: MigrationPolicy;
  closedDevices: string[];
  closedInstances: Array<{ chipId: string; bootCount: number }>;
  expectedNonce?: string;
  now: number;
}

export interface NormalizedTcb {
  components: Record<string, TcbValue>;
  epoch?: string;
  family?: string;
  namespace?: string;
}

export type Policy = MigrationPolicy;
export type Tcb = TcbVector;
export type Decision = ReleaseDecision;
export type MigrationResult = ReleaseDecision;
export type KeyGenerationDecision = ReleaseDecision;
export type ReleaseResult = ReleaseDecision;
export type Attestation = MigrationEvent;
export type SignedEvent = MigrationEvent;
