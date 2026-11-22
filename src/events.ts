import {
  createHash,
  createHmac,
  createPrivateKey,
  createPublicKey,
  sign as cryptoSign,
  verify as cryptoVerify,
  type KeyObject,
} from "node:crypto";
import {
  asBoolean,
  asNumber,
  asString,
  asStringArray,
  canonicalJson,
  deepClone,
  firstDefined,
  isPlainObject,
  sha256Hex,
} from "./canonical.ts";
import type {
  Action,
  EventKind,
  MigrationEvent,
  SignatureVerifier,
  TcbVector,
} from "./types.ts";

const ACTION_ALIASES: Record<string, Action> = {
  attest: "attest",
  attestation: "attest",
  quote: "attest",
  "deactivate-instance": "deactivate-instance",
  "instance-deactivate": "deactivate-instance",
  "instance-deactivated": "deactivate-instance",
  "instance-decommission": "deactivate-instance",
  "decommission-instance": "deactivate-instance",
  "retire-instance": "deactivate-instance",
  "instance-stopped": "deactivate-instance",
  "instance-stop": "deactivate-instance",
  decommission: "deactivate-instance",
  retire: "deactivate-instance",
  "revoke-key": "revoke-key",
  "key-revoke": "revoke-key",
  "key-revoked": "revoke-key",
  "revoke-runtime-key": "revoke-key",
  "revoke-key-material": "revoke-key",
  revoke: "revoke-key",
  "accept-transfer": "accept-transfer",
  "transfer-accept": "accept-transfer",
  "transfer-accepted": "accept-transfer",
  "accept-migration": "accept-transfer",
  "receive-transfer": "accept-transfer",
  accept: "accept-transfer",
  "confirm-migration": "confirm-migration",
  "owner-confirm": "confirm-migration",
  "migration-confirm": "confirm-migration",
  "owner-confirm-migration": "confirm-migration",
  confirm: "confirm-migration",
  "confirm-recovery": "confirm-recovery",
  "recovery-confirm": "confirm-recovery",
  "device-confirm": "confirm-recovery",
  "device-confirm-recovery": "confirm-recovery",
  "recovery-threshold": "confirm-recovery",
  recover: "confirm-recovery",
};

const KIND_TO_ACTION: Record<string, Action> = {
  attestation: "attest",
  "attestation-quote": "attest",
  "attestation-record": "attest",
  "attestation-event": "attest",
  attest: "attest",
  quote: "attest",
  "instance-deactivation": "deactivate-instance",
  "deactivation-event": "deactivate-instance",
  "instance-deactivated": "deactivate-instance",
  "deactivate-instance": "deactivate-instance",
  deactivate: "deactivate-instance",
  "instance-decommission": "deactivate-instance",
  "decommission": "deactivate-instance",
  "instance-stopped": "deactivate-instance",
  "instance-stop": "deactivate-instance",
  deactivation: "deactivate-instance",
  "key-revocation": "revoke-key",
  "revocation-event": "revoke-key",
  "key-revoked": "revoke-key",
  "key-revoke": "revoke-key",
  "revoke-key": "revoke-key",
  revocation: "revoke-key",
  "transfer-acceptance": "accept-transfer",
  "transfer-event": "accept-transfer",
  "transfer-accepted": "accept-transfer",
  "accept-transfer": "accept-transfer",
  "device-transfer": "accept-transfer",
  "chip-transfer": "accept-transfer",
  "migration-acceptance": "accept-transfer",
  transfer: "accept-transfer",
  acceptance: "accept-transfer",
  "owner-confirmation": "confirm-migration",
  "owner-event": "confirm-migration",
  "owner-approval": "confirm-migration",
  "confirm-migration": "confirm-migration",
  "owner-confirm": "confirm-migration",
  owner: "confirm-migration",
  "recovery-confirmation": "confirm-recovery",
  "recovery-event": "confirm-recovery",
  "recovery-confirm": "confirm-recovery",
  "recovery-approval": "confirm-recovery",
  "confirm-recovery": "confirm-recovery",
  recovery: "confirm-recovery",
  "device-confirmation": "confirm-recovery",
  "device-confirm": "confirm-recovery",
};

for (const [kind, action] of Object.entries(KIND_TO_ACTION)) {
  if (ACTION_ALIASES[kind] === undefined) ACTION_ALIASES[kind] = action;
}

function normalizeToken(value: unknown): string | undefined {
  const text = asString(value);
  if (text === undefined) return undefined;
  return text
    .trim()
    .replace(/([a-z0-9])([A-Z])/g, "$1-$2")
    .replace(/[_\s]+/g, "-")
    .toLowerCase();
}

export function normalizeAction(value: unknown): Action | undefined {
  const token = normalizeToken(value);
  if (token === undefined) return undefined;
  return ACTION_ALIASES[token];
}

export function actionForKind(kind: unknown): Action | undefined {
  const token = normalizeToken(kind);
  if (token === undefined) return undefined;
  return KIND_TO_ACTION[token];
}

