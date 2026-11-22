import {
  asBoolean,
  asNumber,
  asString,
  canonicalJson,
  firstDefined,
  isPlainObject,
  sha256Base64,
  sha256Hex,
} from "./canonical.ts";
import {
  attestationClaimsDigest,
  canonicalRecordBytes,
  createEd25519Verifier,
  createHmacVerifier,
  extractEventInfo,
  plainRecordJson,
  recordDigest,
  type EventInfo,
} from "./events.ts";
import { normalizePolicy, type NormalizedPolicy } from "./policy.ts";
import { compareTcbVectors } from "./tcb.ts";
import type {
  ClosedDeviceManifest,
  EvaluateInput,
  InvalidEvent,
  Lease,
  MigrationDecision,
  MigrationEvent,
  MigrationPolicy,
  MissingEvidence,
  ReleaseDecision,
  ReleaseTarget,
  TcbVector,
} from "./types.ts";

type Status = "release" | "wait" | "reject";

interface Prepared {
  infos: EventInfo[];
  byId: Map<string, EventInfo>;
  invalid: EventInfo[];
  duplicateConflicts: InvalidEvent[];
  missingCausalParents: string[];
}

export interface ClosedManifest {
  chips: Set<string>;
  fullyClosedChips: Set<string>;
  instances: Array<{ chipId: string; bootCount: number }>;
}

interface Transaction {
  id: string;
  workloadId: string;
  generation: number;
  infos: EventInfo[];
  newAttestation?: EventInfo;
  oldAttestations: EventInfo[];
  transfer?: EventInfo;
  transferCandidates: EventInfo[];
  deactivations: EventInfo[];
  revocations: EventInfo[];
  ownerConfirmations: EventInfo[];
  recoveryConfirmations: EventInfo[];
  targetChipId?: string;
  targetRuntimeKey?: string;
  oldChipId?: string;
  target?: ReleaseTarget;
  decision: MigrationDecision;
  rank: number;
}

interface AttestationCheck {
  ok: boolean;
  status: Status;
  reasons: string[];
  blockers: string[];
}

interface ConfirmationSelection {
  satisfied: boolean;
  selected: EventInfo[];
  chipCount: number;
  domainCount: number;
  reason?: string;
}

interface TxWork {
  reasons: string[];
  blockers: string[];
  missing: MissingEvidence[];
  dependencies: Set<string>;
  status: Status;
  transferValid: boolean;
  deactivationCovered: boolean;
  revocationComplete: boolean;
  confirmationComplete: boolean;
}

function eventTime(info: EventInfo): number {
  const raw = info.raw;
  return (
    timestampValue(raw.logicalTime) ??
    timestampValue(raw.createdAt) ??
    timestampValue(raw.time) ??
    timestampValue(raw.timestamp) ??
    asNumber(raw.seq) ??
    timestampValue(info.data.logicalTime) ??
    timestampValue(info.data.createdAt) ??
    timestampValue(info.data.time) ??
    timestampValue(info.data.timestamp) ??
    asNumber(info.data.seq) ??
    Number.MAX_SAFE_INTEGER
  );
}

