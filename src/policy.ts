import {
  asBoolean,
  asNumber,
  asString,
  firstDefined,
  isPlainObject,
} from "./canonical.ts";
import type {
  MigrationPolicy,
  RecoveryPolicy,
  TcbRule,
  TcbVector,
} from "./types.ts";

export interface NormalizedPolicy {
  digest?: string;
  workloadId?: string;
  generation?: number;
  baselineTcb?: MigrationPolicy["baselineTcb"];
  tcbRules: Record<string, TcbRule>;
  requireExactComponentSet: boolean;
  allowExtraComponents: boolean;
  ownerThreshold: number;
  recoveryEnabled: boolean;
  recoveryDeviceThreshold: number;
  recoveryDomainThreshold: number;
  recoveryRequireBoth: boolean;
  requireDeactivation: boolean;
  requireKeyRevocation: boolean;
  requireOwnerConfirmation: boolean;
  allowClosedDeviceAsDeactivation: boolean;
  requireOldDeviceClosed: boolean;
  allowDebug: boolean;
  nonceUniqueness: boolean;
  requireSignatures: boolean;
  expectedNonce?: string;
  raw: MigrationPolicy;
}

function recover(policy: MigrationPolicy): RecoveryPolicy {
  return isPlainObject(policy.recovery)
    ? (policy.recovery as RecoveryPolicy)
    : {};
}

/**
 * Policy aliases are intentionally forgiving.  The security relevant defaults
 * are strict: debug must be off, deactivation/revocation/owner confirmation
 * are required, and closed device manifests do not silently replace a signed
 * deactivation unless the policy says so.
 */