function collectData(record: MigrationEvent): Record<string, unknown> {
  const data: Record<string, unknown> = { ...record };
  for (const key of [
    "attestation",
    "claims",
    "quote",
    "evidence",
    "statement",
    "document",
    "body",
    "event",
    "record",
    "envelope",
    "signed",
    "metadata",
    "meta",
    "data",
    "payload",
  ]) {
    if (isPlainObject(record[key])) Object.assign(data, record[key]);
  }
  for (const key of [
    "attestation",
    "claims",
    "quote",
    "evidence",
    "statement",
    "document",
    "body",
    "event",
    "record",
    "envelope",
    "signed",
    "metadata",
    "meta",
    "data",
    "payload",
  ]) {
    const nested = data[key];
    if (!isPlainObject(nested)) continue;
    for (const [nestedKey, nestedValue] of Object.entries(nested)) {
      if (data[nestedKey] === undefined) data[nestedKey] = nestedValue;
    }
  }
  for (const [key, value] of Object.entries(data)) {
    const camelKey = key.replace(/[_-]([a-zA-Z0-9])/g, (_, char: string) =>
      char.toUpperCase(),
    );
    if (camelKey !== key && data[camelKey] === undefined) {
      data[camelKey] = value;
    }
  }
  return data;
}

function nestedObject(
  data: Record<string, unknown>,
  keys: string[],
): Record<string, unknown> | undefined {
  for (const key of keys) {
    const value = data[key];
    if (isPlainObject(value)) return value;
  }
  return undefined;
}

function subObject(
  data: Record<string, unknown>,
  key: string,
): Record<string, unknown> | undefined {
  const value = data[key];
  return isPlainObject(value) ? value : undefined;
}

function read(
  data: Record<string, unknown>,
  keys: string[],
  nested: Array<Record<string, unknown> | undefined> = [],
): unknown {
  for (const source of [data, ...nested]) {
    if (!source) continue;
    for (const key of keys) {
      if (source[key] !== undefined && source[key] !== null) return source[key];
    }
  }
  return undefined;
}

function rawActionValues(record: MigrationEvent, data: Record<string, unknown>): unknown[] {
  return [
    record.action,
    record.declaredAction,
    record.signedAction,
    record.authorizedAction,
    record.actionName,
    data.action,
    data.signedAction,
    data.declaredAction,
    data.authorizedAction,
    data.actionName,
    isPlainObject(record.payload) ? record.payload.action : undefined,
    isPlainObject(record.claims) ? record.claims.action : undefined,
  ].filter((value) => value !== undefined && value !== null);
}

export interface ExtractedAction {
  action?: Action;
  conflict: boolean;
  declared: string[];
}

function inferActionFromData(
  data: Record<string, unknown>,
): Action | undefined {
  const has = (key: string): boolean =>
    data[key] !== undefined && data[key] !== null;
  if (has("nonce") || has("tcb") || has("tcbVector") || has("policyDigest")) {
    return "attest";
  }
  if (
    has("revokedKey") ||
    has("revokedRuntimeKey") ||
    has("revokedAt") ||
    has("revocationReason")
  ) {
    return "revoke-key";
  }
  if (
    (has("attestationId") || has("attestationDigest")) &&
    (has("targetChipId") || has("newChipId") || has("toChipId"))
  ) {
    return "accept-transfer";
  }
  if (has("certificateId") || has("certId")) {
    return "confirm-recovery";
  }
  if (
    (has("ownerId") || has("owner") || has("adminDomainId")) &&
    has("migrationId")
  ) {
    return "confirm-migration";
  }
  if (has("bootCount") || has("instanceBootCount")) {
    return "deactivate-instance";
  }
  return undefined;
}

export function resolveAction(record: MigrationEvent): ExtractedAction {
  const data = collectData(record);
  delete data.signature;
  delete data.sig;
  delete data.proof;
  const declared = rawActionValues(record, data)
    .map((value) => {
      const normalized = normalizeAction(value);
      if (normalized) return normalized;
      const token = normalizeToken(value);
      if (
        token &&
        [
          "sign",
          "signed",
          "signature",
          "endorse",
          "authorization",
          "approve",
          "approved",
          "approval",
        ].includes(token)
      ) {
        return "";
      }
      return token ?? String(value);
    })
    .filter((value) => value.length > 0);
  const actionSet = new Set(declared);
  const kindValues = [
    record.kind,
    record.type,
    record.eventType,
    record.recordType,
    data.kind,
    data.type,
    data.eventType,
  ];
  const kindActions = new Set(
    kindValues
      .map((value) => actionForKind(value))
      .filter((value): value is Action => value !== undefined),
  );
  for (const kindAction of kindActions) actionSet.add(kindAction);
  if (actionSet.size === 0) {
    const inferred = inferActionFromData(data);
    if (inferred) actionSet.add(inferred);
  }

  if (actionSet.size > 1) {
    return { conflict: true, declared: [...actionSet] };
  }
  const only = [...actionSet][0] as Action | undefined;
  return { action: only, conflict: false, declared: only ? [only] : [] };
}