function timestampValue(value: unknown): number | undefined {
  const numeric = asNumber(value);
  if (numeric !== undefined) return numeric;
  if (typeof value === "string") {
    const parsed = Date.parse(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return undefined;
}

function compareEvents(a: EventInfo, b: EventInfo): number {
  const time = eventTime(a) - eventTime(b);
  if (time !== 0) return time;
  return a.id.localeCompare(b.id);
}

function statusRank(status: Status): number {
  if (status === "release") return 2;
  if (status === "wait") return 1;
  return 0;
}

function addMissing(
  missing: MissingEvidence[],
  action: string,
  reason: string,
  detail?: Record<string, unknown>,
): void {
  if (missing.some((item) => item.action === action && item.reason === reason)) {
    return;
  }
  missing.push({ action, reason, detail });
}

function addReason(reasons: string[], reason: string): void {
  if (!reasons.includes(reason)) reasons.push(reason);
}

function uniqueStrings(values: string[]): string[] {
  return [...new Set(values)];
}

export function normalizeClosedDevices(
  input: ClosedDeviceManifest | undefined,
): ClosedManifest {
  const chips = new Set<string>();
  const fullyClosedChips = new Set<string>();
  const instances: Array<{ chipId: string; bootCount: number }> = [];
  if (!input) return { chips, fullyClosedChips, instances };
  const isClosedState = (state: unknown): boolean => {
    if (state === false || state === null || state === undefined) return false;
    if (typeof state === "string") {
      return ["closed", "off", "stopped", "true"].includes(
        state.toLowerCase(),
      );
    }
    if (typeof state === "object") {
      const record = state as Record<string, unknown>;
      if (record.open !== undefined) {
        return !(asBoolean(record.open) ?? true);
      }
      const declared = record.closed ?? record.state ?? record.value;
      if (typeof declared === "string") {
        return ["closed", "off", "stopped", "true"].includes(
          declared.toLowerCase(),
        );
      }
      return asBoolean(declared) ?? true;
    }
    return Boolean(state);
  };
  let values: unknown[];
  if (input instanceof Set) {
    values = [...input];
  } else if (typeof input === "string") {
    values = [input];
  } else if (input instanceof Map) {
    values = [...input.entries()]
      .filter(([, state]) => isClosedState(state))
      .map(([chipId, state]) =>
        isPlainObject(state)
          ? { chipId, ...state }
          : { chipId, closed: true },
      );
  } else if (Array.isArray(input)) {
    values = input;
  } else if (typeof input === "object" && input !== null) {
    const record = input as Record<string, unknown>;
    if (
      record.chipId !== undefined ||
      record.deviceId !== undefined ||
      record.physicalChipId !== undefined ||
      record.physicalDeviceId !== undefined
    ) {
      values = [record];
    } else {
    const listKeys = [
      "closed",
      "devices",
      "closedChips",
      "closedDevices",
      "instances",
      "closedInstances",
    ];
    const list = listKeys
      .map((key) => record[key])
      .find(
        (value) => Array.isArray(value) || typeof value === "string",
      );
    if (Array.isArray(list)) {
      values = list;
    } else if (typeof list === "string") {
      values = [list];
    } else if (isPlainObject(record.instances)) {
      values = Object.entries(record.instances).map(([chipId, bootCount]) => ({
        chipId,
        bootCount,
      }));
    } else if (
      ["devices", "closedDevices", "closedChips"].some((key) =>
        isPlainObject(record[key]),
      )
    ) {
      const nestedKey = ["devices", "closedDevices", "closedChips"].find(
        (key) => isPlainObject(record[key]),
      ) as string;
      const nested = record[nestedKey] as Record<string, unknown>;
      values = Object.entries(nested).map(([chipId, state]) =>
        isPlainObject(state)
          ? { chipId, ...state }
          : { chipId, closed: state },
      );
    } else if (listKeys.some((key) => key in record)) {
      values = [];
    } else {
      values = Object.entries(record)
        .filter(([, state]) => isClosedState(state))
        .map(([chipId, state]) =>
          isPlainObject(state)
            ? { chipId, ...state }
            : { chipId, closed: true },
        );
    }
    }
  } else {
    values = [];
  }
  for (const item of values) {
    if (typeof item === "string") {
      const trimmed = item.trim();
      if (!trimmed) continue;
      if (trimmed.includes(":")) {
        const [chipId, bootText] = trimmed.split(":", 2);
        const bootCount = asNumber(bootText);
        if (bootCount !== undefined) instances.push({ chipId, bootCount });
        chips.add(chipId);
      } else if (trimmed.includes("@")) {
        const [chipId, bootText] = trimmed.split("@", 2);
        const bootCount = asNumber(bootText);
        if (bootCount !== undefined) instances.push({ chipId, bootCount });
        chips.add(chipId);
      } else {
        chips.add(trimmed);
        fullyClosedChips.add(trimmed);
      }
      continue;
    }
    if (typeof item !== "object" || item === null) continue;
    const device = item as Record<string, unknown>;
    if (
      (device.closed !== undefined ||
        device.state !== undefined ||
        device.value !== undefined ||
        device.open !== undefined) &&
      !isClosedState(device)
    ) {
      continue;
    }
    const nestedDevice = isPlainObject(device.device)
      ? device.device
      : isPlainObject(device.instance)
        ? device.instance
        : undefined;
    const chipId = firstDefined(
      asString(device.chipId),
      asString(device.deviceId),
      asString(device.physicalChipId),
      asString(device.physicalDeviceId),
      asString(nestedDevice?.chipId),
      asString(nestedDevice?.deviceId),
    );
    const bootCount = firstDefined(
      asNumber(device.bootCount),
      asNumber(device.instanceBootCount),
      asNumber(device.boot),
      asNumber(nestedDevice?.bootCount),
      asNumber(nestedDevice?.instanceBootCount),
    );
    if (chipId) {
      chips.add(chipId);
      if (bootCount === undefined) fullyClosedChips.add(chipId);
      if (bootCount !== undefined) instances.push({ chipId, bootCount });
    }
  }
  return { chips, fullyClosedChips, instances };
}

function prepare(
  events: MigrationEvent[],
  input: EvaluateInput,
  policy: NormalizedPolicy,
): Prepared {
  const infos: EventInfo[] = [];
  const byId = new Map<string, EventInfo>();
  const invalid: EventInfo[] = [];
  const duplicateConflicts: InvalidEvent[] = [];

  for (const event of events) {
    const info = extractEventInfo(
      event,
      input.verifier,
      input.requireSignatures ?? policy.requireSignatures,
    );
    if (info.invalid) invalid.push(info);
    const existing = byId.get(info.id);
    if (existing) {
      if (recordDigest(existing.raw) !== recordDigest(info.raw)) {
        duplicateConflicts.push({
          id: info.id,
          action: info.action,
          migrationId: info.migrationId,
          reasons: ["duplicate-event-id-with-different-payload"],
        });
      }
      continue;
    }
    byId.set(info.id, info);
    infos.push(info);
  }

  const missingCausalParents: string[] = [];
  for (const info of infos) {
    for (const parent of info.causalParents) {
      if (!byId.has(parent)) missingCausalParents.push(parent);
    }
  }

  return {
    infos: infos.sort(compareEvents),
    byId,
    invalid,
    duplicateConflicts,
    missingCausalParents: uniqueStrings(missingCausalParents).sort(),
  };
}

function causalClosure(
  ids: Iterable<string>,
  byId: Map<string, EventInfo>,
): { ids: string[]; missing: string[]; invalid: string[] } {
  const seen = new Set<string>();
  const missing = new Set<string>();
  const invalid = new Set<string>();
  const stack = [...ids];
  while (stack.length > 0) {
    const id = stack.pop();
    if (id === undefined || seen.has(id)) continue;
    seen.add(id);
    const info = byId.get(id);
    if (!info) {
      missing.add(id);
      continue;
    }
    if (info.invalid) invalid.add(id);
    for (const parent of info.causalParents) stack.push(parent);
  }
  return {
    ids: [...seen].sort(),
    missing: [...missing].sort(),
    invalid: [...invalid].sort(),
  };
}

function targetFromInfo(info: EventInfo): {
  chipId?: string;
  runtimeKey?: string;
} {
  return {
    chipId: firstDefined(
      info.targetChipId,
      info.action === "attest" ||
      info.action === "accept-transfer" ||
      info.action === "confirm-migration"
        ? info.chipId
        : undefined,
    ),
    runtimeKey: firstDefined(
      info.targetRuntimeKey,
      info.action === "confirm-migration" || info.action === "confirm-recovery"
        ? info.runtimeKey
        : undefined,
    ),
  };
}

function transactionMatchesInfo(info: EventInfo, tx: Transaction): boolean {
  if (info.migrationId !== undefined) return info.migrationId === tx.id;
  if (info.action === "accept-transfer") {
    const target = targetFromInfo(info);
    if (target.chipId && tx.targetChipId && target.chipId !== tx.targetChipId) {
      return false;
    }
    if (
      target.runtimeKey &&
      tx.targetRuntimeKey &&
      target.runtimeKey !== tx.targetRuntimeKey
    ) {
      return false;
    }
    if (info.generation !== undefined && info.generation !== tx.generation) {
      return false;
    }
    if (
      info.workloadId !== undefined &&
      info.workloadId !== tx.workloadId
    ) {
      return false;
    }
    return true;
  }
  if (
    info.action === "confirm-migration" ||
    info.action === "confirm-recovery"
  ) {
    if (info.generation !== undefined && info.generation !== tx.generation) {
      return false;
    }
    if (info.workloadId !== undefined && info.workloadId !== tx.workloadId) {
      return false;
    }
    const target = targetFromInfo(info);
    if (target.chipId && tx.targetChipId && target.chipId !== tx.targetChipId) {
      return false;
    }
    if (
      target.runtimeKey &&
      tx.targetRuntimeKey &&
      target.runtimeKey !== tx.targetRuntimeKey
    ) {
      return false;
    }
    // An unbound confirmation is accepted only when it carries at least one
    // explicit binding.  This prevents a confirmation from one concurrent
    // transaction being borrowed by another one.
    return (
      target.chipId !== undefined ||
      target.runtimeKey !== undefined ||
      info.generation !== undefined ||
      info.workloadId !== undefined
    );
  }
  return true;
}

function confirmationMatches(info: EventInfo, tx: Transaction): boolean {
  if (info.generation !== undefined && info.generation !== tx.generation) {
    return false;
  }
  if (info.workloadId !== undefined && info.workloadId !== tx.workloadId) {
    return false;
  }
  if (
    info.targetChipId !== undefined &&
    tx.targetChipId !== undefined &&
    info.targetChipId !== tx.targetChipId
  ) {
    return false;
  }
  const targetKey = firstDefined(
    info.targetRuntimeKey,
    info.action === "confirm-migration" || info.action === "confirm-recovery"
      ? info.runtimeKey
      : undefined,
  );
  if (
    targetKey !== undefined &&
    tx.targetRuntimeKey !== undefined &&
    targetKey !== tx.targetRuntimeKey
  ) {
    return false;
  }
  return true;
}

function buildTransactions(
  prepared: Prepared,
  workloadId: string,
  generation: number,
): Transaction[] {
  const ids = new Set<string>();
  for (const info of prepared.infos) {
    if (info.migrationId) ids.add(info.migrationId);
    if (
      !info.migrationId &&
      info.action === "accept-transfer" &&
      info.targetChipId &&
      info.targetRuntimeKey
    ) {
      ids.add(
        autoTargetId(
          workloadId,
          generation,
          info.targetChipId,
          info.targetRuntimeKey,
        ),
      );
    }
  }

  // An attestation can be the only record for a transaction.
  for (const info of prepared.infos) {
    if (!info.migrationId && info.action === "attest") {
      if (
        info.workloadId === workloadId &&
        (info.generation === undefined || info.generation === generation) &&
        info.chipId &&
        info.runtimeKey
      ) {
        const referenced = prepared.infos.some(
          (candidate) =>
            candidate.action === "accept-transfer" &&
            candidate.attestationId === info.id,
        );
        if (!referenced) {
          ids.add(
            autoTargetId(
              workloadId,
              generation,
              info.chipId,
              info.runtimeKey,
            ),
          );
        }
      }
    }
  }

  const transactions: Transaction[] = [];
  for (const id of [...ids].sort()) {
    const direct = prepared.infos.filter(
      (info) => info.migrationId === id && !info.invalid,
    );
    const tx: Transaction = {
      id,
      workloadId,
      generation,
      infos: direct,
      oldAttestations: [],
      deactivations: [],
      revocations: [],
      ownerConfirmations: [],
      recoveryConfirmations: [],
      transferCandidates: [],
      decision: {
        migrationId: id,
        workloadId,
        generation,
        status: "wait",
        target: null,
        releaseTo: null,
        dependencies: [],
        minimalDependencies: [],
        missing: [],
        missingEvidence: [],
        reasons: [],
        blockers: [],
        invalidEvents: [],
      },
      rank: Number.POSITIVE_INFINITY,
    };

    const transfers = direct
      .filter((info) => info.action === "accept-transfer")
      .sort(compareEvents);
    tx.transferCandidates.push(...transfers);
    tx.transfer = transfers[0];
    if (tx.transfer) {
      const target = targetFromInfo(tx.transfer);
      tx.targetChipId = target.chipId;
      tx.targetRuntimeKey = target.runtimeKey;
      tx.oldChipId = tx.transfer.oldChipId;
    }

    for (const info of direct) {
      if (info.action === "revoke-key") tx.revocations.push(info);
      if (info.action === "deactivate-instance") {
        tx.deactivations.push(info);
        if (!tx.oldChipId) tx.oldChipId = info.chipId;
      }
      if (info.action === "confirm-migration") {
        tx.ownerConfirmations.push(info);
        const target = targetFromInfo(info);
        if (!tx.targetChipId) tx.targetChipId = target.chipId;
        if (!tx.targetRuntimeKey) tx.targetRuntimeKey = target.runtimeKey;
      }
      if (info.action === "confirm-recovery") {
        tx.recoveryConfirmations.push(info);
        const target = targetFromInfo(info);
        if (!tx.targetChipId && target.chipId && target.chipId !== info.chipId) {
          tx.targetChipId = target.chipId;
        }
        if (!tx.targetRuntimeKey) tx.targetRuntimeKey = target.runtimeKey;
      }
      if (info.action === "attest") {
        const target = targetFromInfo(info);
        if (!tx.targetChipId) tx.targetChipId = info.chipId;
        if (!tx.targetRuntimeKey) tx.targetRuntimeKey = info.runtimeKey;
        if (info.chipId === tx.targetChipId) {
          const matchesBinding =
            (info.workloadId === undefined ||
              info.workloadId === tx.workloadId) &&
            (info.generation === undefined ||
              info.generation === tx.generation);
          if (matchesBinding) {
            tx.newAttestation = chooseLatestAttestation(
              tx.newAttestation,
              info,
            );
          } else if (!tx.newAttestation) {
            tx.newAttestation = info;
          }
        } else if (!tx.newAttestation && info.chipId !== tx.oldChipId) {
          tx.newAttestation = info;
        }
      }
    }

    // Unbound records are associated only through strong target bindings.
    const unboundOrdered = [...prepared.infos].sort((a, b) => {
      const rank = (info: EventInfo): number =>
        info.action === "accept-transfer"
          ? 0
          : info.action === "deactivate-instance"
            ? 1
            : info.action === "revoke-key"
              ? 2
              : info.action === "attest"
                ? 4
                : 3;
      return rank(a) - rank(b) || compareEvents(a, b);
    });
    for (const info of unboundOrdered) {
      if (info.invalid || info.migrationId !== undefined) continue;
      if (info.action === "accept-transfer") {
        const autoId =
          info.targetChipId && info.targetRuntimeKey
            ? autoTargetId(
                tx.workloadId,
                tx.generation,
                info.targetChipId,
                info.targetRuntimeKey,
              )
            : undefined;
        if (
          ((tx.targetChipId !== undefined ||
            tx.targetRuntimeKey !== undefined) &&
            transactionMatchesInfo(info, tx)) ||
          autoId === tx.id
        ) {
          tx.transferCandidates.push(info);
          tx.infos.push(info);
          const target = targetFromInfo(info);
          if (!tx.targetChipId) tx.targetChipId = target.chipId;
          if (!tx.targetRuntimeKey) tx.targetRuntimeKey = target.runtimeKey;
          if (!tx.oldChipId) tx.oldChipId = info.oldChipId;
        }
      } else if (info.action === "confirm-migration") {
        if (
          (tx.targetChipId !== undefined ||
            tx.targetRuntimeKey !== undefined) &&
          transactionMatchesInfo(info, tx)
        ) {
          tx.ownerConfirmations.push(info);
          tx.infos.push(info);
        }
      } else if (info.action === "confirm-recovery") {
        if (
          (tx.targetChipId !== undefined ||
            tx.targetRuntimeKey !== undefined) &&
          transactionMatchesInfo(info, tx)
        ) {
          tx.recoveryConfirmations.push(info);
          tx.infos.push(info);
        }
      } else if (info.action === "deactivate-instance") {
        if (
          info.workloadId === undefined ||
          info.workloadId === tx.workloadId
        ) {
          if (
            tx.oldChipId === undefined ||
            info.chipId === tx.oldChipId ||
            info.chipId === undefined
          ) {
            tx.deactivations.push(info);
            tx.infos.push(info);
            if (!tx.oldChipId) tx.oldChipId = info.chipId;
          }
        }
      } else if (info.action === "revoke-key") {
        if (
          (info.workloadId === undefined ||
            info.workloadId === tx.workloadId) &&
          (info.generation === undefined || info.generation === tx.generation)
        ) {
          tx.revocations.push(info);
          tx.infos.push(info);
        }
      } else if (info.action === "attest") {
        const referenced =
          tx.transfer?.attestationId === info.id ||
          (info.workloadId === tx.workloadId &&
            (info.generation === undefined ||
              info.generation === tx.generation) &&
            info.chipId === tx.targetChipId);
        if (referenced) {
          tx.infos.push(info);
          tx.newAttestation = chooseLatestAttestation(tx.newAttestation, info);
        }
      }
    }

    const referencedAttestationIds = new Set(
      tx.transferCandidates
        .map((candidate) => candidate.attestationId)
        .filter((id): id is string => id !== undefined),
    );
    for (const attestationId of referencedAttestationIds) {
      const referenced = prepared.byId.get(attestationId);
      if (referenced && referenced.action === "attest" && !referenced.invalid) {
        tx.newAttestation = chooseLatestAttestation(
          tx.newAttestation,
          referenced,
        );
        if (!tx.infos.includes(referenced)) tx.infos.push(referenced);
      }
    }
    if (!tx.newAttestation) {
      const candidates = prepared.infos.filter(
        (info) =>
          info.action === "attest" &&
          !info.invalid &&
          info.workloadId === workloadId &&
          (info.generation === undefined || info.generation === generation) &&
          info.chipId !== tx.oldChipId &&
          (tx.targetChipId === undefined || info.chipId === tx.targetChipId),
      );
      if (candidates.length === 1) {
        tx.newAttestation = candidates[0];
        if (!tx.infos.includes(candidates[0])) tx.infos.push(candidates[0]);
        if (!tx.targetChipId) tx.targetChipId = candidates[0].chipId;
        if (!tx.targetRuntimeKey) tx.targetRuntimeKey = candidates[0].runtimeKey;
      }
    }
    tx.newAttestation =
      chooseBestTargetAttestation(tx, prepared.infos, prepared.byId) ??
      tx.newAttestation;
    if (tx.newAttestation) {
      if (!tx.targetChipId && tx.newAttestation.chipId) {
        tx.targetChipId = tx.newAttestation.chipId;
      }
      if (
        tx.newAttestation.runtimeKey &&
        (tx.targetChipId === undefined ||
          tx.newAttestation.chipId === tx.targetChipId)
      ) {
        tx.targetRuntimeKey = tx.newAttestation.runtimeKey;
      }
    }
    tx.transferCandidates = sortUniqueEvents(tx.transferCandidates);
    tx.transfer = selectCurrentTransfer(tx);
    if (tx.transfer) {
      if (!tx.targetChipId) {
        tx.targetChipId = targetFromInfo(tx.transfer).chipId;
      }
      if (!tx.targetRuntimeKey) {
        tx.targetRuntimeKey = targetFromInfo(tx.transfer).runtimeKey;
      }
      if (!tx.oldChipId) tx.oldChipId = tx.transfer.oldChipId;
    }
    if (!tx.targetChipId && tx.newAttestation?.chipId) {
      tx.targetChipId = tx.newAttestation.chipId;
    }
    if (!tx.targetRuntimeKey && tx.newAttestation?.runtimeKey) {
      tx.targetRuntimeKey = tx.newAttestation.runtimeKey;
    }

    if (tx.targetChipId && tx.targetRuntimeKey) {
      tx.target = {
        migrationId: tx.id,
        workloadId,
        generation,
        chipId: tx.targetChipId,
        runtimeKey: tx.targetRuntimeKey,
        attestationId: tx.newAttestation?.id,
      };
    }

    if (!tx.oldChipId) {
      const candidates = prepared.infos.filter(
        (info) =>
          info.action === "attest" &&
          !info.invalid &&
          info.chipId !== undefined &&
          info.chipId !== tx.targetChipId &&
          info.workloadId === workloadId &&
          info.generation !== undefined &&
          info.generation <= generation,
      );
      const chips = [...new Set(candidates.map((info) => info.chipId as string))];
      if (chips.length === 1) tx.oldChipId = chips[0];
    }

    tx.oldAttestations = prepared.infos
      .filter(
        (info) =>
          info.action === "attest" &&
          !info.invalid &&
          tx.oldChipId !== undefined &&
          info.chipId === tx.oldChipId &&
          info.workloadId === workloadId &&
          info.id !== tx.newAttestation?.id,
      )
      .sort((a, b) => {
        const boot = (b.bootCount ?? -1) - (a.bootCount ?? -1);
        return boot !== 0 ? boot : compareEvents(a, b);
      });

    for (const attestation of tx.oldAttestations) {
      if (!tx.infos.includes(attestation)) tx.infos.push(attestation);
    }

    tx.ownerConfirmations = sortUniqueEvents(tx.ownerConfirmations);
    tx.recoveryConfirmations = sortUniqueEvents(tx.recoveryConfirmations);
    tx.deactivations = sortUniqueEvents(tx.deactivations);
    tx.revocations = sortUniqueEvents(tx.revocations);
    tx.infos = sortUniqueEvents(tx.infos);
    transactions.push(tx);
  }

  // An unbound attestation from the old host is evidence for an existing
  // migration, not a second transaction.  Use the synthetic id's intended
  // target rather than the possibly re-associated attestation: the old-side
  // record must not be mistaken for another new side just because a global
  // deactivation identified its chip.
  const intendedTarget = (
    tx: Transaction,
  ): { chipId?: string; runtimeKey?: string } => {
    const attestation = prepared.infos.find(
      (info) =>
        info.action === "attest" &&
        info.chipId !== undefined &&
        info.runtimeKey !== undefined &&
        autoTargetId(
          workloadId,
          generation,
          info.chipId,
          info.runtimeKey,
        ) === tx.id,
    );
    return attestation
      ? { chipId: attestation.chipId, runtimeKey: attestation.runtimeKey }
      : { chipId: tx.targetChipId, runtimeKey: tx.targetRuntimeKey };
  };
  return transactions.filter((tx) => {
    if (!tx.id.startsWith("auto:")) return true;
    if (!tx.newAttestation) return false;
    const intended = intendedTarget(tx);
    if (!intended.chipId) return false;
    return !transactions.some((candidate) => {
      if (candidate === tx) return false;
      if (candidate.oldChipId === intended.chipId) return true;
      const otherIntended = intendedTarget(candidate);
      return (
        otherIntended.chipId === intended.chipId &&
        otherIntended.runtimeKey === intended.runtimeKey &&
        (!candidate.id.startsWith("auto:") || candidate.id < tx.id)
      );
    });
  });
}

function chooseLatestAttestation(
  current: EventInfo | undefined,
  candidate: EventInfo,
): EventInfo {
  if (!current) return candidate;
  const currentBoot = current.bootCount ?? -1;
  const candidateBoot = candidate.bootCount ?? -1;
  if (candidateBoot > currentBoot) return candidate;
  if (candidateBoot < currentBoot) return current;
  return compareEvents(candidate, current) < 0 ? candidate : current;
}

function selectCurrentTransfer(tx: Transaction): EventInfo | undefined {
  if (tx.transferCandidates.length === 0) return undefined;
  const matching = tx.transferCandidates.filter((candidate) => {
    const target = targetFromInfo(candidate);
    if (
      tx.targetChipId !== undefined &&
      target.chipId !== undefined &&
      target.chipId !== tx.targetChipId
    ) {
      return false;
    }
    if (
      tx.targetRuntimeKey !== undefined &&
      target.runtimeKey !== undefined &&
      target.runtimeKey !== tx.targetRuntimeKey
    ) {
      return false;
    }
    if (
      tx.newAttestation !== undefined &&
      candidate.attestationId !== undefined &&
      candidate.attestationId !== tx.newAttestation.id
    ) {
      return false;
    }
    return (
      candidate.attestationId !== undefined ||
      target.chipId !== undefined ||
      target.runtimeKey !== undefined
    );
  });
  if (matching.length === 0) return undefined;
  return matching.sort(compareEvents)[matching.length - 1];
}

function causalDepth(
  id: string,
  byId: Map<string, EventInfo>,
  memo: Map<string, number>,
  visiting = new Set<string>(),
): number {
  const cached = memo.get(id);
  if (cached !== undefined) return cached;
  if (visiting.has(id)) return 0;
  const info = byId.get(id);
  if (!info || info.causalParents.length === 0) {
    memo.set(id, 0);
    return 0;
  }
  visiting.add(id);
  let depth = 0;
  for (const parent of info.causalParents) {
    depth = Math.max(depth, 1 + causalDepth(parent, byId, memo, visiting));
  }
  visiting.delete(id);
  memo.set(id, depth);
  return depth;
}

function recency(
  info: EventInfo,
  byId: Map<string, EventInfo>,
  depthMemo: Map<string, number>,
): { time: number; depth: number; id: string } {
  const time = eventTime(info);
  return {
    time: time === Number.MAX_SAFE_INTEGER ? -1 : time,
    depth: causalDepth(info.id, byId, depthMemo),
    id: info.id,
  };
}

function compareRecency(
  a: { time: number; depth: number; id: string },
  b: { time: number; depth: number; id: string },
): number {
  if (a.time !== b.time) return a.time - b.time;
  if (a.depth !== b.depth) return a.depth - b.depth;
  return a.id.localeCompare(b.id);
}

function chooseBestTargetAttestation(
  tx: Transaction,
  infos: EventInfo[],
  byId: Map<string, EventInfo>,
): EventInfo | undefined {
  if (tx.targetChipId === undefined) return tx.newAttestation;
  const depthMemo = new Map<string, number>();
  const candidates = infos.filter(
    (info) =>
      info.action === "attest" &&
      !info.invalid &&
      info.chipId === tx.targetChipId &&
      info.workloadId === tx.workloadId &&
      (info.generation === undefined || info.generation === tx.generation),
  );
  if (candidates.length === 0) return tx.newAttestation;

  const score = (info: EventInfo) => {
    const matchingTransfers = tx.transferCandidates.filter(
      (candidate) =>
        candidate.attestationId === info.id ||
        (targetFromInfo(candidate).runtimeKey !== undefined &&
          targetFromInfo(candidate).runtimeKey === info.runtimeKey),
    );
    const scores = [
      recency(info, byId, depthMemo),
      ...matchingTransfers.map((candidate) =>
        recency(candidate, byId, depthMemo),
      ),
    ];
    return scores.reduce((best, current) =>
      compareRecency(current, best) > 0 ? current : best,
    );
  };

  return [...candidates].sort((a, b) => {
    const boot = (b.bootCount ?? -1) - (a.bootCount ?? -1);
    if (boot !== 0) return boot;
    return compareRecency(score(b), score(a));
  })[0];
}

function sortUniqueEvents(events: EventInfo[]): EventInfo[] {
  const byId = new Map<string, EventInfo>();
  for (const event of events) {
    if (!byId.has(event.id)) byId.set(event.id, event);
  }
  return [...byId.values()].sort(compareEvents);
}

function duplicateNonceIds(infos: EventInfo[], policy: NormalizedPolicy): Set<string> {
  const invalid = new Set<string>();
  if (!policy.nonceUniqueness) return invalid;
  const groups = new Map<string, EventInfo[]>();
  for (const info of infos) {
    if (
      info.action !== "attest" ||
      info.chipId === undefined ||
      info.workloadId === undefined ||
      info.nonce === undefined
    ) {
      continue;
    }
    const key = `${info.chipId}\u0000${info.workloadId}\u0000${info.nonce}`;
    const group = groups.get(key);
    if (group) group.push(info);
    else groups.set(key, [info]);
  }
  for (const group of groups.values()) {
    if (group.length <= 1) continue;
    for (const info of group) invalid.add(info.id);
  }
  return invalid;
}

function validateAttestation(
  info: EventInfo,
  tx: Transaction,
  policy: NormalizedPolicy,
  baseline: TcbVector | undefined,
  duplicateNonces: Set<string>,
  expectedNonce: string | undefined,
): AttestationCheck {
  const reasons: string[] = [];
  const blockers: string[] = [];
  const reject = (reason: string): void => {
    if (!reasons.includes(reason)) reasons.push(reason);
    if (!blockers.includes(reason)) blockers.push(reason);
  };

  if (info.invalid) return { ok: false, status: "reject", reasons: info.invalidReasons, blockers: info.invalidReasons };
  if (info.missingBindings.length > 0) {
    reject("attestation-binding-missing");
    return {
      ok: false,
      status: "reject",
      reasons,
      blockers: uniqueStrings([...blockers, ...info.missingBindings.map((name) => `missing:${name}`)]),
    };
  }
  if (info.workloadId !== tx.workloadId) reject("workload-mismatch");
  if (info.generation !== undefined && info.generation !== tx.generation) {
    reject("generation-mismatch");
  }
  if (tx.targetChipId && info.chipId !== tx.targetChipId) {
    reject("target-chip-mismatch");
  }
  if (tx.targetRuntimeKey && info.runtimeKey !== tx.targetRuntimeKey) {
    reject("target-key-mismatch");
  }
  if (policy.digest && info.policyDigest !== policy.digest) {
    reject("policy-digest-mismatch");
  }
  if ((info.debug ?? false) && !policy.allowDebug) {
    reject("debug-enabled");
  }
  if (expectedNonce && info.nonce !== expectedNonce) {
    reject("nonce-mismatch");
  }
  if (duplicateNonces.has(info.id)) {
    reject("nonce-replay");
  }
  if (baseline) {
    const comparison = compareTcbVectors(info.tcb, baseline, {
      rules: policy.tcbRules,
      requireExactComponentSet: policy.requireExactComponentSet,
      allowExtraComponents: policy.allowExtraComponents,
    });
    if (comparison === "older" || comparison === "incomparable") {
      reject(`tcb-${comparison}`);
    }
  }
  return {
    ok: reasons.length === 0,
    status: reasons.length === 0 ? "release" : "reject",
    reasons,
    blockers,
  };
}

function transferMatches(
  tx: Transaction,
  transfer: EventInfo,
  attestation: EventInfo | undefined,
): AttestationCheck {
  const reasons: string[] = [];
  const blockers: string[] = [];
  const reject = (reason: string): void => {
    if (!reasons.includes(reason)) reasons.push(reason);
    if (!blockers.includes(reason)) blockers.push(reason);
  };
  if (transfer.invalid) {
    return {
      ok: false,
      status: "reject",
      reasons: transfer.invalidReasons,
      blockers: transfer.invalidReasons,
    };
  }
  if (transfer.action !== "accept-transfer") reject("action-not-accept-transfer");
  const target = targetFromInfo(transfer);
  if (
    tx.targetChipId &&
    target.chipId !== undefined &&
    target.chipId !== tx.targetChipId
  ) {
    reject("transfer-target-chip-mismatch");
  }
  if (
    tx.targetRuntimeKey &&
    target.runtimeKey !== undefined &&
    target.runtimeKey !== tx.targetRuntimeKey
  ) {
    reject("transfer-target-key-mismatch");
  }
  if (
    transfer.generation !== undefined &&
    transfer.generation !== tx.generation
  ) {
    reject("transfer-generation-mismatch");
  }
  if (
    transfer.workloadId !== undefined &&
    transfer.workloadId !== tx.workloadId
  ) {
    reject("transfer-workload-mismatch");
  }
  const explicitSignerChip = transfer.signerChipId;
  if (
    explicitSignerChip !== undefined &&
    tx.targetChipId !== undefined &&
    explicitSignerChip !== tx.targetChipId
  ) {
    reject("transfer-signer-chip-mismatch");
  }
  if (
    transfer.signer !== undefined &&
    tx.oldChipId !== undefined &&
    tx.targetChipId !== undefined &&
    transfer.signer === tx.oldChipId &&
    tx.oldChipId !== tx.targetChipId
  ) {
    reject("transfer-signed-by-old-chip");
  }
  if (attestation) {
    if (transfer.attestationId && transfer.attestationId !== attestation.id) {
      reject("transfer-attestation-id-mismatch");
    }
    if (transfer.attestationDigest) {
      const expected = new Set([
        attestationClaimsDigest(attestation),
        recordDigest(attestation.raw),
        sha256Hex(canonicalRecordBytes(attestation.raw)),
        sha256Hex(plainRecordJson(attestation.raw)),
        sha256Hex(canonicalJson(attestation.raw)),
        sha256Hex(canonicalJson(attestation.raw.payload ?? {})),
        sha256Hex(JSON.stringify(attestation.raw.payload ?? {})),
        sha256Hex(canonicalJson(attestation.data)),
        sha256Hex(JSON.stringify(attestation.data)),
        sha256Base64(canonicalRecordBytes(attestation.raw)),
        sha256Base64(plainRecordJson(attestation.raw)),
        sha256Base64(canonicalJson(attestation.raw)),
        sha256Base64(canonicalJson(attestation.raw.payload ?? {})),
        sha256Base64(JSON.stringify(attestation.raw.payload ?? {})),
        sha256Base64(canonicalJson(attestation.data)),
        sha256Base64(JSON.stringify(attestation.data)),
      ]);
      if (
        !expected.has(transfer.attestationDigest) &&
        !expected.has(transfer.attestationDigest.toLowerCase()) &&
        !expected.has(transfer.attestationDigest.toUpperCase())
      ) {
        reject("transfer-attestation-digest-mismatch");
      }
    }
    if (transfer.nonce && transfer.nonce !== attestation.nonce) {
      reject("transfer-nonce-mismatch");
    }
  } else if (transfer.attestationId) {
    reject("transfer-attestation-missing");
  }
  return {
    ok: reasons.length === 0,
    status: reasons.length === 0 ? "release" : "reject",
    reasons,
    blockers,
  };
}

function deactivationCovers(
  deactivation: EventInfo,
  attestation: EventInfo,
): boolean {
  if (
    deactivation.chipId !== undefined &&
    attestation.chipId !== undefined &&
    deactivation.chipId !== attestation.chipId
  ) {
    return false;
  }
  if (deactivation.bootCount === undefined || attestation.bootCount === undefined) {
    return false;
  }
  if (deactivation.bootCount > attestation.bootCount) return true;
  if (deactivation.bootCount < attestation.bootCount) return false;
  if (deactivation.runtimeKey === undefined) return true;
  return deactivation.runtimeKey === attestation.runtimeKey;
}

function closedCoversAttestation(
  manifest: ClosedManifest,
  attestation: EventInfo,
): boolean {
  if (attestation.chipId === undefined) return false;
  if (manifest.fullyClosedChips.has(attestation.chipId)) return true;
  if (attestation.bootCount === undefined) return false;
  return manifest.instances.some(
    (instance) =>
      instance.chipId === attestation.chipId &&
      instance.bootCount >= attestation.bootCount!,
  );
}

function selectDeactivationSet(
  deactivations: EventInfo[],
  attestations: EventInfo[],
  closedAllowed: boolean,
  manifest: ClosedManifest,
): { covered: boolean; selected: EventInfo[]; uncovered: EventInfo[] } {
  const selected: EventInfo[] = [];
  const uncovered: EventInfo[] = [];
  for (const attestation of attestations) {
    if (closedAllowed && closedCoversAttestation(manifest, attestation)) continue;
    if (deactivations.some((event) => deactivationCovers(event, attestation))) {
      continue;
    }
    uncovered.push(attestation);
  }

  // Pick a deterministic, minimum-sized cover.  Highest boot count first is
  // optimal for the usual monotonic boot counter case; same-boot key rotations
  // require one deactivation per distinct key.
  const remaining = attestations.filter(
    (attestation) =>
      !(closedAllowed && closedCoversAttestation(manifest, attestation)),
  );
  while (remaining.length > 0) {
    let best: EventInfo | undefined;
    let bestCovered: EventInfo[] = [];
    for (const deactivation of deactivations) {
      const covered = remaining.filter((attestation) =>
        deactivationCovers(deactivation, attestation),
      );
      if (
        covered.length > bestCovered.length ||
        (covered.length === bestCovered.length &&
          covered.length > 0 &&
          best !== undefined &&
          compareEvents(deactivation, best) < 0)
      ) {
        best = deactivation;
        bestCovered = covered;
      }
    }
    if (!best || bestCovered.length === 0) break;
    selected.push(best);
    const coveredIds = new Set(bestCovered.map((item) => item.id));
    for (let index = remaining.length - 1; index >= 0; index -= 1) {
      if (coveredIds.has(remaining[index].id)) remaining.splice(index, 1);
    }
  }

  if (
    selected.length === 0 &&
    attestations.length === 0 &&
    !closedAllowed &&
    deactivations.length > 0
  ) {
    const latest = [...deactivations].sort(
      (a, b) =>
        (b.bootCount ?? -1) - (a.bootCount ?? -1) || compareEvents(a, b),
    )[0];
    if (latest) selected.push(latest);
  }

  return {
    covered: uncovered.length === 0,
    selected: sortUniqueEvents(selected),
    uncovered,
  };
}

function revocationMatches(
  revocation: EventInfo,
  key: string,
  tx: Transaction,
): boolean {
  if (revocation.action !== "revoke-key") return false;
  if (revocation.runtimeKey !== key && revocation.oldRuntimeKey !== key) {
    return false;
  }
  if (
    revocation.workloadId !== undefined &&
    revocation.workloadId !== tx.workloadId
  ) {
    return false;
  }
  if (
    revocation.generation !== undefined &&
    revocation.generation > tx.generation
  ) {
    return false;
  }
  if (
    revocation.migrationId !== undefined &&
    revocation.migrationId !== tx.id
  ) {
    return false;
  }
  return true;
}

function selectOwnerConfirmations(
  confirmations: EventInfo[],
  threshold: number,
): ConfirmationSelection {
  if (threshold <= 0) {
    return { satisfied: true, selected: [], chipCount: 0, domainCount: 0 };
  }
  const byDomain = new Map<string, EventInfo>();
  const seenCertificates = new Set<string>();
  for (const confirmation of [...confirmations].sort(compareEvents)) {
    if (
      confirmation.certificateId !== undefined &&
      seenCertificates.has(confirmation.certificateId)
    ) {
      continue;
    }
    const domain =
      confirmation.adminDomainId ??
      confirmation.ownerId ??
      confirmation.signer ??
      `event:${confirmation.id}`;
    if (!byDomain.has(domain)) {
      byDomain.set(domain, confirmation);
      if (confirmation.certificateId !== undefined) {
        seenCertificates.add(confirmation.certificateId);
      }
    }
  }
  const selected = [...byDomain.values()]
    .sort((a, b) => compareEvents(a, b))
    .slice(0, threshold);
  return {
    satisfied: selected.length >= threshold,
    selected,
    chipCount: selected.length,
    domainCount: selected.length,
  };
}

function selectRecoveryConfirmations(
  confirmations: EventInfo[],
  deviceThreshold: number,
  domainThreshold: number,
  requireBoth = true,
): ConfirmationSelection {
  const positiveThresholds = [deviceThreshold, domainThreshold].filter(
    (threshold) => threshold > 0,
  );
  const required = requireBoth
    ? Math.max(deviceThreshold, domainThreshold, 0)
    : positiveThresholds.length > 0
      ? Math.min(...positiveThresholds)
      : 0;
  if (required === 0) {
    return { satisfied: true, selected: [], chipCount: 0, domainCount: 0 };
  }
  interface Edge {
    info: EventInfo;
    chip: string;
    domain: string;
  }
  const edges: Edge[] = [];
  const seenPairs = new Set<string>();
  const seenCertificates = new Set<string>();
  for (const confirmation of [...confirmations].sort(compareEvents)) {
    const chip =
      confirmation.chipId ??
      confirmation.certificateId ??
      `chip-from:${confirmation.id}`;
    const domain =
      confirmation.adminDomainId ??
      confirmation.ownerId ??
      confirmation.signer ??
      `domain-from:${confirmation.id}`;
    const pair = `${chip}\u0000${domain}`;
    if (seenPairs.has(pair)) continue;
    if (
      confirmation.certificateId !== undefined &&
      seenCertificates.has(confirmation.certificateId)
    ) {
      continue;
    }
    seenPairs.add(pair);
    if (confirmation.certificateId !== undefined) {
      seenCertificates.add(confirmation.certificateId);
    }
    edges.push({ info: confirmation, chip, domain });
  }

  const edgesByChip = new Map<string, Edge[]>();
  for (const edge of edges) {
    const list = edgesByChip.get(edge.chip);
    if (list) list.push(edge);
    else edgesByChip.set(edge.chip, [edge]);
  }

  // Maximum bipartite matching between physical chips and admin domains.
  // A greedy pass is not enough: (chip-1, domain-a), (chip-1, domain-b),
  // (chip-2, domain-a) has a valid two-record matching, but greedy selection
  // would lock both dimensions on the first record.
  const domainOwner = new Map<string, Edge>();
  const tryAugment = (chip: string, seenDomains: Set<string>): boolean => {
    const candidates = edgesByChip.get(chip) ?? [];
    for (const edge of candidates) {
      if (seenDomains.has(edge.domain)) continue;
      seenDomains.add(edge.domain);
      const owner = domainOwner.get(edge.domain);
      if (!owner || tryAugment(owner.chip, seenDomains)) {
        domainOwner.set(edge.domain, edge);
        return true;
      }
    }
    return false;
  };

  for (const chip of [...edgesByChip.keys()].sort()) {
    tryAugment(chip, new Set());
  }

  const matched = [...domainOwner.values()].sort((a, b) =>
    compareEvents(a.info, b.info),
  );
  const selected = matched.slice(0, required).map((edge) => edge.info);
  const satisfied = requireBoth
    ? selected.length >= deviceThreshold && selected.length >= domainThreshold
    : positiveThresholds.length === 0 ||
      positiveThresholds.some((threshold) => selected.length >= threshold);
  return {
    satisfied,
    selected,
    chipCount: selected.length,
    domainCount: selected.length,
  };
}

function relevantInvalidEvents(
  prepared: Prepared,
  tx: Transaction,
): InvalidEvent[] {
  const result: InvalidEvent[] = [];
  for (const info of prepared.invalid) {
    const sameMigration =
      info.migrationId !== undefined && info.migrationId === tx.id;
    const sameTarget =
      firstDefined(info.targetChipId, info.chipId) !== undefined &&
      tx.targetChipId !== undefined &&
      firstDefined(info.targetChipId, info.chipId) === tx.targetChipId;
    const sameOldChip =
      info.chipId !== undefined &&
      tx.oldChipId !== undefined &&
      info.chipId === tx.oldChipId;
    const sameOldChipGlobal = sameOldChip && info.migrationId === undefined;
    const sameWorkload =
      info.workloadId === undefined || info.workloadId === tx.workloadId;
    if ((sameMigration || sameTarget || sameOldChipGlobal) && sameWorkload) {
      result.push({
        id: info.id,
        action: info.action,
        migrationId: info.migrationId,
        reasons: info.invalidReasons,
      });
    }
  }
  for (const conflict of prepared.duplicateConflicts) {
    if (
      conflict.migrationId === undefined ||
      conflict.migrationId === tx.id
    ) {
      result.push(conflict);
    }
  }
  return result.sort((a, b) => a.id.localeCompare(b.id));
}

function evaluateTransaction(
  tx: Transaction,
  prepared: Prepared,
  policy: NormalizedPolicy,
  manifest: ClosedManifest,
  duplicateNonces: Set<string>,
  expectedNonce: string | undefined,
  baselineOverride: TcbVector | undefined,
  leases: Lease[],
): void {
  const reasons: string[] = [];
  const blockers: string[] = [];
  const missing: MissingEvidence[] = [];
  const dependencies = new Set<string>();
  let status: Status = "release";
  let transferValid = false;
  let deactivationCovered = false;
  let revocationComplete = false;
  let confirmationComplete = false;

  const invalid = relevantInvalidEvents(prepared, tx);
  if (invalid.length > 0) {
    status = "reject";
    for (const item of invalid) {
      dependencies.add(item.id);
      for (const reason of item.reasons) addReason(reasons, `invalid-event:${reason}`);
    }
    blockers.push("invalid-event");
  }
  const replayedOldAttestations = tx.oldAttestations.filter((info) =>
    duplicateNonces.has(info.id),
  );
  if (replayedOldAttestations.length > 0) {
    status = "reject";
    addReason(reasons, "nonce-replay");
    blockers.push("nonce-replay");
    for (const replay of replayedOldAttestations) {
      dependencies.add(replay.id);
    }
  }
  const debugOldAttestations = tx.oldAttestations.filter(
    (info) => info.debug === true && !policy.allowDebug,
  );
  if (debugOldAttestations.length > 0) {
    status = "reject";
    addReason(reasons, "old-debug-enabled");
    blockers.push("old-debug-enabled");
    for (const attestation of debugOldAttestations) {
      dependencies.add(attestation.id);
    }
  }

  if (!tx.newAttestation) {
    if (status !== "reject") status = "wait";
    addMissing(missing, "attest", "missing-new-attestation");
    addReason(reasons, "missing-new-attestation");
  } else {
    const oldBaselines = tx.oldAttestations
      .map((info) => info.tcb)
      .filter((tcb): tcb is TcbVector => tcb !== undefined);
    const baselines = uniqueTcbBaselines([
      ...(baselineOverride ? [baselineOverride] : []),
      ...(policy.baselineTcb ? [policy.baselineTcb] : []),
      ...oldBaselines,
    ]);
    const checks: AttestationCheck[] = [];
    for (const baseline of baselines) {
      checks.push(
        validateAttestation(
          tx.newAttestation,
          tx,
          policy,
          baseline,
          duplicateNonces,
          expectedNonce,
        ),
      );
    }
    if (checks.length === 0) {
      checks.push(
        validateAttestation(
          tx.newAttestation,
          tx,
          policy,
          undefined,
          duplicateNonces,
          expectedNonce,
        ),
      );
    }
    for (const check of checks) {
      if (!check.ok) {
        status = "reject";
        if (tx.newAttestation) dependencies.add(tx.newAttestation.id);
        for (const reason of check.reasons) addReason(reasons, reason);
        for (const blocker of check.blockers) {
          if (!blockers.includes(blocker)) blockers.push(blocker);
        }
      }
    }
    if (checks.every((check) => check.ok)) dependencies.add(tx.newAttestation.id);
    if (
      !baselineOverride &&
      !policy.baselineTcb &&
      oldBaselines.length > 0
    ) {
      for (const oldAttestation of tx.oldAttestations) {
        if (oldAttestation.tcb !== undefined) {
          dependencies.add(oldAttestation.id);
        }
      }
    }
  }

  if (tx.transfer) {
    const check = transferMatches(tx, tx.transfer, tx.newAttestation);
    if (check.ok) {
      transferValid = true;
      dependencies.add(tx.transfer.id);
    } else {
      status = "reject";
      dependencies.add(tx.transfer.id);
      for (const reason of check.reasons) addReason(reasons, reason);
      for (const blocker of check.blockers) {
        if (!blockers.includes(blocker)) blockers.push(blocker);
      }
    }
  } else {
    if (status !== "reject") status = "wait";
    if (tx.transferCandidates.length > 0) {
      addMissing(
        missing,
        "accept-transfer",
        "transfer-does-not-bind-current-target",
      );
      addReason(reasons, "transfer-does-not-bind-current-target");
      for (const candidate of tx.transferCandidates) {
        dependencies.add(candidate.id);
      }
    } else {
      addMissing(missing, "accept-transfer", "missing-transfer-acceptance");
      addReason(reasons, "missing-transfer-acceptance");
    }
  }

  if (tx.newAttestation && tx.targetChipId) {
    const selected = tx.newAttestation;
    const rotations = prepared.infos.filter(
      (info) => {
        if (
          info.action !== "attest" ||
          info.invalid ||
          info.id === selected.id ||
          info.chipId !== tx.targetChipId ||
          info.workloadId !== tx.workloadId ||
          (info.generation !== undefined &&
            info.generation !== tx.generation) ||
          info.runtimeKey === undefined ||
          info.runtimeKey === selected.runtimeKey ||
          (info.bootCount ?? -1) < (selected.bootCount ?? -1)
        ) {
          return false;
        }
        const competingTransfers = tx.transferCandidates.filter(
          (candidate) =>
            candidate.attestationId === info.id ||
            targetFromInfo(candidate).runtimeKey === info.runtimeKey,
        );
        if (
          tx.transfer !== undefined &&
          competingTransfers.length > 0 &&
          competingTransfers.every(
            (candidate) => compareEvents(candidate, tx.transfer!) < 0,
          )
        ) {
          return false;
        }
        return true;
      },
    );
    if (rotations.length > 0) {
      if (status !== "reject") status = "wait";
      addReason(reasons, "target-key-rotated");
      blockers.push("target-key-rotated");
      for (const rotation of rotations) dependencies.add(rotation.id);
    }
  }

  if (
    tx.targetChipId !== undefined &&
    tx.oldChipId !== tx.targetChipId &&
    manifest.chips.has(tx.targetChipId)
  ) {
    status = "reject";
    addReason(reasons, "target-device-closed");
    blockers.push("target-device-closed");
  }

  if (
    tx.oldChipId !== undefined &&
    tx.oldChipId === tx.targetChipId &&
    tx.newAttestation?.bootCount !== undefined
  ) {
    const maxOldBoot = tx.oldAttestations.reduce<number | undefined>(
      (max, attestation) =>
        attestation.bootCount === undefined
          ? max
          : max === undefined
            ? attestation.bootCount
            : Math.max(max, attestation.bootCount),
      undefined,
    );
    if (
      maxOldBoot !== undefined &&
      tx.newAttestation.bootCount <= maxOldBoot
    ) {
      status = "reject";
      addReason(reasons, "instance-boot-rollback");
      blockers.push("instance-boot-rollback");
    }
  }

  if (!tx.oldChipId) {
    if (status !== "reject") status = "wait";
    addMissing(missing, "deactivate-instance", "missing-old-chip-identity");
    addReason(reasons, "missing-old-chip-identity");
  }

  const matchingDeactivations = tx.deactivations.filter((info) => {
    if (info.bootCount === undefined) return false;
    if (
      info.workloadId !== undefined &&
      info.workloadId !== tx.workloadId
    ) {
      return false;
    }
    if (tx.oldChipId && info.chipId && info.chipId !== tx.oldChipId) {
      return false;
    }
    if (info.generation !== undefined && info.generation > tx.generation) {
      return false;
    }
    const signerChip = info.signerChipId;
    if (
      signerChip !== undefined &&
      tx.oldChipId !== undefined &&
      signerChip !== tx.oldChipId
    ) {
      return false;
    }
    if (
      info.signer !== undefined &&
      tx.targetChipId !== undefined &&
      tx.oldChipId !== undefined &&
      info.signer === tx.targetChipId &&
      tx.targetChipId !== tx.oldChipId
    ) {
      return false;
    }
    return true;
  });

  const closedAllowed =
    policy.allowClosedDeviceAsDeactivation &&
    tx.oldChipId !== undefined &&
    manifest.chips.has(tx.oldChipId);

  if (
    policy.requireOldDeviceClosed &&
    tx.oldChipId !== undefined &&
    !manifest.chips.has(tx.oldChipId)
  ) {
    if (status !== "reject") status = "wait";
    addMissing(missing, "closed-device", "old-device-not-in-closed-manifest");
    addReason(reasons, "old-device-not-in-closed-manifest");
  }

  const deactivationSelection = selectDeactivationSet(
    matchingDeactivations,
    tx.oldAttestations,
    closedAllowed,
    manifest,
  );
  if (!deactivationSelection.covered) {
    if (status !== "reject") status = "wait";
    for (const attestation of deactivationSelection.uncovered) {
      dependencies.add(attestation.id);
      const maxDeactivation = matchingDeactivations.reduce<number | undefined>(
        (max, event) =>
          event.bootCount === undefined
            ? max
            : max === undefined
              ? event.bootCount
              : Math.max(max, event.bootCount),
        undefined,
      );
      if (
        maxDeactivation !== undefined &&
        attestation.bootCount !== undefined &&
        attestation.bootCount > maxDeactivation
      ) {
        addReason(reasons, "old-instance-resurrected");
      } else if (
        matchingDeactivations.some(
          (event) =>
            event.bootCount === attestation.bootCount &&
            event.runtimeKey !== undefined &&
            event.runtimeKey !== attestation.runtimeKey,
        )
      ) {
        addReason(reasons, "old-instance-key-rotated");
      } else {
        addReason(reasons, "old-instance-not-stopped");
      }
    }
    addMissing(missing, "deactivate-instance", "old-instance-not-stopped");
  }

  if (policy.requireDeactivation) {
    if (matchingDeactivations.length === 0 && !closedAllowed) {
      if (status !== "reject") status = "wait";
      addMissing(missing, "deactivate-instance", "missing-deactivation");
      addReason(reasons, "missing-deactivation");
    } else if (matchingDeactivations.length > 0) {
      deactivationCovered = deactivationSelection.covered;
      for (const event of deactivationSelection.selected) {
        dependencies.add(event.id);
      }
    } else {
      deactivationCovered = true;
    }
  } else {
    deactivationCovered = true;
  }

  const oldKeys = new Set<string>();
  if (tx.transfer?.oldRuntimeKey) oldKeys.add(tx.transfer.oldRuntimeKey);
  for (const attestation of tx.oldAttestations) {
    if (attestation.runtimeKey) oldKeys.add(attestation.runtimeKey);
  }
  for (const deactivation of matchingDeactivations) {
    if (deactivation.runtimeKey) oldKeys.add(deactivation.runtimeKey);
  }

  if (tx.targetRuntimeKey) {
    const targetRevocations = tx.revocations.filter(
      (revocation) =>
        (revocation.runtimeKey === tx.targetRuntimeKey ||
          revocation.oldRuntimeKey === tx.targetRuntimeKey) &&
        (revocation.migrationId === undefined ||
          revocation.migrationId === tx.id) &&
        (revocation.workloadId === undefined ||
          revocation.workloadId === tx.workloadId) &&
        (revocation.generation === undefined ||
          revocation.generation <= tx.generation),
    );
    if (targetRevocations.length > 0) {
      status = "reject";
      addReason(reasons, "target-key-revoked");
      blockers.push("target-key-revoked");
      for (const revocation of targetRevocations) dependencies.add(revocation.id);
    }
  }

  // A historical transaction stops being a current release candidate when a
  // *different* migration deactivates its target instance or revokes its
  // target key.  This is what makes a chain of migrations progress instead of
  // repeatedly re-winning the oldest completed transaction.
  if (tx.targetChipId && tx.targetRuntimeKey) {
    const superseding = prepared.infos.filter((info) => {
      if (info.invalid) return false;
      if (info.migrationId === tx.id) return false;
      if (
        info.workloadId !== undefined &&
        info.workloadId !== tx.workloadId
      ) {
        return false;
      }
      if (info.action === "revoke-key") {
        return (
          (info.runtimeKey === tx.targetRuntimeKey ||
            info.oldRuntimeKey === tx.targetRuntimeKey) &&
          (info.generation === undefined ||
            info.generation <= tx.generation)
        );
      }
      if (info.action === "deactivate-instance") {
        if (info.chipId !== tx.targetChipId) return false;
        if (info.bootCount === undefined) return false;
        const targetBoot = tx.newAttestation?.bootCount;
        if (targetBoot !== undefined && info.bootCount < targetBoot) {
          return false;
        }
        if (
          info.runtimeKey !== undefined &&
          info.runtimeKey !== tx.targetRuntimeKey
        ) {
          return false;
        }
        return true;
      }
      return false;
    });
    if (superseding.length > 0) {
      if (status !== "reject") status = "wait";
      addReason(reasons, "target-superseded-by-later-migration");
      blockers.push("target-superseded-by-later-migration");
      for (const evidence of superseding) dependencies.add(evidence.id);
    }
  }

  for (const key of [...oldKeys].sort()) {
    if (tx.targetRuntimeKey && key === tx.targetRuntimeKey) {
      status = "reject";
      addReason(reasons, "target-key-equals-old-key");
      blockers.push("target-key-equals-old-key");
      continue;
    }
    const matches = tx.revocations.filter((revocation) =>
      revocationMatches(revocation, key, tx),
    );
    if (matches.length === 0) {
      if (policy.requireKeyRevocation) {
        if (status !== "reject") status = "wait";
        addMissing(missing, "revoke-key", `missing-revocation:${key}`);
        addReason(reasons, "missing-old-key-revocation");
      }
    } else {
      dependencies.add(matches[0].id);
    }
  }
  if (oldKeys.size === 0 && policy.requireKeyRevocation) {
    if (status !== "reject") status = "wait";
    addMissing(missing, "revoke-key", "missing-old-key-identity");
    addReason(reasons, "missing-old-key-identity");
  }
  revocationComplete =
    oldKeys.size === 0
      ? !policy.requireKeyRevocation
      : [...oldKeys].every((key) =>
          tx.revocations.some((revocation) =>
            revocationMatches(revocation, key, tx),
          ),
        );
  if (policy.requireKeyRevocation && !revocationComplete) {
    if (status !== "reject") status = "wait";
  }

  const ownerSelection = selectOwnerConfirmations(
    tx.ownerConfirmations.filter((info) => confirmationMatches(info, tx)),
    policy.ownerThreshold,
  );
  const matchingRecoveryConfirmations = tx.recoveryConfirmations.filter(
    (info) => confirmationMatches(info, tx),
  );
  const recoverySelection = policy.recoveryEnabled
    ? selectRecoveryConfirmations(
        matchingRecoveryConfirmations,
        policy.recoveryDeviceThreshold,
        policy.recoveryDomainThreshold,
        policy.recoveryRequireBoth,
      )
    : {
        satisfied: false,
        selected: [],
        chipCount: 0,
        domainCount: 0,
        reason: "recovery-disabled",
      };

  const recoveryConfigured =
    policy.recoveryEnabled &&
    (policy.recoveryDeviceThreshold > 0 || policy.recoveryDomainThreshold > 0);
  const ownerPathSatisfied =
    policy.ownerThreshold > 0
      ? ownerSelection.satisfied
      : !recoveryConfigured;
  const recoveryPathSatisfied =
    recoveryConfigured && recoverySelection.satisfied;

  if (!policy.requireOwnerConfirmation) {
    confirmationComplete = true;
  } else if (ownerPathSatisfied || recoveryPathSatisfied) {
    confirmationComplete = true;
    const selected =
      ownerPathSatisfied && recoveryPathSatisfied
        ? ownerSelection.selected.length <= recoverySelection.selected.length
          ? ownerSelection.selected
          : recoverySelection.selected
        : ownerPathSatisfied
          ? ownerSelection.selected
          : recoverySelection.selected;
    for (const confirmation of selected) dependencies.add(confirmation.id);
  } else {
    if (status !== "reject") status = "wait";
    if (recoveryConfigured) {
      addMissing(missing, "confirm-recovery", "insufficient-recovery-confirmations", {
        chips: recoverySelection.chipCount,
        domains: recoverySelection.domainCount,
        requiredChips: policy.recoveryDeviceThreshold,
        requiredDomains: policy.recoveryDomainThreshold,
      });
      addReason(reasons, "insufficient-recovery-confirmations");
    } else {
      addMissing(missing, "confirm-migration", "insufficient-owner-confirmations", {
        domains: ownerSelection.domainCount,
        required: policy.ownerThreshold,
      });
      addReason(reasons, "insufficient-owner-confirmations");
    }
    for (const confirmation of [
      ...ownerSelection.selected,
      ...recoverySelection.selected,
    ]) {
      dependencies.add(confirmation.id);
    }
  }

  if (tx.targetRuntimeKey) {
    const activeLease = leases
      .filter(
        (lease) =>
          lease.workloadId === tx.workloadId &&
          lease.generation === tx.generation,
      )
      .sort((a, b) => (a.releasedAt ?? 0) - (b.releasedAt ?? 0))
      .pop();
    if (activeLease) {
      const leaseConflicts =
        activeLease.runtimeKey !== tx.targetRuntimeKey ||
        (activeLease.chipId !== undefined &&
          tx.targetChipId !== undefined &&
          activeLease.chipId !== tx.targetChipId);
      const leaseKeyRevoked = oldKeys.has(activeLease.runtimeKey) &&
        tx.revocations.some((revocation) =>
          revocationMatches(revocation, activeLease.runtimeKey, tx),
        );
      if (leaseConflicts && !leaseKeyRevoked) {
        status = "reject";
        addReason(reasons, "existing-generation-lease-conflict");
        blockers.push("existing-generation-lease-conflict");
      }
    }
  }

  const closure = causalClosure(dependencies, prepared.byId);
  if (closure.missing.length > 0) {
    if (status !== "reject") status = "wait";
    addMissing(missing, "causal-prefix", "missing-causal-parents", {
      missing: closure.missing,
    });
    addReason(reasons, "missing-causal-parents");
  }
  if (closure.invalid.length > 0) {
    status = "reject";
    addReason(reasons, "invalid-causal-parent");
    blockers.push("invalid-causal-parent");
  }

  if (status === "release") {
    if (!tx.newAttestation) status = "wait";
    else if (!transferValid) status = "wait";
    else if (policy.requireDeactivation && !deactivationCovered) status = "wait";
    else if (policy.requireKeyRevocation && !revocationComplete) status = "wait";
    else if (policy.requireOwnerConfirmation && !confirmationComplete) status = "wait";
    else if (!tx.targetChipId || !tx.targetRuntimeKey) {
      status = "wait";
      addMissing(missing, "target-binding", "missing-target-binding");
      addReason(reasons, "missing-target-binding");
    }
  }

  const dependencyIds = [...dependencies].sort();
  tx.decision = {
    migrationId: tx.id,
    workloadId: tx.workloadId,
    generation: tx.generation,
    status,
    target: tx.target ?? null,
    releaseTo: tx.target ?? null,
    dependencies: dependencyIds,
    minimalDependencies: dependencyIds,
    missing: uniqueStrings(missing.map((item) => item.action)),
    missingEvidence: missing,
    reasons,
    blockers: uniqueStrings(blockers),
    invalidEvents: invalid,
    evidence: sortUniqueEvents(tx.infos).map((info) => info.id),
    requiredRecords: dependencyIds,
    causalDependencies: closure.ids,
    dependencyRecords: dependencyIds
      .map((id) => prepared.byId.get(id)?.raw)
      .filter((record): record is MigrationEvent => record !== undefined),
  };
  tx.rank = dependencyIds.reduce((minimum, id) => {
    const info = prepared.byId.get(id);
    return info ? Math.min(minimum, eventTime(info)) : minimum;
  }, Number.MAX_SAFE_INTEGER);
}

function uniqueTcbBaselines(vectors: TcbVector[]): TcbVector[] {
  const byDigest = new Map<string, TcbVector>();
  for (const vector of vectors) {
    byDigest.set(sha256Hex(canonicalJson(vector)), vector);
  }
  return [...byDigest.values()];
}

function emptyDecision(
  workloadId: string,
  generation: number,
): MigrationDecision {
  return {
    migrationId: "",
    workloadId,
    generation,
    status: "wait",
    target: null,
    releaseTo: null,
    dependencies: [],
    minimalDependencies: [],
    missing: [],
    missingEvidence: [],
    reasons: [],
    blockers: [],
    invalidEvents: [],
    evidence: [],
    requiredRecords: [],
  };
}

function chooseWinner(transactions: Transaction[]): Transaction | undefined {
  const releases = transactions
    .filter((tx) => tx.decision.status === "release")
    .sort((a, b) => {
      if (a.rank !== b.rank) return a.rank - b.rank;
      return a.id.localeCompare(b.id);
    });
  return releases[0];
}

function applyConcurrency(
  transactions: Transaction[],
): Array<{ migrationId: string; conflictingMigrationId: string }> {
  const conflicts: Array<{
    migrationId: string;
    conflictingMigrationId: string;
  }> = [];
  const releases = transactions.filter((tx) => tx.decision.status === "release");
  if (releases.length <= 1) return conflicts;

  const winner = chooseWinner(transactions);
  if (!winner) return conflicts;
  for (const tx of releases) {
    if (tx === winner) continue;
    if (
      tx.decision.target?.runtimeKey !== winner.decision.target?.runtimeKey ||
      tx.decision.target?.chipId !== winner.decision.target?.chipId
    ) {
      tx.decision.status = "reject";
      tx.decision.conflictingMigrationId = winner.id;
      tx.decision.reasons.push("concurrent-migration-conflict");
      tx.decision.blockers.push("concurrent-migration-conflict");
      conflicts.push({
        migrationId: tx.id,
        conflictingMigrationId: winner.id,
      });
    } else {
      // Same key on the same chip is one capability, not two.  Keep the
      // deterministic winner and mark the duplicate as waiting rather than
      // pretending a second release occurred.
      tx.decision.status = "wait";
      tx.decision.conflictingMigrationId = winner.id;
      tx.decision.reasons.push("duplicate-target-migration");
      tx.decision.blockers.push("duplicate-target-migration");
    }
  }
  return conflicts;
}

function makeReleaseDecision(
  workloadId: string,
  generation: number,
  prepared: Prepared,
  transactions: Transaction[],
  conflicts: Array<{ migrationId: string; conflictingMigrationId: string }>,
  leases: Lease[] = [],
): ReleaseDecision {
  if (prepared.missingCausalParents.length > 0) {
    for (const tx of transactions) {
      if (tx.decision.status !== "release") continue;
      tx.decision.status = "wait";
      addReason(tx.decision.reasons, "missing-causal-parents");
      if (!tx.decision.missing.includes("causal-prefix")) {
        tx.decision.missing.push("causal-prefix");
      }
      tx.decision.missingEvidence.push({
        action: "causal-prefix",
        reason: "missing-causal-parents",
        detail: { missing: prepared.missingCausalParents },
      });
    }
  }
  const winner = chooseWinner(transactions);
  const chosen =
    winner ??
    [...transactions].sort((a, b) => {
      const status = statusRank(b.decision.status) - statusRank(a.decision.status);
      if (status !== 0) return status;
      if (a.rank !== b.rank) return a.rank - b.rank;
      return a.id.localeCompare(b.id);
    })[0];
  const base = chosen?.decision ?? emptyDecision(workloadId, generation);

  const migrations = transactions
    .map((tx) => tx.decision)
    .sort((a, b) => a.migrationId.localeCompare(b.migrationId));
  const released = winner?.decision.target
    ? [winner.decision.target]
    : [];
  const allInvalidEvents = [
    ...new Map(
      [
        ...transactions.flatMap((tx) => tx.decision.invalidEvents),
        ...prepared.invalid.map((info) => ({
          id: info.id,
          action: info.action,
          migrationId: info.migrationId,
          reasons: info.invalidReasons,
        })),
      ]
        .map((event) => [event.id, event] as const),
    ).values(),
  ].sort((a, b) => a.id.localeCompare(b.id));
  const currentHolder =
    leases
      .filter(
        (lease) =>
          lease.workloadId === workloadId && lease.generation === generation,
      )
      .sort((a, b) => (a.releasedAt ?? 0) - (b.releasedAt ?? 0))
      .pop() ?? null;
  const invalidOnly = transactions.length === 0 && prepared.invalid.length > 0;
  const finalStatus: Status = invalidOnly ? "reject" : base.status;
  const finalReasons = invalidOnly
    ? uniqueStrings(
        prepared.invalid.flatMap((info) => info.invalidReasons),
      )
    : base.reasons;

  return {
    ...base,
    migrationId: chosen?.id ?? "",
    status: finalStatus,
    target: winner?.decision.target ?? null,
    releaseTo: winner?.decision.target ?? null,
    release: winner?.decision.target ?? null,
    targetKey: winner?.decision.target?.runtimeKey ?? null,
    targetChipId: winner?.decision.target?.chipId ?? null,
    targetPublicKey: winner?.decision.target?.runtimeKey ?? null,
    candidateTarget: chosen?.decision.target ?? null,
    candidateTargetKey: chosen?.decision.target?.runtimeKey ?? null,
    releasable: finalStatus === "release" && winner !== undefined,
    allowed: finalStatus === "release" && winner !== undefined,
    outcome:
      finalStatus === "release"
        ? "allow"
        : finalStatus === "wait"
          ? "pending"
          : "deny",
    runtimeKey: winner?.decision.target?.runtimeKey ?? null,
    key: winner?.decision.target?.runtimeKey ?? null,
    chipId: winner?.decision.target?.chipId ?? null,
    publicKey: winner?.decision.target?.runtimeKey ?? null,
    reason: finalReasons.join(";"),
    reasons: finalReasons,
    requiredSignatures: [...base.dependencies],
    requiredRecords: [...base.dependencies],
    evidence: [...(base.evidence ?? [])],
    invalidEvents: allInvalidEvents,
    migrations,
    rejected: migrations.filter((item) => item.status === "reject"),
    waiting: migrations.filter((item) => item.status === "wait"),
    released,
    conflicts,
    causalPrefixComplete: prepared.missingCausalParents.length === 0,
    missingCausalParents: prepared.missingCausalParents,
    currentHolder,
    currentHolderKey: currentHolder?.runtimeKey ?? null,
  };
}

function isEvaluateInput(value: unknown): value is EvaluateInput {
  return (
    typeof value === "object" &&
    value !== null &&
    "workloadId" in value &&
    "generation" in value &&
    "events" in value
  );
}

/**
 * Evaluate a key generation.  Overloads accept either an object or positional
 * arguments, which makes the package easy to consume from a key agent.
 */
export function evaluateRelease(input: EvaluateInput): ReleaseDecision;
export function evaluateRelease(events: MigrationEvent[]): ReleaseDecision;
export function evaluateRelease(
  events: MigrationEvent[],
  options: Omit<EvaluateInput, "events">,
): ReleaseDecision;
export function evaluateRelease(
  events: MigrationEvent[],
  policy: MigrationPolicy,
): ReleaseDecision;
export function evaluateRelease(
  generation: number,
  events: MigrationEvent[],
  options?: Omit<EvaluateInput, "generation" | "events">,
): ReleaseDecision;
export function evaluateRelease(
  generation: number,
  events: MigrationEvent[],
  policy?: MigrationPolicy,
): ReleaseDecision;
export function evaluateRelease(
  workloadId: string,
  generation: number,
  events: MigrationEvent[],
  options?: Omit<EvaluateInput, "workloadId" | "generation" | "events">,
): ReleaseDecision;
export function evaluateRelease(
  workloadId: string,
  events: MigrationEvent[],
  options?: Omit<EvaluateInput, "workloadId" | "events">,
): ReleaseDecision;
export function evaluateRelease(
  workloadId: string,
  options: Omit<EvaluateInput, "workloadId">,
): ReleaseDecision;
export function evaluateRelease(
  inputOrWorkload: EvaluateInput | string | MigrationEvent[] | number,
  generationArg?: number | Omit<EvaluateInput, "events"> | MigrationEvent[],
  eventsArg?: MigrationEvent[] | Omit<EvaluateInput, "generation" | "events">,
  optionsArg?: Omit<EvaluateInput, "workloadId" | "generation" | "events">,
): ReleaseDecision {
  let input: EvaluateInput;
  if (Array.isArray(inputOrWorkload)) {
    const rawOptions =
      (generationArg as Omit<EvaluateInput, "events"> | undefined) ?? {};
    const looksLikePolicy =
      (rawOptions as { policy?: MigrationPolicy }).policy === undefined &&
      ("digest" in rawOptions ||
        "id" in rawOptions ||
        "ownerThreshold" in rawOptions ||
        "baselineTcb" in rawOptions ||
        "recovery" in rawOptions ||
        "policyDigest" in rawOptions);
    input = {
      events: inputOrWorkload,
      ...rawOptions,
      ...(looksLikePolicy ? { policy: rawOptions as MigrationPolicy } : {}),
    };
  } else if (typeof inputOrWorkload === "number") {
    const rawOptions =
      (eventsArg as Omit<EvaluateInput, "generation" | "events"> | undefined) ??
      {};
    const looksLikePolicy =
      (rawOptions as { policy?: MigrationPolicy }).policy === undefined &&
      ("digest" in rawOptions ||
        "id" in rawOptions ||
        "ownerThreshold" in rawOptions ||
        "baselineTcb" in rawOptions ||
        "recovery" in rawOptions ||
        "policyDigest" in rawOptions);
    input = {
      generation: inputOrWorkload,
      events: Array.isArray(generationArg) ? generationArg : [],
      ...rawOptions,
      ...(looksLikePolicy ? { policy: rawOptions as MigrationPolicy } : {}),
    };
  } else if (typeof inputOrWorkload === "string") {
    const inlineOptions =
      generationArg !== undefined &&
      !Array.isArray(generationArg) &&
      typeof generationArg === "object"
        ? (generationArg as Omit<
            EvaluateInput,
            "workloadId" | "generation" | "events"
          > & { events?: MigrationEvent[]; generation?: number })
        : undefined;
    const workloadEvents = Array.isArray(generationArg)
      ? generationArg
      : Array.isArray(eventsArg)
        ? eventsArg
        : [];
    const rawOptions =
      (Array.isArray(generationArg)
        ? (eventsArg as Omit<
            EvaluateInput,
            "workloadId" | "generation" | "events"
          > | undefined)
        : (inlineOptions ?? optionsArg)) ?? {};
    const looksLikePolicy =
      rawOptions.policy === undefined &&
      ("digest" in rawOptions ||
        "id" in rawOptions ||
        "ownerThreshold" in rawOptions ||
        "baselineTcb" in rawOptions ||
        "tcbRules" in rawOptions ||
        "recovery" in rawOptions ||
        "policyDigest" in rawOptions);
    input = {
      workloadId: inputOrWorkload,
      generation:
        typeof generationArg === "number"
          ? generationArg
          : inlineOptions?.generation,
      events:
        workloadEvents.length > 0
          ? workloadEvents
          : Array.isArray(
                (rawOptions as EvaluateInput & { events?: unknown }).events,
              )
            ? ((rawOptions as EvaluateInput).events as MigrationEvent[])
            : [],
      ...rawOptions,
      ...(looksLikePolicy ? { policy: rawOptions as MigrationPolicy } : {}),
    };
  } else {
    input = inputOrWorkload;
  }

  const normalizedInput: Record<string, unknown> = {
    ...(input as Record<string, unknown>),
  };
  for (const [key, value] of Object.entries(normalizedInput)) {
    const camelKey = key.replace(/[_-]([a-zA-Z0-9])/g, (_, char: string) =>
      char.toUpperCase(),
    );
    if (camelKey !== key && normalizedInput[camelKey] === undefined) {
      normalizedInput[camelKey] = value;
    }
  }
  input = normalizedInput as EvaluateInput;

  const verifier =
    input.verifier ??
    input.verify ??
    input.signatureVerifier ??
    (input.publicKeys
      ? createEd25519Verifier(
          input.publicKeys as Parameters<typeof createEd25519Verifier>[0],
        )
      : undefined);
  const hmacKeys = (
    input as EvaluateInput & {
      hmacSecrets?: Record<string, string | Buffer>;
      hmacKeys?: Record<string, string | Buffer>;
      secrets?: Record<string, string | Buffer>;
    }
  );
  const effectiveVerifier =
    verifier ??
    (hmacKeys.hmacSecrets ?? hmacKeys.hmacKeys ?? hmacKeys.secrets
      ? createHmacVerifier(
          hmacKeys.hmacSecrets ?? hmacKeys.hmacKeys ?? hmacKeys.secrets ?? {},
        )
      : undefined);
  const rawEvents = input.events;
  const grouped = input as EvaluateInput & {
    attestations?: MigrationEvent[];
    deactivations?: MigrationEvent[];
    instanceDeactivations?: MigrationEvent[];
    revocations?: MigrationEvent[];
    keyRevocations?: MigrationEvent[];
    transfers?: MigrationEvent[];
    transferAcceptances?: MigrationEvent[];
    confirmations?: MigrationEvent[];
    owners?: MigrationEvent[];
    ownerConfirmations?: MigrationEvent[];
    recoveries?: MigrationEvent[];
    recoveryConfirmations?: MigrationEvent[];
  };
  const rawValues = Array.isArray(rawEvents)
    ? rawEvents
    : rawEvents instanceof Set
      ? [...rawEvents]
      : rawEvents instanceof Map
        ? [...rawEvents.values()]
    : Object.values(rawEvents ?? {});
  const groupedValues = [
    ...(Array.isArray(grouped.attestations) ? grouped.attestations : []),
    ...(Array.isArray(grouped.deactivations) ? grouped.deactivations : []),
    ...(Array.isArray(grouped.instanceDeactivations)
      ? grouped.instanceDeactivations
      : []),
    ...(Array.isArray(grouped.revocations) ? grouped.revocations : []),
    ...(Array.isArray(grouped.keyRevocations) ? grouped.keyRevocations : []),
    ...(Array.isArray(grouped.transfers) ? grouped.transfers : []),
    ...(Array.isArray(grouped.transferAcceptances)
      ? grouped.transferAcceptances
      : []),
    ...(Array.isArray(grouped.confirmations) ? grouped.confirmations : []),
    ...(Array.isArray(grouped.owners) ? grouped.owners : []),
    ...(Array.isArray(grouped.ownerConfirmations)
      ? grouped.ownerConfirmations
      : []),
    ...(Array.isArray(grouped.recoveries) ? grouped.recoveries : []),
    ...(Array.isArray(grouped.recoveryConfirmations)
      ? grouped.recoveryConfirmations
      : []),
  ];
  const eventList = [...rawValues, ...groupedValues].flatMap((value) =>
    Array.isArray(value) ? value : [value],
  ).filter(
    (value): value is MigrationEvent =>
      typeof value === "object" && value !== null,
  );
  const effectiveInput: EvaluateInput = {
    ...input,
    events: eventList,
    verifier: effectiveVerifier,
    requireSignatures:
      asBoolean(input.requireSignatures) ??
      asBoolean(
        (input as EvaluateInput & { verifySignatures?: unknown })
          .verifySignatures,
      ) ??
      asBoolean(
        (input as EvaluateInput & { signaturesRequired?: unknown })
          .signaturesRequired,
      ),
  };
  const topLevelPolicy = {
    digest: asString(
      (input as EvaluateInput & { policyDigest?: unknown }).policyDigest,
    ) ??
      asString((input as EvaluateInput & { digest?: unknown }).digest) ??
      asString((input as EvaluateInput & { policyHash?: unknown }).policyHash),
    baselineTcb: input.baselineTcb,
    minimumTcb: (input as EvaluateInput & { minimumTcb?: MigrationPolicy["minimumTcb"] })
      .minimumTcb,
    tcbRules: (input as EvaluateInput & { tcbRules?: MigrationPolicy["tcbRules"] })
      .tcbRules,
    ownerThreshold: (input as EvaluateInput & { ownerThreshold?: number })
      .ownerThreshold,
    allowRecovery: (input as EvaluateInput & { allowRecovery?: boolean })
      .allowRecovery,
    recovery: (input as EvaluateInput & { recovery?: MigrationPolicy["recovery"] })
      .recovery,
    recoveryThreshold: (
      input as EvaluateInput & { recoveryThreshold?: number }
    ).recoveryThreshold,
    requireDeactivation: (
      input as EvaluateInput & { requireDeactivation?: boolean }
    ).requireDeactivation,
    requireKeyRevocation: (
      input as EvaluateInput & { requireKeyRevocation?: boolean }
    ).requireKeyRevocation,
    requireOwnerConfirmation: (
      input as EvaluateInput & { requireOwnerConfirmation?: boolean }
    ).requireOwnerConfirmation,
    allowDebug: (input as EvaluateInput & { allowDebug?: boolean }).allowDebug,
  };
  const declaredPolicy = input.policy as
    | MigrationPolicy
    | string
    | undefined;
  const policy = normalizePolicy(
    typeof declaredPolicy === "string"
      ? { ...topLevelPolicy, digest: declaredPolicy }
      : { ...topLevelPolicy, ...(declaredPolicy ?? {}) },
  );
  const prepared = prepare(eventList, effectiveInput, policy);
  const workloadId =
    asString(input.workloadId) ??
    asString(
      (input as EvaluateInput & { workload?: unknown }).workload,
    ) ??
    asString(policy.workloadId) ??
    uniqueValue(
      prepared.infos
        .filter(
          (info) =>
            info.action === "accept-transfer" ||
            info.action === "confirm-migration" ||
            info.action === "confirm-recovery",
        )
        .map((info) => info.workloadId)
        .filter((value): value is string => value !== undefined),
    ) ??
    uniqueValue(
      prepared.infos
        .map((info) => info.workloadId)
        .filter((value): value is string => value !== undefined),
    ) ??
    "";
  const generation =
    asNumber(input.generation) ??
    asNumber(
      (input as EvaluateInput & { keyGeneration?: unknown }).keyGeneration,
    ) ??
    asNumber(policy.generation) ??
    uniqueValue(
      prepared.infos
        .filter(
          (info) =>
            info.action === "accept-transfer" ||
            info.action === "confirm-migration" ||
            info.action === "confirm-recovery",
        )
        .map((info) => info.generation)
        .filter((value): value is number => value !== undefined),
    ) ??
    uniqueValue(
      prepared.infos
        .map((info) => info.generation)
        .filter((value): value is number => value !== undefined),
    ) ??
    (() => {
      const generations = prepared.infos
        .map((info) => info.generation)
        .filter((value): value is number => value !== undefined);
      return generations.length > 0 ? Math.max(...generations) : 0;
    })();
  const manifest = normalizeClosedDevices(
    input.closedDevices ??
      ((input as EvaluateInput & { closed?: ClosedDeviceManifest }).closed) ??
      (input as EvaluateInput & { closedDeviceList?: ClosedDeviceManifest })
        .closedDeviceList ??
      (input as EvaluateInput & { devices?: ClosedDeviceManifest }).devices ??
      (policy.raw.closedDevices as ClosedDeviceManifest | undefined) ??
      (policy.raw.devices as ClosedDeviceManifest | undefined),
  );
  const transactions = buildTransactions(prepared, workloadId, generation);
  const duplicateNonces = duplicateNonceIds(prepared.infos, policy);
  const rawLeases =
    input.leases ??
    (input as EvaluateInput & { currentHolders?: Lease[] }).currentHolders ??
    (input as EvaluateInput & { released?: Lease[] }).released ??
    [];
  const leases: Lease[] = Array.isArray(rawLeases)
    ? rawLeases
    : rawLeases instanceof Map
      ? [...rawLeases.values()]
      : Object.values(rawLeases);
  const normalizedLeases = leases
    .map((lease) => normalizeLease(lease))
    .filter((lease): lease is Lease => lease !== undefined);
  const expectedNonce = firstDefined(
    asString(input.expectedNonce),
    asString(policy.expectedNonce),
    asString((input as EvaluateInput & { nonce?: unknown }).nonce),
    asString((input as EvaluateInput & { challenge?: unknown }).challenge),
    asString(
      (input as EvaluateInput & { challengeNonce?: unknown }).challengeNonce,
    ),
  );

  for (const tx of transactions) {
    evaluateTransaction(
      tx,
      prepared,
      policy,
      manifest,
      duplicateNonces,
      expectedNonce,
      input.baselineTcb,
      normalizedLeases,
    );
  }
  const conflicts = applyConcurrency(transactions);
  return makeReleaseDecision(
    workloadId,
    generation,
    prepared,
    transactions,
    conflicts,
    normalizedLeases,
  );
}

function uniqueValue<T>(values: T[]): T | undefined {
  const unique = [...new Set(values)];
  return unique.length === 1 ? unique[0] : undefined;
}

function normalizeLease(value: Lease): Lease | undefined {
  if (typeof value !== "object" || value === null) return undefined;
  const raw = value as unknown as Record<string, unknown>;
  const workloadId = asString(
    raw.workloadId ?? raw.workload_id ?? raw.workload,
  );
  const generation = asNumber(
    raw.generation ?? raw.keyGeneration ?? raw.gen,
  );
  const runtimeKey = asString(
    raw.runtimeKey ??
      raw.runtime_key ??
      raw.publicKey ??
      raw.public_key ??
      raw.key,
  );
  if (
    workloadId === undefined ||
    generation === undefined ||
    runtimeKey === undefined
  ) {
    return undefined;
  }
  const dependencies = Array.isArray(raw.dependencies)
    ? raw.dependencies
        .map((entry) => asString(entry))
        .filter((entry): entry is string => entry !== undefined)
    : undefined;
  return {
    workloadId,
    generation,
    runtimeKey,
    chipId: asString(raw.chipId ?? raw.chip_id ?? raw.chip),
    migrationId: asString(
      raw.migrationId ?? raw.migration_id ?? raw.migration,
    ),
    dependencies,
    releasedAt: timestampValue(raw.releasedAt ?? raw.released_at),
  };
}

function autoTargetId(
  workloadId: string,
  generation: number,
  chipId: string,
  runtimeKey: string,
): string {
  return `auto:${sha256Hex(
    canonicalJson({ workloadId, generation, chipId, runtimeKey }),
  ).slice(0, 20)}`;
}

export const evaluateGeneration = evaluateRelease;
export const evaluate = evaluateRelease;
export const evaluateMigration = evaluateRelease;
export const evaluateMigrationAcceptance = evaluateRelease;
export const decideKeyRelease = evaluateRelease;
export const decideRelease = evaluateRelease;
export const computeReleaseDecision = evaluateRelease;
export const releaseDecision = evaluateRelease;
export const acceptMigration = evaluateRelease;
export const evaluateAcceptance = evaluateRelease;
export const assessMigration = evaluateRelease;
export const determineRelease = evaluateRelease;
export const decideKeyGeneration = evaluateRelease;
export const evaluateKeyRelease = evaluateRelease;
export const evaluateKeyGeneration = evaluateRelease;
export const accept = evaluateRelease;
export const decide = evaluateRelease;
export const release = evaluateRelease;

export function canRelease(decision: ReleaseDecision): boolean {
  return decision.status === "release" && decision.release !== null;
}

export function minimalSignedDependencies(
  decision: ReleaseDecision | MigrationDecision,
): string[] {
  return "minimalDependencies" in decision
    ? [...decision.minimalDependencies]
    : [];
}