export function normalizePolicy(
  policy: MigrationPolicy | string = {},
): NormalizedPolicy {
  if (typeof policy === "string") policy = { digest: policy };
  const normalizedPolicy: Record<string, unknown> = {
    ...(policy as Record<string, unknown>),
  };
  for (const [key, value] of Object.entries(normalizedPolicy)) {
    const camelKey = key.replace(/[_-]([a-zA-Z0-9])/g, (_, char: string) =>
      char.toUpperCase(),
    );
    if (camelKey !== key && normalizedPolicy[camelKey] === undefined) {
      normalizedPolicy[camelKey] = value;
    }
  }
  policy = normalizedPolicy as MigrationPolicy;
  let recovery = isPlainObject(policy.recovery)
    ? (policy.recovery as RecoveryPolicy)
    : isPlainObject(policy.recoveryPolicy)
      ? (policy.recoveryPolicy as RecoveryPolicy)
      : recover(policy);
  const normalizedRecovery: Record<string, unknown> = {
    ...(recovery as Record<string, unknown>),
  };
  for (const [key, value] of Object.entries(normalizedRecovery)) {
    const camelKey = key.replace(/[_-]([a-zA-Z0-9])/g, (_, char: string) =>
      char.toUpperCase(),
    );
    if (camelKey !== key && normalizedRecovery[camelKey] === undefined) {
      normalizedRecovery[camelKey] = value;
    }
  }
  recovery = normalizedRecovery as RecoveryPolicy;
  const tcbConfig = isPlainObject(policy.tcb) ? policy.tcb : undefined;
  const recoveryEnabled =
    asBoolean(recovery.enabled) ??
    asBoolean(policy.allowRecovery) ??
    (recovery.deviceThreshold !== undefined ||
      recovery.chipThreshold !== undefined ||
      recovery.deviceConfirmationThreshold !== undefined ||
      recovery.threshold !== undefined ||
      recovery.requiredDevices !== undefined ||
      recovery.adminDomainThreshold !== undefined ||
      recovery.domainThreshold !== undefined ||
      recovery.domainConfirmationThreshold !== undefined ||
      recovery.requiredDomains !== undefined ||
      policy.recoveryThreshold !== undefined ||
      policy.deviceThreshold !== undefined ||
      policy.deviceConfirmationThreshold !== undefined ||
      policy.minDeviceConfirmations !== undefined ||
      policy.adminDomainThreshold !== undefined ||
      policy.domainConfirmationThreshold !== undefined ||
      policy.minDomainConfirmations !== undefined);

  const deviceThreshold = firstDefined(
    asNumber(recovery.deviceThreshold),
    asNumber(recovery.chipThreshold),
    asNumber(recovery.deviceConfirmationThreshold),
    asNumber(recovery.threshold),
    asNumber(recovery.requiredDevices),
    asNumber(recovery.minDevices),
    asNumber(policy.recoveryThreshold),
    asNumber(policy.deviceThreshold),
    asNumber(policy.minDevices),
    asNumber(policy.recoveryDeviceThreshold),
    asNumber(policy.deviceConfirmationThreshold),
    asNumber(policy.minDeviceConfirmations),
    recoveryEnabled ? 1 : 0,
  ) as number;
  const domainThreshold = firstDefined(
    asNumber(recovery.adminDomainThreshold),
    asNumber(recovery.domainThreshold),
    asNumber(recovery.domainConfirmationThreshold),
    asNumber(recovery.requiredDomains),
    asNumber(recovery.minDomains),
    asNumber(policy.adminDomainThreshold),
    asNumber(policy.minDomains),
    asNumber(policy.recoveryDomainThreshold),
    asNumber(policy.domainConfirmationThreshold),
    asNumber(policy.minDomainConfirmations),
    recoveryEnabled ? 1 : 0,
  ) as number;

  return {
    digest: asString(firstDefined(
      policy.digest,
      policy.id,
      asString(policy.policyDigest),
      asString(policy.hash),
    )),
    workloadId: policy.workloadId,
    generation: policy.generation,
    baselineTcb: firstDefined(
      policy.baselineTcb,
      policy.minimumTcb,
      policy.requiredTcb,
      policy.minTcb,
      policy.tcbBaseline,
      tcbConfig?.baseline,
      tcbConfig?.minimum,
      tcbConfig?.vector,
    ) as TcbVector | undefined,
    tcbRules:
      policy.tcbRules ??
      (isPlainObject(tcbConfig?.rules)
        ? (tcbConfig.rules as Record<string, TcbRule>)
        : {}),
    requireExactComponentSet:
      asBoolean(policy.requireExactComponentSet) ?? true,
    allowExtraComponents: asBoolean(policy.allowExtraComponents) ?? false,
    ownerThreshold: Math.max(
      0,
      firstDefined(
        asNumber(policy.ownerThreshold),
        asNumber(policy.requiredOwnerConfirmations),
        asNumber(policy.minOwnerConfirmations),
        asNumber(policy.ownerConfirmationThreshold),
        asNumber(policy.ownersRequired),
        asNumber(policy.requiredOwners),
        asNumber(policy.minOwners),
        asNumber(policy.confirmationThreshold),
        asNumber(policy.threshold),
        1,
      ) as number,
      firstDefined(asNumber(policy.ownerDomainThreshold), 0) as number,
    ),
    recoveryEnabled,
    recoveryDeviceThreshold: Math.max(0, deviceThreshold),
    recoveryDomainThreshold: Math.max(0, domainThreshold),
    recoveryRequireBoth: asBoolean(recovery.requireBoth) ?? true,
    requireDeactivation: asBoolean(policy.requireDeactivation) ?? true,
    requireKeyRevocation: asBoolean(policy.requireKeyRevocation) ?? true,
    requireOwnerConfirmation:
      asBoolean(policy.requireOwnerConfirmation) ?? true,
    allowClosedDeviceAsDeactivation:
      asBoolean(policy.allowClosedDeviceAsDeactivation) ?? false,
    requireOldDeviceClosed:
      asBoolean(policy.requireOldDeviceClosed) ?? false,
    allowDebug: asBoolean(policy.allowDebug) ?? false,
    nonceUniqueness: asBoolean(policy.nonceUniqueness) ?? true,
    requireSignatures:
      asBoolean(policy.requireSignatures) ??
      asBoolean(policy.signaturesRequired) ??
      false,
    expectedNonce: asString(
      firstDefined(
        policy.expectedNonce,
        policy.challengeNonce,
        policy.nonce,
      ),
    ),
    raw: policy,
  };
}