export interface EventInfo {
  raw: MigrationEvent;
  id: string;
  action?: Action;
  kind?: EventKind;
  signer?: string;
  signerChipId?: string;
  signature?: string;
  causalParents: string[];
  migrationId?: string;
  workloadId?: string;
  generation?: number;
  chipId?: string;
  bootCount?: number;
  debug?: boolean;
  runtimeKey?: string;
  nonce?: string;
  tcb?: TcbVector;
  policyDigest?: string;
  attestationId?: string;
  attestationDigest?: string;
  oldChipId?: string;
  oldRuntimeKey?: string;
  targetChipId?: string;
  targetRuntimeKey?: string;
  certificateId?: string;
  adminDomainId?: string;
  ownerId?: string;
  data: Record<string, unknown>;
  invalid: boolean;
  invalidReasons: string[];
  missingBindings: string[];
  signatureVerified: boolean;
}

function pushMissing(list: string[], condition: boolean, name: string): void {
  if (condition && !list.includes(name)) list.push(name);
}

export function extractEventInfo(
  record: MigrationEvent,
  verifier?: SignatureVerifier,
  requireSignatures = false,
): EventInfo {
  const data = collectData(record);
  const actionResolution = resolveAction(record);
  const source = nestedObject(data, ["source", "old", "from", "previous", "origin"]);
  const target = nestedObject(data, ["target", "destination", "new", "to", "recipient"]);

  const rawId = firstDefined(
    asString(record.id),
    asString(record.eventId),
    asString(record.recordId),
    asString(data.id),
    asString(data.eventId),
    asString(data.recordId),
    actionResolution.action === "attest"
      ? asString(data.quoteId)
      : undefined,
    actionResolution.action === "attest"
      ? asString(data.proofId)
      : undefined,
  );

  const signerObject = isPlainObject(record.signer)
    ? (record.signer as Record<string, unknown>)
    : subObject(data, "signer");
  const signer = firstDefined(
    asString(record.signer),
    asString(record.signerId),
    asString(record.issuer),
    asString(record.author),
    asString(data.signer),
    asString(data.signerId),
    asString(data.issuer),
    asString(data.ownerId),
    asString(data.certificateId),
    asString(signerObject?.id),
    asString(signerObject?.chipId),
    asString(signerObject?.chip_id),
    asString(signerObject?.signer_id),
    asString(signerObject?.adminDomainId),
  );
  const signerChipId = firstDefined(
    asString(data.signerChipId),
    asString(data.chipSignerId),
    asString(data.signerDeviceId),
    asString(signerObject?.chipId),
    asString(signerObject?.chip_id),
    asString(signerObject?.deviceId),
  );
  const proofObject = isPlainObject(record.proof)
    ? (record.proof as Record<string, unknown>)
    : isPlainObject(data.proof)
      ? data.proof
      : undefined;
  const signature = firstDefined(
    asString(record.signature),
    asString(record.sig),
    asString(record.proof),
    asString(proofObject?.signature),
    asString(proofObject?.sig),
    asString(proofObject?.value),
    asString(data.signature),
    asString(data.sig),
    asString(data.signatureValue),
    asString(data.sigValue),
  );

  const causalParents = Array.from(
    new Set(
      asStringArray(
        firstDefined(
          record.causalParents,
          record.parents,
          record.causes,
          record.dependsOn,
          record.causalPredecessors,
          record.predecessors,
          data.causalParents,
          data.parents,
          data.causes,
          data.dependsOn,
          data.causalPredecessors,
          data.predecessors,
        ),
      ),
    ),
  );

  const migrationId = firstDefined(
    asString(data.migrationId),
    asString(data.migrationID),
    asString(data.migration),
    asString(data.transactionId),
    asString(data.txId),
    asString(data.transferId),
    asString(subObject(data, "migration")?.id),
    asString(subObject(data, "transaction")?.id),
  );
  const workloadId = firstDefined(
    asString(data.workloadId),
    asString(data.workloadID),
    asString(data.workload),
    asString(data.workloadPersistentId),
    asString(data.workloadPersistentID),
    asString(data.workloadPid),
    asString(data.workloadUid),
    asString(data.persistentWorkloadId),
    asString(data.persistentId),
    asString(subObject(data, "workload")?.id),
    asString(subObject(data, "workload")?.persistentId),
  );
  const generation = firstDefined(
    asNumber(data.generation),
    asNumber(data.keyGeneration),
    asNumber(data.gen),
    asNumber(data.generationId),
    asNumber(data.keyGen),
    asNumber(subObject(data, "key")?.generation),
    asNumber(subObject(data, "key")?.generationId),
  );

  const chipId = firstDefined(
    asString(data.chipId),
    asString(data.chipID),
    asString(data.deviceId),
    asString(data.physicalChipId),
    asString(data.physicalDeviceId),
    asString(data.chipIdentity),
    asString(data.hardwareId),
    asString(data.dieId),
    asString(data.deviceUid),
    asString(data.hostId),
    asString(data.chip),
    asString(data.host),
    asString(subObject(data, "chip")?.id),
    asString(subObject(data, "chip")?.identity),
    asString(subObject(data, "host")?.chipId),
    asString(subObject(data, "host")?.deviceId),
    asString(subObject(data, "host")?.id),
    asString(subObject(data, "certificate")?.chipId),
    asString(subObject(data, "certificate")?.chip_id),
    asString(subObject(data, "certificate")?.physicalChipId),
    asString(subObject(data, "certificate")?.physical_chip_id),
    asString(subObject(data, "certificate")?.deviceId),
    asString(subObject(data, "instance")?.chipId),
    asString(subObject(data, "instance")?.chip_id),
    asString(subObject(data, "instance")?.deviceId),
    asString(subObject(data, "device")?.chipId),
    asString(subObject(data, "device")?.id),
    asString(source?.chipId),
    asString(source?.deviceId),
  );
  const targetChipId = firstDefined(
    asString(data.targetChipId),
    asString(data.targetChipID),
    asString(data.newChipId),
    asString(data.newDeviceId),
    asString(data.targetDeviceId),
    asString(data.toChipId),
    asString(data.toDeviceId),
    asString(data.toChip),
    asString(data.to),
    asString(data.newChip),
    asString(data.recipientChip),
    asString(data.destinationChip),
    asString(data.destinationChipId),
    asString(target?.chipId),
    asString(target?.chip_id),
    asString(target?.deviceId),
    asString(target?.chip),
    actionResolution.action === "accept-transfer" ? chipId : undefined,
  );
  const oldChipId = firstDefined(
    asString(data.oldChipId),
    asString(data.oldChipID),
    asString(data.oldDeviceId),
    asString(data.previousDeviceId),
    asString(data.sourceDeviceId),
    asString(data.sourceChipId),
    asString(data.fromChipId),
    asString(data.fromDeviceId),
    asString(data.fromChip),
    asString(data.from),
    asString(data.oldChip),
    asString(data.sourceChip),
    asString(data.previousChip),
    asString(data.previousChipId),
    asString(source?.chipId),
    asString(source?.chip_id),
    asString(source?.deviceId),
    asString(source?.chip),
    actionResolution.action === "deactivate-instance" ? chipId : undefined,
  );

  const bootCount = firstDefined(
    asNumber(data.bootCount),
    asNumber(data.instanceBootCount),
    asNumber(data.boot),
    asNumber(data.bootEpoch),
    asNumber(data.instanceEpoch),
    asNumber(data.bootCounter),
    asNumber(data.launchCount),
    asNumber(data.startCount),
    asNumber(subObject(data, "instance")?.bootCount),
    asNumber(subObject(data, "instance")?.boot_count),
    asNumber(subObject(data, "device")?.bootCount),
    asNumber(subObject(data, "runtime")?.bootCount),
    asNumber(data.instanceBoot),
  );

  const runtimeKey = firstDefined(
    asString(data.runtimeKey),
    asString(data.runtimePublicKey),
    asString(data.runtimePubKey),
    asString(data.publicKey),
    asString(data.pubKey),
    asString(data.key),
    asString(data.runtimeKeyFingerprint),
    asString(data.publicKeyFingerprint),
    asString(data.publicKeyHash),
    asString(data.akPub),
    asString(data.keyFingerprint),
    asString(data.keyId),
    asString(subObject(data, "key")?.fingerprint),
    asString(subObject(data, "key")?.id),
    asString(subObject(data, "runtime")?.key),
    asString(subObject(data, "runtime")?.publicKey),
    asString(subObject(data, "instance")?.runtimeKey),
    asString(subObject(data, "device")?.runtimeKey),
    asString(data.runtimeKeyId),
    asString(data.targetRuntimeKey),
    asString(data.oldRuntimeKey),
    asString(data.revokedRuntimeKey),
    asString(data.revokedKey),
  );
  const targetRuntimeKey = firstDefined(
    asString(data.targetRuntimeKey),
    asString(data.newRuntimeKey),
    asString(data.newKey),
    asString(data.toKey),
    asString(data.targetPubKey),
    asString(data.recipientKey),
    asString(data.targetPublicKey),
    asString(data.newPublicKey),
    asString(data.newPubKey),
    asString(data.targetKey),
    asString(target?.runtimeKey),
    asString(target?.runtime_key),
    asString(target?.publicKey),
    asString(target?.pubKey),
    asString(target?.key),
    actionResolution.action === "accept-transfer" ? runtimeKey : undefined,
  );
  const oldRuntimeKey = firstDefined(
    asString(data.oldRuntimeKey),
    asString(data.oldPublicKey),
    asString(data.oldPubKey),
    asString(data.previousRuntimeKey),
    asString(data.previousPublicKey),
    asString(data.sourceRuntimeKey),
    asString(data.oldKey),
    asString(data.fromKey),
    asString(data.sourceKey),
    asString(data.previousKey),
    asString(data.revokedRuntimeKey),
    asString(data.revokedKey),
    asString(source?.runtimeKey),
    asString(source?.runtime_key),
    asString(source?.publicKey),
    asString(source?.key),
    asString(subObject(data, "instance")?.runtimeKey),
    asString(subObject(data, "device")?.runtimeKey),
    actionResolution.action === "deactivate-instance" ||
    actionResolution.action === "revoke-key"
      ? runtimeKey
      : undefined,
  );

  const nonce = firstDefined(
    asString(data.nonce),
    asString(data.attestationNonce),
    asString(data.challenge),
    asString(data.challengeNonce),
    asString(data.nonceHex),
    asString(subObject(data, "challenge")?.value),
    asString(subObject(data, "challenge")?.nonce),
    asString(subObject(data, "nonce")?.value),
    asString(subObject(data, "nonce")?.nonce),
  );
  let tcb = firstDefined(
    data.tcb,
    data.tcbVector,
    data.tcbVersionVector,
    data.tcbVersion,
    data.tcbVersions,
    data.securityVersion,
    data.versions,
    data.versionVector,
  ) as TcbVector | undefined;
  const tcbNamespace = firstDefined(
    asString(data.tcbEpoch),
    asString(data.tcbFamily),
    asString(data.tcbNamespace),
    asString(data.epoch),
  );
  if (tcbNamespace && isPlainObject(tcb)) {
    const existing =
      data.tcbEpoch !== undefined
        ? "epoch"
        : data.tcbFamily !== undefined
          ? "family"
          : data.tcbNamespace !== undefined
            ? "namespace"
            : "epoch";
    if (tcb[existing] === undefined) {
      tcb = { ...tcb, [existing]: tcbNamespace };
    }
  }
  const policyDigest = firstDefined(
    asString(data.policyDigest),
    asString(data.policyId),
    asString(data.policyHash),
    asString(data.policyDigestHex),
    asString(data.policyDigestSha256),
    asString(data.policyMeasurement),
    asString(data.policyDigestValue),
    asString(data.policy),
    isPlainObject(data.policy)
      ? asString(data.policy.digest) ??
        asString(data.policy.id) ??
        asString(data.policy.hash) ??
        asString(data.policy.value)
      : undefined,
  );
  const debugValues = [
    data.debug,
    data.debugEnabled,
    data.isDebug,
    data.debugBit,
    data.debugFlag,
  ]
    .map((value) => asBoolean(value))
    .filter((value): value is boolean => value !== undefined);
  const debug =
    debugValues.length === 0
      ? undefined
      : debugValues.some((value) => value);

  const attestationId = firstDefined(
    asString(data.attestationId),
    asString(data.quoteId),
    asString(data.proofId),
    asString(data.attestationRecordId),
    asString(data.attestationRef),
    asString(data.quoteRef),
  );
  const attestationDigest = firstDefined(
    asString(data.attestationDigest),
    asString(data.quoteDigest),
    asString(data.proofDigest),
    asString(data.attestationHash),
    asString(data.quoteHash),
    asString(data.attestationDigestHex),
  );
  const certificateId = firstDefined(
    asString(data.certificateId),
    asString(data.certId),
    asString(data.certificate),
    asString(data.cert),
    asString(data.certificateSerial),
    asString(subObject(data, "certificate")?.id),
    asString(subObject(data, "certificate")?.serialNumber),
    asString(subObject(data, "certificate")?.serial),
    asString(subObject(data, "device")?.certificateId),
  );
  const adminDomainId = firstDefined(
    asString(data.adminDomainId),
    asString(data.adminDomain),
    asString(data.adminDomainName),
    asString(data.organizationId),
    asString(data.orgId),
    asString(data.tenantId),
    asString(data.managementDomainId),
    asString(data.domainId),
    asString(data.domain),
    asString(data.managementDomain),
    asString(subObject(data, "domain")?.id),
    asString(subObject(data, "certificate")?.adminDomainId),
    asString(subObject(data, "certificate")?.admin_domain_id),
    asString(subObject(data, "certificate")?.domainId),
    asString(subObject(subObject(data, "certificate") ?? {}, "adminDomain")?.id),
    asString(subObject(data, "device")?.adminDomainId),
    asString(subObject(data, "device")?.domainId),
    asString(subObject(data, "owner")?.adminDomainId),
    asString(subObject(data, "owner")?.admin_domain_id),
    asString(subObject(data, "owner")?.domainId),
    asString(subObject(subObject(data, "owner") ?? {}, "domain")?.id),
    asString(subObject(data, "managementDomain")?.id),
    asString(subObject(data, "organization")?.id),
    asString(subObject(data, "tenant")?.id),
  );
  const ownerId = firstDefined(
    asString(data.ownerId),
    asString(data.owner),
    asString(data.ownerPrincipal),
    asString(data.ownerDomainId),
    asString(subObject(data, "owner")?.id),
    asString(subObject(data, "owner")?.ownerId),
  );

  const id = rawId ?? `auto:${sha256Hex(canonicalJson(record)).slice(0, 24)}`;
  const invalidReasons: string[] = [];
  if (actionResolution.conflict) {
    invalidReasons.push(
      `action-mismatch:${actionResolution.declared.sort().join(",")}`,
    );
  } else if (!actionResolution.action) {
    invalidReasons.push("missing-action");
  }

  let signatureVerified = false;
  if (verifier) {
    try {
      signatureVerified = Boolean(verifier(record, canonicalRecord(record)));
    } catch {
      signatureVerified = false;
    }
    if (!signatureVerified) invalidReasons.push("invalid-signature");
  } else if (requireSignatures) {
    if (!signature) invalidReasons.push("missing-signature");
    else invalidReasons.push("unverifiable-signature");
  } else {
    signatureVerified = true;
  }

  const missingBindings: string[] = [];
  if (actionResolution.action === "attest") {
    pushMissing(missingBindings, chipId === undefined, "chipId");
    pushMissing(missingBindings, workloadId === undefined, "workloadId");
    pushMissing(missingBindings, bootCount === undefined, "bootCount");
    pushMissing(missingBindings, debug === undefined, "debug");
    pushMissing(missingBindings, runtimeKey === undefined, "runtimeKey");
    pushMissing(missingBindings, nonce === undefined, "nonce");
    pushMissing(missingBindings, tcb === undefined, "tcb");
    pushMissing(missingBindings, policyDigest === undefined, "policyDigest");
  } else if (actionResolution.action === "deactivate-instance") {
    pushMissing(missingBindings, chipId === undefined, "chipId");
    pushMissing(missingBindings, workloadId === undefined, "workloadId");
    pushMissing(missingBindings, bootCount === undefined, "bootCount");
  } else if (actionResolution.action === "revoke-key") {
    pushMissing(missingBindings, runtimeKey === undefined, "runtimeKey");
    pushMissing(missingBindings, workloadId === undefined, "workloadId");
  } else if (actionResolution.action === "accept-transfer") {
    pushMissing(missingBindings, targetChipId === undefined, "targetChipId");
    pushMissing(missingBindings, targetRuntimeKey === undefined, "targetRuntimeKey");
    pushMissing(missingBindings, generation === undefined, "generation");
  } else if (actionResolution.action === "confirm-migration") {
    pushMissing(missingBindings, migrationId === undefined, "migrationId");
    pushMissing(
      missingBindings,
      adminDomainId === undefined && ownerId === undefined && signer === undefined,
      "owner-domain",
    );
  } else if (actionResolution.action === "confirm-recovery") {
    pushMissing(missingBindings, migrationId === undefined, "migrationId");
    pushMissing(
      missingBindings,
      chipId === undefined &&
        certificateId === undefined &&
        adminDomainId === undefined,
      "recovery-identity",
    );
  }

  return {
    raw: record,
    id,
    action: actionResolution.action,
    kind: (record.kind ??
      record.type ??
      record.eventType ??
      record.recordType ??
      data.kind ??
      data.type) as EventKind | undefined,
    signer,
    signerChipId,
    signature,
    causalParents,
    migrationId,
    workloadId,
    generation,
    chipId,
    bootCount,
    debug,
    runtimeKey,
    nonce,
    tcb,
    policyDigest,
    attestationId,
    attestationDigest,
    oldChipId,
    oldRuntimeKey,
    targetChipId,
    targetRuntimeKey,
    certificateId,
    adminDomainId,
    ownerId,
    data,
    invalid: invalidReasons.length > 0,
    invalidReasons,
    missingBindings,
    signatureVerified,
  };
}

/**
 * Canonical bytes covered by a signature.  The declared action is always part
 * of the bytes, so a signature made for one action can never be replayed as a
 * different action.
 */
export function canonicalRecord(record: MigrationEvent): string {
  const clone = deepClone(record) as Record<string, unknown>;
  delete clone.signature;
  delete clone.sig;
  delete clone.proof;
  delete clone.signatureVerified;
  const resolved = resolveAction(record);
  const declaresAction =
    record.action !== undefined ||
    record.declaredAction !== undefined ||
    record.signedAction !== undefined ||
    record.authorizedAction !== undefined ||
    (isPlainObject(record.payload) && record.payload.action !== undefined);
  if (resolved.action && declaresAction) clone.action = resolved.action;
  return canonicalJson(clone);
}

export function canonicalRecordBytes(record: MigrationEvent): string {
  const clone = deepClone(record) as Record<string, unknown>;
  delete clone.signature;
  delete clone.sig;
  delete clone.proof;
  delete clone.signatureVerified;
  return canonicalJson(clone);
}

export function canonicalRecordWithAction(record: MigrationEvent): string {
  const clone = deepClone(record) as Record<string, unknown>;
  delete clone.signature;
  delete clone.sig;
  delete clone.proof;
  delete clone.signatureVerified;
  const resolved = resolveAction(record);
  if (resolved.action) clone.action = resolved.action;
  return canonicalJson(clone);
}

export function plainRecordJson(record: MigrationEvent): string {
  const clone = deepClone(record) as Record<string, unknown>;
  delete clone.signature;
  delete clone.sig;
  delete clone.proof;
  delete clone.signatureVerified;
  return JSON.stringify(clone);
}

export function recordDigest(record: MigrationEvent): string {
  return sha256Hex(canonicalRecord(record));
}

type PrivateKeyInput = string | Buffer | KeyObject;
type PublicKeyInput = string | Buffer | KeyObject;

function toPrivateKey(key: PrivateKeyInput): KeyObject {
  if (isPlainObject(key)) {
    const inner = key.privateKey ?? key.pem ?? key.key;
    if (inner !== undefined) {
      return toPrivateKey(inner as PrivateKeyInput);
    }
  }
  if (typeof key !== "string" && !Buffer.isBuffer(key)) return key;
  try {
    return createPrivateKey(key);
  } catch {
    if (typeof key === "string") {
      return createPrivateKey(Buffer.from(key, "base64"));
    }
    throw new Error("invalid private key");
  }
}

function toPublicKey(key: PublicKeyInput): KeyObject {
  if (isPlainObject(key)) {
    const inner = key.publicKey ?? key.pem ?? key.key;
    if (inner !== undefined) {
      return toPublicKey(inner as PublicKeyInput);
    }
  }
  if (typeof key !== "string" && !Buffer.isBuffer(key)) return key;
  try {
    return createPublicKey(key);
  } catch {
    if (typeof key === "string") {
      return createPublicKey(Buffer.from(key, "base64"));
    }
    throw new Error("invalid public key");
  }
}

function verifyWithAnyAlgorithm(
  data: Buffer,
  key: KeyObject,
  signature: Buffer,
): boolean {
  for (const algorithm of [null, "sha256", "sha384", "sha512"] as const) {
    try {
      if (cryptoVerify(algorithm, data, key, signature)) return true;
    } catch {
      // The key type may not support this algorithm.
    }
  }
  return false;
}

function actionBoundClaimVariants(record: MigrationEvent): string[] {
  const resolved = resolveAction(record);
  if (!resolved.action) return [];
  const data = collectData(record);
  delete data.signature;
  delete data.sig;
  delete data.proof;
  const declared = firstDefined(
    data.action,
    record.action,
    record.declaredAction,
    record.signedAction,
  );
  const actionBound =
    normalizeAction(declared) === resolved.action ||
    [
      record.kind,
      record.type,
      record.eventType,
      record.recordType,
      data.kind,
      data.type,
      data.eventType,
    ].some((value) => actionForKind(value) === resolved.action);
  if (!actionBound) return [];
  const variants = [
    canonicalJson(data),
    canonicalJson({ action: resolved.action, payload: data }),
    canonicalJson({ action: resolved.action, ...data }),
  ];
  if (isPlainObject(record.payload)) {
    const payload = record.payload;
    if (normalizeAction(payload.action) === resolved.action) {
      variants.push(canonicalJson(payload));
      variants.push(
        canonicalJson({ action: resolved.action, ...payload }),
      );
      variants.push(JSON.stringify(payload));
      variants.push(
        JSON.stringify({ action: resolved.action, payload }),
      );
    }
  }
  return variants;
}

/** Sign a record with Ed25519 (recommended) or another crypto.sign key. */
export function signRecord<T extends MigrationEvent>(
  record: T,
  privateKey: PrivateKeyInput,
): T & { signature: string } {
  const canonical = canonicalRecord(record);
  const key = toPrivateKey(privateKey);
  let signature: Buffer;
  try {
    signature = cryptoSign(null, Buffer.from(canonical), key);
  } catch {
    signature = cryptoSign("sha256", Buffer.from(canonical), key);
  }
  return { ...record, signature: signature.toString("base64") };
}

export function verifyRecordSignature(
  record: MigrationEvent,
  publicKey: PublicKeyInput,
): boolean {
  const resolved = resolveAction(record);
  if (resolved.conflict || resolved.action === undefined) return false;
  const data = collectData(record);
  const signature = firstDefined(
    asString(record.signature),
    asString(record.sig),
    asString(record.proof),
    asString(data.signature),
    asString(data.sig),
    asString(data.signatureValue),
    asString(data.sigValue),
    isPlainObject(record.proof)
      ? firstDefined(
          asString((record.proof as Record<string, unknown>).signature),
          asString((record.proof as Record<string, unknown>).sig),
          asString((record.proof as Record<string, unknown>).value),
        )
      : undefined,
  );
  if (!signature) return false;
  const canonical = Buffer.from(canonicalRecord(record));
  const variants = [
    canonical,
    Buffer.from(canonicalRecordBytes(record)),
    Buffer.from(canonicalRecordWithAction(record)),
    Buffer.from(plainRecordJson(record)),
    createHash("sha256").update(canonical).digest(),
    createHash("sha256").update(canonicalRecordBytes(record)).digest(),
    createHash("sha256").update(plainRecordJson(record)).digest(),
    ...actionBoundClaimVariants(record).map((variant) => Buffer.from(variant)),
  ];
  const key = toPublicKey(publicKey);
  const candidates = [
    Buffer.from(signature, "base64"),
    Buffer.from(signature, "hex"),
    Buffer.from(signature.replace(/-/g, "+").replace(/_/g, "/"), "base64"),
  ];
  for (const candidate of candidates) {
    if (candidate.length === 0) continue;
    for (const data of variants) {
      if (verifyWithAnyAlgorithm(data, key, candidate)) return true;
    }
  }
  return false;
}

export function createEd25519Verifier(
  publicKeys: Record<string, PublicKeyInput>,
): SignatureVerifier {
  return (record, canonical) => {
    const resolved = resolveAction(record);
    if (resolved.conflict || resolved.action === undefined) return false;
    const data = collectData(record);
    const signer = firstDefined(
      asString(record.signer),
      asString(record.signerId),
      asString(record.issuer),
      asString(record.author),
      asString(data.signer),
      asString(data.signerId),
      asString(data.issuer),
      asString(data.author),
      isPlainObject(record.signer)
        ? asString((record.signer as Record<string, unknown>).id)
        : undefined,
      isPlainObject(record.signer)
        ? asString((record.signer as Record<string, unknown>).chipId)
        : undefined,
    );
    const keySource = publicKeys as unknown;
    if (!signer) {
      const wildcard =
        keySource instanceof Map
          ? keySource.has("*")
          : publicKeys["*"] !== undefined;
      if (!wildcard) return false;
    }
    const lookupSigner = signer ?? "*";
    const publicKey =
      keySource instanceof Map
        ? (keySource.get(lookupSigner) ?? keySource.get("*"))
        : (publicKeys[lookupSigner] ?? publicKeys["*"]);
    if (publicKey === undefined) return false;
    const signature = firstDefined(
      asString(record.signature),
      asString(record.sig),
      asString(record.proof),
      asString(data.signature),
      asString(data.sig),
      asString(data.signatureValue),
      asString(data.sigValue),
      isPlainObject(record.proof)
        ? firstDefined(
            asString((record.proof as Record<string, unknown>).signature),
            asString((record.proof as Record<string, unknown>).sig),
            asString((record.proof as Record<string, unknown>).value),
          )
        : undefined,
    );
    if (!signature) return false;
    const key = toPublicKey(publicKey as PublicKeyInput);
    const variants = [
      canonical,
      canonicalRecordBytes(record),
      canonicalRecordWithAction(record),
      plainRecordJson(record),
      createHash("sha256").update(canonical).digest(),
      createHash("sha256").update(canonicalRecordBytes(record)).digest(),
      createHash("sha256").update(plainRecordJson(record)).digest(),
      ...actionBoundClaimVariants(record),
    ];
    for (const candidate of [
      Buffer.from(signature, "base64"),
      Buffer.from(signature, "hex"),
      Buffer.from(signature.replace(/-/g, "+").replace(/_/g, "/"), "base64"),
    ]) {
      if (candidate.length === 0) continue;
      for (const data of variants) {
        if (verifyWithAnyAlgorithm(Buffer.from(data), key, candidate)) {
          return true;
        }
      }
    }
    return false;
  };
}

export function signRecordHmac<T extends MigrationEvent>(
  record: T,
  secret: string | Buffer,
): T & { signature: string } {
  const signature = createHmac("sha256", secret)
    .update(canonicalRecord(record))
    .digest("base64");
  return { ...record, signature };
}

export function createHmacVerifier(
  secrets: Record<string, string | Buffer>,
): SignatureVerifier {
  return (record, canonical) => {
    const resolved = resolveAction(record);
    if (resolved.conflict || resolved.action === undefined) return false;
    const data = collectData(record);
    const signer = firstDefined(
      asString(record.signer),
      asString(record.signerId),
      asString(record.issuer),
      asString(record.author),
      asString(data.signer),
      asString(data.signerId),
      asString(data.issuer),
      asString(data.author),
      isPlainObject(record.signer)
        ? asString((record.signer as Record<string, unknown>).id)
        : undefined,
      isPlainObject(record.signer)
        ? asString((record.signer as Record<string, unknown>).chipId)
        : undefined,
    );
    const secretSource = secrets as unknown;
    if (!signer) {
      const wildcard =
        secretSource instanceof Map
          ? secretSource.has("*")
          : secrets["*"] !== undefined;
      if (!wildcard) return false;
    }
    const lookupSigner = signer ?? "*";
    const secret =
      secretSource instanceof Map
        ? (secretSource.get(lookupSigner) ?? secretSource.get("*"))
        : (secrets[lookupSigner] ?? secrets["*"]);
    if (secret === undefined) return false;
    const signature = firstDefined(
      asString(record.signature),
      asString(record.sig),
      asString(record.proof),
      asString(data.signature),
      asString(data.sig),
      asString(data.signatureValue),
      asString(data.sigValue),
      isPlainObject(record.proof)
        ? firstDefined(
            asString((record.proof as Record<string, unknown>).signature),
            asString((record.proof as Record<string, unknown>).sig),
            asString((record.proof as Record<string, unknown>).value),
          )
        : undefined,
    );
    if (!signature) return false;
    const selectedSecret = secret as string | Buffer;
    const expectedVariants = [
      canonical,
      canonicalRecordBytes(record),
      canonicalRecordWithAction(record),
      plainRecordJson(record),
      ...actionBoundClaimVariants(record),
    ].map((variant) =>
      createHmac("sha256", selectedSecret).update(variant).digest(),
    );
    for (const candidate of [
      Buffer.from(signature, "base64"),
      Buffer.from(signature, "hex"),
      Buffer.from(signature.replace(/-/g, "+").replace(/_/g, "/"), "base64"),
    ]) {
      for (const expected of expectedVariants) {
        if (candidate.length !== expected.length) continue;
        let equal = true;
        for (let index = 0; index < candidate.length; index += 1) {
          if (candidate[index] !== expected[index]) {
            equal = false;
            break;
          }
        }
        if (equal) return true;
      }
    }
    return false;
  };
}

export function attestationClaimsDigest(info: EventInfo): string {
  return sha256Hex(
    canonicalJson({
      chipId: info.chipId,
      workloadId: info.workloadId,
      bootCount: info.bootCount,
      debug: info.debug ?? false,
      runtimeKey: info.runtimeKey,
      nonce: info.nonce,
      tcb: info.tcb,
      policyDigest: info.policyDigest,
      generation: info.generation,
    }),
  );
}
