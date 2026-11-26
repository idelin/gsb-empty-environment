import { bridgeSigningPayload, verifyBridgeSignature } from "./bridge.ts";
import { canonicalDigest, canonicalJson } from "./canonical.ts";
import {
  DEVICE_DOMAIN_END,
  DEVICE_ID_MAX,
  cellsFromBoundaries,
  collectBoundaries,
  normalizeRangeSet,
  rangeContains,
  rangeIntersect,
  rangeIntersects,
  rangeIsSubset,
  rangeSignature,
  rangeSize,
  rangeUnion,
  toDeviceId,
  type RangeSet,
  type ScalarDeviceId,
} from "./interval.ts";
import {
  deviceClassMembers,
  modelToManifest,
  normalizePolicy,
  policyFromManifest,
} from "./normalize.ts";
import type {
  AcceptanceCertificate,
  AcceptanceManifest,
  AcceptancePolicy,
  AcceptanceResult,
  AuthorizationClass,
  BridgeAudit,
  BridgeRejectionCode,
  ClassDecision,
  CoalitionDecision,
  DeviceDecision,
  IdDecision,
  NormalizedCapability,
  NormalizedDevice,
  NormalizedModel,
  PartitionCell,
  RiskFinding,
  RiskVerdict,
  ShareEvaluation,
  ShareInput,
  VerificationResult,
} from "./types.ts";

interface CapabilityEligibility {
  readonly direct: boolean;
  readonly bridged: boolean;
  readonly directCapabilities: readonly string[];
  readonly bridgedCapabilities: readonly string[];
}

interface AnalysisCore {
  readonly model: NormalizedModel;
  readonly audits: readonly BridgeAudit[];
  readonly decisions: readonly DeviceDecision[];
  readonly classes: readonly ClassDecision[];
  readonly coalitions: readonly CoalitionDecision[];
  readonly partitions: readonly PartitionCell[];
  readonly boundaries: readonly bigint[];
  readonly risk: RiskVerdict;
}

function bridgeCanApply(bridge: BridgeAudit["bridge"], model: NormalizedModel): boolean {
  if (bridge.tenant !== null && bridge.tenant !== model.message.tenant) return false;
  if (bridge.purpose !== null && bridge.purpose !== model.message.purpose) return false;
  if (
    bridge.contentGeneration !== null &&
    bridge.contentGeneration !== model.message.contentGeneration
  ) {
    return false;
  }
  if (
    model.message.interval !== null &&
    !rangeIntersects(bridge.interval, model.message.interval)
  ) {
    return false;
  }
  return true;
}

function auditOneBridge(
  bridge: BridgeAudit["bridge"],
  model: NormalizedModel,
  policy: AcceptancePolicy,
  duplicate: boolean,
): BridgeAudit {
  const payload = bridgeSigningPayload(bridge as unknown as Parameters<typeof bridgeSigningPayload>[0]);
  let rejection: BridgeAudit["rejection"];

  if (duplicate) {
    rejection = { code: "duplicate-bridge-id", detail: "bridge id is declared more than once" };
  } else if (bridge.shortcutFields.length > 0 && !hasCompleteScope(bridge)) {
    rejection = {
      code: "name-or-membership-shortcut",
      detail: `bridge uses ${bridge.shortcutFields.join(", ")} without interval, purpose, and generation`,
    };
  } else if (bridge.interval.length === 0) {
    rejection = { code: "missing-interval", detail: "bridge has no symbolic device interval" };
  } else if (bridge.purpose === null || bridge.purpose.length === 0) {
    rejection = { code: "missing-purpose", detail: "bridge has no purpose" };
  } else if (bridge.keyGeneration === null) {
    rejection = { code: "missing-generation", detail: "bridge has no old key generation" };
  } else if (
    bridge.purpose === "*" ||
    bridge.purpose.toLowerCase() === "any" ||
    bridge.tenant === null
  ) {
    rejection = { code: "wildcard-scope", detail: "bridge scope must be concrete" };
  } else if (bridge.keyGeneration >= model.message.keyGeneration) {
    rejection = {
      code: "not-old",
      detail: `bridge generation ${bridge.keyGeneration} is not older than the message generation ${model.message.keyGeneration}`,
    };
  } else {
    const trusted = new Set(policy.trustedBridgeIds ?? []);
    let valid = trusted.has(bridge.bridgeId);
    const hasVerifier =
      trusted.size > 0 ||
      policy.verifyBridgeSignature !== undefined ||
      (policy.bridgeSignatureVerifier as unknown) !== undefined ||
      (policy.bridgeSecret ?? policy.authorityKey ?? policy.bridgeKey ?? policy.signingKey) !==
        undefined ||
      policy.bridgePublicKey !== undefined;
    if (!valid) {
      const secret =
        policy.bridgeSecret ??
        policy.authorityKey ??
        (policy.bridgeKey as string | undefined) ??
        (policy.signingKey as string | undefined);
      const customVerifier =
        policy.verifyBridgeSignature ??
        (policy.bridgeSignatureVerifier as typeof policy.verifyBridgeSignature | undefined);
      if (customVerifier !== undefined) {
        valid = customVerifier(bridge, payload);
      } else if (secret !== undefined) {
        valid = verifyBridgeSignature(
          bridge as unknown as Parameters<typeof verifyBridgeSignature>[0],
          { secret },
        );
      } else if (policy.bridgePublicKey !== undefined) {
        valid = verifyBridgeSignature(
          bridge as unknown as Parameters<typeof verifyBridgeSignature>[0],
          { publicKey: policy.bridgePublicKey },
        );
      } else if (policy.assumeSignedBridges === true) {
        valid = verifyBridgeSignature(
          bridge as unknown as Parameters<typeof verifyBridgeSignature>[0],
          { assumeSigned: true },
        );
      } else if (
        policy.rejectUnverifiedSignatures !== true &&
        !hasVerifier &&
        bridge.signature !== null
      ) {
        // The host may treat signatures as opaque attestations supplied by an
        // outer transport.  Presence-only acceptance is deliberately limited
        // to the case where no cryptographic verifier was configured.
        valid = true;
      }
    }
    if (!valid) {
      rejection =
        bridge.signature === null && !bridge.signedFlag
          ? { code: "unsigned", detail: "bridge declaration is not signed" }
          : { code: "bad-signature", detail: "bridge signature did not verify" };
    }
  }

  const accepted = rejection === undefined;
  const applicable = accepted && bridgeCanApply(bridge, model);
  return { bridge, accepted, applicable, ...(rejection === undefined ? {} : { rejection }), payload };
}

function hasCompleteScope(bridge: BridgeAudit["bridge"]): boolean {
  return (
    bridge.tenant !== null &&
    bridge.interval.length > 0 &&
    bridge.purpose !== null &&
    bridge.purpose.length > 0 &&
    bridge.keyGeneration !== null
  );
}

export function auditBridges(model: NormalizedModel, policy: AcceptancePolicy): BridgeAudit[] {
  const seen = new Set<string>();
  const audits: BridgeAudit[] = [];
  for (const bridge of model.bridges) {
    const duplicate = seen.has(bridge.bridgeId);
    seen.add(bridge.bridgeId);
    audits.push(auditOneBridge(bridge, model, policy, duplicate));
  }
  return audits.sort((a, b) => a.bridge.bridgeId.localeCompare(b.bridge.bridgeId));
}

function bridgeAllowsCapability(
  audit: BridgeAudit,
  capability: NormalizedCapability,
  model: NormalizedModel,
): boolean {
  if (!audit.accepted || !audit.applicable) return false;
  const bridge = audit.bridge;
  const message = model.message;
  if (bridge.tenant !== capability.tenant || capability.tenant !== message.tenant) return false;
  if (bridge.purpose !== capability.purpose || capability.purpose !== message.purpose) return false;
  if (bridge.keyGeneration !== capability.keyGeneration) return false;
  if (capability.keyGeneration >= message.keyGeneration) return false;
  if (capability.contentGeneration > message.contentGeneration) return false;
  if (
    bridge.contentGeneration !== null &&
    bridge.contentGeneration !== message.contentGeneration
  ) {
    return false;
  }
  return rangeIntersects(capability.interval, bridge.interval);
}

function bridgeAllowsCapabilityForId(
  audit: BridgeAudit,
  capability: NormalizedCapability,
  model: NormalizedModel,
  id: bigint,
): boolean {
  if (!bridgeAllowsCapability(audit, capability, model)) return false;
  return rangeContains(audit.bridge.interval, id) && rangeContains(capability.interval, id);
}

function evaluateDeviceCapabilities(
  model: NormalizedModel,
  audits: readonly BridgeAudit[],
  device: NormalizedDevice,
): CapabilityEligibility {
  let direct = false;
  let bridged = false;
  const directCapabilities: string[] = [];
  const bridgedCapabilities: string[] = [];
  const message = model.message;

  for (const capability of device.capabilities) {
    const coveredIds = device.ids.filter((id) => {
      if (message.interval !== null && !rangeContains(message.interval, id)) return false;
      return rangeContains(capability.interval, id);
    });
    if (coveredIds.length === 0) continue;

    if (
      capability.tenant === message.tenant &&
      capability.contentGeneration === message.contentGeneration &&
      capability.keyGeneration === message.keyGeneration &&
      capability.purpose === message.purpose
    ) {
      direct = true;
      directCapabilities.push(capability.capabilityId);
    }

    if (
      audits.some((audit) =>
        coveredIds.some((id) => bridgeAllowsCapabilityForId(audit, capability, model, id)),
      )
    ) {
      bridged = true;
      bridgedCapabilities.push(capability.capabilityId);
    }
  }

  return {
    direct,
    bridged,
    directCapabilities: [...new Set(directCapabilities)].sort(),
    bridgedCapabilities: [...new Set(bridgedCapabilities)].sort(),
  };
}

function adjacentToRevocation(revocation: RangeSet, id: bigint): boolean {
  for (const interval of revocation) {
    if (id + 1n === interval.start || id === interval.end + 1n) return true;
  }
  return false;
}

function decisionForDevice(
  model: NormalizedModel,
  audits: readonly BridgeAudit[],
  device: NormalizedDevice,
): DeviceDecision {
  const eligibility = evaluateDeviceCapabilities(model, audits, device);
  const eligible = eligibility.direct || eligibility.bridged;
  const alone = !device.revoked && model.message.threshold <= 1 && eligible;
  let authorization: AuthorizationClass;
  if (device.revoked) authorization = "revoked";
  else if (alone && eligibility.direct) authorization = "authorized";
  else if (alone && eligibility.bridged) authorization = "bridged";
  else if (eligible) authorization = "partial";
  else authorization = "denied";

  return {
    device: device.key,
    ids: device.ids.map((id) => id.toString(10)),
    ...(device.label === undefined ? {} : { label: device.label }),
    ...(device.className === undefined ? {} : { className: device.className }),
    revoked: device.revoked,
    direct: eligibility.direct,
    bridged: eligibility.bridged,
    eligible,
    alone,
    distinctSources: eligible ? 1 : 0,
    authorization,
    risk: {
      revokedRead: device.revoked && eligible,
      boundaryAdjacent: device.ids.some((id) => adjacentToRevocation(model.revocation, id)),
    },
  };
}

function syntheticDevice(model: NormalizedModel, id: bigint): NormalizedDevice {
  const key = `device:${id.toString(10)}`;
  const capabilities: NormalizedCapability[] = [];
  for (const group of model.groups) {
    if (!rangeContains(group.interval, id)) continue;
    capabilities.push({
      capabilityId: `cap:${key}:${group.groupId}`,
      owner: key,
      groupId: group.groupId,
      tenant: group.tenant,
      contentGeneration: group.contentGeneration,
      keyGeneration: group.keyGeneration,
      purpose: group.purpose,
      interval: group.interval,
    });
  }
  return {
    key,
    ids: [id],
    aliases: [id.toString(10)],
    capabilities,
    revoked: rangeContains(model.revocation, id),
    explicitCapabilities: false,
  };
}

function resolveDevice(model: NormalizedModel, ref: string | ScalarDeviceId): NormalizedDevice | undefined {
  if (typeof ref !== "string") {
    const id = toDeviceId(ref);
    return model.deviceById.get(id.toString(10)) ?? syntheticDevice(model, id);
  }
  const direct = model.deviceById.get(ref);
  if (direct !== undefined) return direct;
  const alias = model.aliasToDevice.get(ref);
  if (alias !== undefined) return model.deviceById.get(alias);
  try {
    const id = toDeviceId(ref as ScalarDeviceId);
    return model.deviceById.get(id.toString(10)) ?? syntheticDevice(model, id);
  } catch {
    return undefined;
  }
}

function classDecisions(
  policy: AcceptancePolicy,
  model: NormalizedModel,
  audits: readonly BridgeAudit[],
  decisions: readonly DeviceDecision[],
): ClassDecision[] {
  const byKey = new Map<string, DeviceDecision>();
  for (const decision of decisions) {
    byKey.set(decision.device, decision);
    for (const id of decision.ids) byKey.set(id, decision);
  }
  let classes = deviceClassMembers(policy, model.devices);
  if (classes.length === 0) {
    const inferred = new Map<string, string[]>();
    for (const device of model.devices) {
      const profile = device.capabilities
        .map(
          (capability) =>
            `${capability.tenant}|c${capability.contentGeneration}|k${capability.keyGeneration}|${capability.purpose}|${rangeSignature(capability.interval)}`,
        )
        .sort()
        .join(";");
      const name = `profile:${canonicalDigest({ profile }).slice(0, 12)}`;
      const members = inferred.get(name) ?? [];
      members.push(device.key);
      inferred.set(name, members);
    }
    classes = [...inferred].map(([className, members]) => ({ className, members }));
  }
  return classes.map(({ className, members }) => {
    const resolved: DeviceDecision[] = [];
    for (const member of members) {
      const direct = byKey.get(member);
      if (direct !== undefined) {
        resolved.push(direct);
        continue;
      }
      const device = resolveDevice(model, member);
      if (device === undefined) continue;
      const decision = decisionForDevice(model, audits, device);
      resolved.push(decision);
      byKey.set(device.key, decision);
    }
    const unique = [...new Map(resolved.map((decision) => [decision.device, decision])).values()];
    const eligible = unique.filter((decision) => decision.eligible && !decision.revoked);
    const distinctEligibleSources = new Set(eligible.map((decision) => decision.device)).size;
    const canReadTogether = distinctEligibleSources >= model.message.threshold;
    const authorization: AuthorizationClass = canReadTogether
      ? eligible.some((decision) => decision.direct)
        ? "authorized"
        : "bridged"
      : distinctEligibleSources > 0
        ? "partial"
        : unique.length > 0 && unique.every((decision) => decision.revoked)
          ? "revoked"
          : "denied";
    return {
      name: className,
      members: unique.map((decision) => decision.device).sort(),
      authorization,
      canReadTogether,
      allMembersAlone: unique.length > 0 && unique.every((decision) => decision.alone),
      someMemberAlone: unique.some((decision) => decision.alone),
      distinctEligibleSources,
    };
  });
}

function coalitionFromMembers(
  model: NormalizedModel,
  audits: readonly BridgeAudit[],
  coalitionId: string,
  rawRefs: readonly string[],
): CoalitionDecision {
  const resolved = rawRefs.map((ref) => ({ ref, device: resolveDevice(model, ref) }));
  const uniqueDevices = new Map<string, NormalizedDevice>();
  let onlyRevoked = true;
  for (const entry of resolved) {
    if (entry.device === undefined) {
      onlyRevoked = false;
      continue;
    }
    uniqueDevices.set(entry.device.key, entry.device);
  }

  let individualMemberCouldRead = false;
  let eligibleSources = 0;
  let normalEligibleSources = 0;
  for (const device of uniqueDevices.values()) {
    const eligibility = evaluateDeviceCapabilities(model, audits, device);
    const eligible = eligibility.direct || eligibility.bridged;
    if (!device.revoked) onlyRevoked = false;
    else if (!eligible) {
      // A revoked device with no usable capability is still revoked.
    }
    if (eligible) {
      eligibleSources += 1;
      if (!device.revoked) normalEligibleSources += 1;
      if (model.message.threshold <= 1) individualMemberCouldRead = true;
    }
  }
  if (uniqueDevices.size === 0) onlyRevoked = false;

  const distinctPhysicalSources = uniqueDevices.size;
  const aliasCollapsed =
    rawRefs.length > distinctPhysicalSources && rawRefs.length >= model.message.threshold;
  const canRead = normalEligibleSources >= model.message.threshold;
  const revokedRisk = onlyRevoked && eligibleSources >= model.message.threshold;
  return {
    coalitionId,
    members: [...uniqueDevices.keys()].sort(),
    rawMembers: [...rawRefs],
    distinctPhysicalSources,
    eligibleSources,
    threshold: model.message.threshold,
    canRead,
    individualMemberCouldRead,
    newReadRights: revokedRisk && !individualMemberCouldRead,
    aliasCollapsed,
    onlyRevoked,
    revokedRisk,
  };
}

function combinations<T>(items: readonly T[], size: number): T[][] {
  if (size <= 0 || size > items.length) return [];
  if (size === 1) return items.map((item) => [item]);
  const result: T[][] = [];
  const walk = (start: number, current: T[]): void => {
    if (current.length === size) {
      result.push([...current]);
      return;
    }
    for (let index = start; index <= items.length - (size - current.length); index += 1) {
      current.push(items[index]!);
      walk(index + 1, current);
      current.pop();
    }
  };
  walk(0, []);
  return result;
}

function coalitionDecisions(
  policy: AcceptancePolicy,
  model: NormalizedModel,
  audits: readonly BridgeAudit[],
): CoalitionDecision[] {
  const results: CoalitionDecision[] = [];
  const seen = new Set<string>();
  const add = (id: string, members: readonly string[]): void => {
    if (seen.has(id)) return;
    seen.add(id);
    results.push(coalitionFromMembers(model, audits, id, members));
  };

  for (const coalition of model.allowedCoalitions) {
    add(coalition.coalitionId, coalition.memberRefs);
  }

  const enumerate = policy.enumerateRevokedCoalitions !== false && model.devices.length <= 24;
  if (enumerate) {
    const revoked = model.devices.filter((device) => device.revoked);
    for (const device of revoked) add(`auto:single:${device.key}`, [device.key]);
    const maxSize =
      policy.maxCoalitionSize ??
      Math.min(6, Math.max(2, model.message.threshold));
    for (let size = 2; size <= Math.min(maxSize, revoked.length); size += 1) {
      for (const group of combinations(revoked, size)) {
        const refs = group.map((device) => device.key);
        const id = `auto:${size}:${canonicalDigest(refs).slice(0, 16)}`;
        add(id, refs);
      }
    }
  }
  return results.sort((a, b) => a.coalitionId.localeCompare(b.coalitionId));
}

function buildPartitions(
  model: NormalizedModel,
  audits: readonly BridgeAudit[],
): { partitions: PartitionCell[]; boundaries: bigint[] } {
  const ranges: RangeSet[] = [model.revocation];
  for (const group of model.groups) ranges.push(group.interval);
  for (const device of model.devices) {
    for (const capability of device.capabilities) ranges.push(capability.interval);
  }
  for (const audit of audits) {
    if (audit.accepted) ranges.push(audit.bridge.interval);
  }
  if (model.message.interval !== null) ranges.push(model.message.interval);

  const points: bigint[] = [];
  for (const device of model.devices) points.push(...device.ids);
  const boundaries = collectBoundaries(ranges, points);
  const cells = cellsFromBoundaries(boundaries);
  const message = model.message;
  const partitions = cells.map((cell): PartitionCell => {
    const revoked = rangeContains(model.revocation, cell.start);
    const inRecipientScope =
      message.interval === null || rangeContains(message.interval, cell.start);
    const cellDevices = model.devices.filter((device) =>
      device.ids.some((id) => id >= cell.start && id <= cell.end),
    );
    const directGroups = model.groups
      .filter((group) => {
        if (
          !inRecipientScope ||
          !rangeContains(group.interval, cell.start) ||
          group.tenant !== message.tenant ||
          group.contentGeneration !== message.contentGeneration ||
          group.keyGeneration !== message.keyGeneration ||
          group.purpose !== message.purpose
        ) {
          return false;
        }
        // A point cell that contains an explicit device is governed by what
        // that device actually retained.  Empty cells are symbolic coverage
        // and follow the encapsulation group itself.
        if (cellDevices.length === 0) return true;
        return cellDevices.some((device) =>
          device.capabilities.some(
            (capability) =>
              capability.groupId === group.groupId &&
              rangeContains(capability.interval, cell.start),
          ),
        );
      })
      .map((group) => group.groupId)
      .sort();

    const directCapabilities: string[] = [];
    const bridgedCapabilities: string[] = [];
    for (const device of model.devices) {
      const ownsPoint = device.ids.some((id) => id >= cell.start && id <= cell.end);
      if (!ownsPoint) continue;
      for (const capability of device.capabilities) {
        if (!inRecipientScope || !rangeContains(capability.interval, cell.start)) continue;
        if (
          capability.tenant === message.tenant &&
          capability.contentGeneration === message.contentGeneration &&
          capability.keyGeneration === message.keyGeneration &&
          capability.purpose === message.purpose
        ) {
          directCapabilities.push(capability.capabilityId);
        }
        if (
          audits.some(
            (audit) =>
              rangeContains(audit.bridge.interval, cell.start) &&
              bridgeAllowsCapability(audit, capability, model),
          )
        ) {
          bridgedCapabilities.push(capability.capabilityId);
        }
      }
    }

    const devices = cellDevices.map((device) => device.key).sort();
    const direct = directGroups.length > 0 || directCapabilities.length > 0;
    const bridged = bridgedCapabilities.length > 0;
    const authorization: AuthorizationClass = revoked
      ? "revoked"
      : !inRecipientScope
        ? "denied"
      : model.message.threshold > 1
        ? direct || bridged
          ? "partial"
          : "denied"
        : direct
          ? "authorized"
          : bridged
            ? "bridged"
            : "denied";
    return {
      start: cell.start.toString(10),
      end: cell.end.toString(10),
      revoked,
      directGroups,
      directCapabilities: [...new Set(directCapabilities)].sort(),
      bridgedCapabilities: [...new Set(bridgedCapabilities)].sort(),
      devices,
      authorization,
    };
  });
  return { partitions, boundaries };
}

function symbolicRevokedExposure(
  model: NormalizedModel,
  audits: readonly BridgeAudit[],
): { exposed: boolean; distinctIds: bigint } {
  if (
    model.devices.length > 0 ||
    model.groups.length === 0 ||
    rangeSize(model.revocation) === 0n
  ) {
    return { exposed: false, distinctIds: 0n };
  }
  const message = model.message;
  const ranges: RangeSet[] = [];
  for (const group of model.groups) {
    if (
      group.tenant !== message.tenant ||
      group.contentGeneration !== message.contentGeneration ||
      group.keyGeneration !== message.keyGeneration ||
      group.purpose !== message.purpose
    ) {
      continue;
    }
    ranges.push(
      message.interval === null
        ? group.interval
        : rangeIntersect(group.interval, message.interval),
    );
  }
  for (const audit of audits) {
    if (!audit.accepted || !audit.applicable) continue;
    const bridge = audit.bridge;
    if (bridge.keyGeneration === null || bridge.purpose === null || bridge.tenant === null) continue;
    if (
      bridge.contentGeneration !== null &&
      bridge.contentGeneration !== message.contentGeneration
    ) {
      continue;
    }
    for (const group of model.groups) {
      if (
        group.tenant !== bridge.tenant ||
        group.purpose !== bridge.purpose ||
        group.keyGeneration !== bridge.keyGeneration ||
        group.contentGeneration > message.contentGeneration
      ) {
        continue;
      }
      let range = rangeIntersect(group.interval, bridge.interval);
      if (message.interval !== null) range = rangeIntersect(range, message.interval);
      ranges.push(range);
    }
  }
  if (ranges.length === 0) return { exposed: false, distinctIds: 0n };
  const usable = rangeUnion(...ranges);
  const revokedUsable = rangeIntersect(model.revocation, usable);
  const distinctIds = rangeSize(revokedUsable);
  const exposed =
    message.threshold <= 1
      ? distinctIds > 0n
      : distinctIds >= BigInt(message.threshold);
  return { exposed, distinctIds };
}

function buildRisk(
  model: NormalizedModel,
  audits: readonly BridgeAudit[],
  decisions: readonly DeviceDecision[],
  coalitions: readonly CoalitionDecision[],
): RiskVerdict {
  const findings: RiskFinding[] = [];
  const findingKeys = new Set<string>();
  const add = (code: string, level: RiskFinding["level"], detail: string): void => {
    const key = `${code}:${detail}`;
    if (findingKeys.has(key)) return;
    findingKeys.add(key);
    findings.push({ code, level, detail });
  };

  let revokedDirectRead = false;
  let revokedBridgeRead = false;
  let revokedCoalitionEscalation = false;
  let bridgeOverreach = false;
  let unsignedBridge = false;
  let aliasDoubleCount = model.aliasConflicts.length > 0;
  let generationConfusion = false;
  let boundaryLeak = false;

  for (const audit of audits) {
    if (!audit.accepted) {
      const code = audit.rejection?.code;
      const couldApply = bridgeCanApply(audit.bridge, model);
      if ((code === "unsigned" || code === "bad-signature") && couldApply) {
        unsignedBridge = true;
        add("unsigned-bridge", "elevated", `${audit.bridge.bridgeId} was ignored because it is not validly signed`);
      } else if (code === "name-or-membership-shortcut") {
        add("bridge-name-shortcut", "elevated", `${audit.bridge.bridgeId} tried to use a name or membership shortcut`);
      } else if (code === "not-old" && couldApply) {
        generationConfusion = true;
        add("bridge-generation", "elevated", `${audit.bridge.bridgeId} does not name an older generation`);
      } else if (code === "wildcard-scope") {
        add("bridge-wildcard", "elevated", `${audit.bridge.bridgeId} used a wildcard scope`);
      } else if (
        code === "missing-interval" ||
        code === "missing-purpose" ||
        code === "missing-generation" ||
        code === "missing-tenant"
      ) {
        add("bridge-incomplete", "elevated", `${audit.bridge.bridgeId} has an incomplete scope`);
      }
      continue;
    }

    if (audit.bridge.tenantInferred) {
      add("bridge-tenant-inferred", "info", `${audit.bridge.bridgeId} inherits the permanent tenant`);
    }
    if (audit.bridge.roundUnbound) {
      add("bridge-round-unbound", "info", `${audit.bridge.bridgeId} is evaluated against the current round`);
    }
    if (audit.bridge.shortcutFields.length > 0) {
      add("bridge-extra-name-field", "elevated", `${audit.bridge.bridgeId} carries a non-scope name field`);
    }
    if (rangeIsSubset(audit.bridge.interval, [{ start: 0n, end: DEVICE_ID_MAX }])) {
      if (rangeSize(audit.bridge.interval) === DEVICE_DOMAIN_END) {
        bridgeOverreach = true;
        add("bridge-full-domain", "elevated", `${audit.bridge.bridgeId} covers the entire device domain`);
      }
    }
    if (audit.applicable && rangeIntersects(audit.bridge.interval, model.revocation)) {
      const touchesUsableOldCapability = model.devices.some((device) =>
        device.capabilities.some(
          (capability) =>
            capability.tenant === model.message.tenant &&
            capability.purpose === model.message.purpose &&
            capability.keyGeneration === audit.bridge.keyGeneration &&
            rangeIntersects(capability.interval, audit.bridge.interval),
        ),
      );
      if (touchesUsableOldCapability) {
        bridgeOverreach = true;
        add(
          "bridge-overlaps-revocation",
          "elevated",
          `${audit.bridge.bridgeId} overlaps the revoked interval with a usable old capability`,
        );
      }
    }
  }

  for (const decision of decisions) {
    if (decision.revoked && decision.eligible) {
      if (model.message.threshold <= 1) {
        if (decision.bridged && !decision.direct) {
          revokedBridgeRead = true;
          add("revoked-bridge-read", "unsafe", `${decision.device} is revoked but is eligible through a bridge`);
        } else {
          revokedDirectRead = true;
          add("revoked-direct-read", "unsafe", `${decision.device} is revoked but still holds a direct capability`);
        }
        boundaryLeak = true;
      } else {
        add(
          "revoked-retained-share",
          "info",
          `${decision.device} is revoked but retains one share; it is evaluated in coalition cases`,
        );
      }
    }
  }

  for (const coalition of coalitions) {
    if (coalition.revokedRisk) {
      if (coalition.newReadRights) {
        revokedCoalitionEscalation = true;
        add(
          "revoked-coalition-escalation",
          "unsafe",
          `${coalition.coalitionId} gains read access only by pooling revoked devices`,
        );
      } else {
        add(
          "revoked-coalition-read",
          "unsafe",
          `${coalition.coalitionId} of revoked devices can still open the message`,
        );
      }
      boundaryLeak = true;
    }
    if (coalition.aliasCollapsed) {
      aliasDoubleCount = true;
      add("alias-collapsed", "info", `${coalition.coalitionId} contained two aliases of one physical device`);
    }
  }
  if (model.aliasConflicts.length > 0) {
    add("alias-conflict", "elevated", "an alias resolves to more than one physical device");
  }

  // A bridge that names an old generation but has the same interval/purpose as
  // a current capability is a classic generation-confusion hazard.  It is not
  // an authorization by itself; the flag tells the acceptance function that
  // the declaration was rejected rather than silently applied.
  for (const audit of audits) {
    if (audit.rejection?.code !== "not-old") continue;
    if (audit.bridge.tenant !== model.message.tenant) continue;
    if (audit.bridge.purpose !== model.message.purpose) continue;
    if (!rangeIntersects(audit.bridge.interval, model.message.interval ?? [{ start: 0n, end: DEVICE_ID_MAX }])) {
      continue;
    }
    generationConfusion = true;
  }

  const symbolicExposure = symbolicRevokedExposure(model, audits);
  if (symbolicExposure.exposed) {
    boundaryLeak = true;
    if (model.message.threshold <= 1) {
      revokedDirectRead = true;
      add(
        "symbolic-revoked-read",
        "unsafe",
        "a revoked identifier interval intersects a range that can satisfy this round",
      );
    } else {
      revokedCoalitionEscalation = true;
      add(
        "symbolic-revoked-coalition",
        "unsafe",
        `at least ${model.message.threshold} revoked identifiers share usable ranges for this round`,
      );
    }
  }

  const level = findings.some((finding) => finding.level === "unsafe")
    ? "unsafe"
    : findings.some((finding) => finding.level === "elevated")
      ? "elevated"
      : "safe";
  findings.sort((a, b) => `${a.level}:${a.code}:${a.detail}`.localeCompare(`${b.level}:${b.code}:${b.detail}`));
  return {
    level,
    safe: level === "safe",
    revokedDirectRead,
    revokedCoalitionEscalation,
    revokedBridgeRead,
    bridgeOverreach,
    unsignedBridge,
    aliasDoubleCount,
    generationConfusion,
    boundaryLeak,
    findings,
  };
}

function certificateFor(
  model: NormalizedModel,
  audits: readonly BridgeAudit[],
  partitions: readonly PartitionCell[],
  boundaries: readonly bigint[],
  decisions: readonly DeviceDecision[],
  risk: RiskVerdict,
): AcceptanceCertificate {
  const manifest = modelToManifest(model);
  const accepted = audits.filter((audit) => audit.accepted).map((audit) => audit.bridge.bridgeId).sort();
  const rejected = audits
    .filter((audit) => !audit.accepted && audit.rejection !== undefined)
    .map((audit) => ({ bridgeId: audit.bridge.bridgeId, code: audit.rejection!.code }))
    .sort((a, b) => a.bridgeId.localeCompare(b.bridgeId));
  const cellSize = partitions.reduce(
    (total, cell) => total + (BigInt(cell.end) - BigInt(cell.start) + 1n),
    0n,
  );
  const boundaryWitnesses = [...new Set(
    model.revocation.flatMap((interval) => [
      interval.start,
      ...(interval.end + 1n <= DEVICE_ID_MAX ? [interval.end + 1n] : []),
    ]),
  )]
    .sort((a, b) => (a < b ? -1 : a > b ? 1 : 0))
    .map((at) => ({ at: at.toString(10), revoked: rangeContains(model.revocation, at) }));

  return {
    scheme: "gsb-header-acceptance/v1",
    domainBits: 48,
    policyDigest: canonicalDigest(manifest),
    partitionDigest: canonicalDigest(partitions),
    decisionDigest: canonicalDigest(decisions),
    riskDigest: canonicalDigest(risk),
    bridgeDigest: canonicalDigest(
      audits.map((audit) => ({
        bridgeId: audit.bridge.bridgeId,
        accepted: audit.accepted,
        applicable: audit.applicable,
        rejection: audit.rejection?.code ?? null,
        signatureDigest:
          audit.bridge.signature === null ? null : canonicalDigest(audit.bridge.signature),
      })),
    ),
    manifest,
    boundaries: boundaries.map((boundary) => boundary.toString(10)),
    coverage: {
      cells: partitions.length,
      total: cellSize.toString(10),
      complete:
        partitions.length > 0 &&
        partitions[0]!.start === "0" &&
        partitions[partitions.length - 1]!.end === DEVICE_ID_MAX.toString(10) &&
        cellSize === DEVICE_DOMAIN_END,
    },
    bridges: { accepted, rejected },
    boundaryWitnesses,
  };
}

export function analyzeAcceptance(policy: AcceptancePolicy): AcceptanceResult {
  const model = normalizePolicy(policy);
  const audits = auditBridges(model, policy);
  const decisions = model.devices.map((device) => decisionForDevice(model, audits, device));
  const classes = classDecisions(policy, model, audits, decisions);
  const coalitions = coalitionDecisions(policy, model, audits);
  const { partitions, boundaries } = buildPartitions(model, audits);
  const risk = buildRisk(model, audits, decisions, coalitions);
  const certificate = certificateFor(model, audits, partitions, boundaries, decisions, risk);

  const result: AcceptanceResult = {
    scheme: "gsb-header-acceptance/v1",
    message: model.message,
    decisions,
    classes,
    coalitions,
    partitions,
    certificate,
    risk,
  };
  return withResultAliases(result);
}

function withResultAliases(result: AcceptanceResult): AcceptanceResult {
  Object.defineProperties(result, {
    riskVerdict: { value: result.risk, enumerable: false },
    partition: { value: result.partitions, enumerable: false },
    cert: { value: result.certificate, enumerable: false },
    authorizations: { value: result.decisions, enumerable: false },
  });
  return result;
}

function validatePartitionCells(partitions: readonly PartitionCell[]): string[] {
  const errors: string[] = [];
  let expectedStart = 0n;
  for (let index = 0; index < partitions.length; index += 1) {
    const cell = partitions[index]!;
    let start: bigint;
    let end: bigint;
    try {
      start = toDeviceId(cell.start);
      end = toDeviceId(cell.end);
    } catch {
      errors.push(`partition cell ${index} has an invalid boundary`);
      continue;
    }
    if (start !== expectedStart) {
      errors.push(`partition cell ${index} starts at ${start}, expected ${expectedStart}`);
    }
    if (start > end) errors.push(`partition cell ${index} is inverted`);
    expectedStart = end + 1n;
  }
  if (expectedStart !== DEVICE_DOMAIN_END) {
    errors.push(`partition ends at ${expectedStart}, expected ${DEVICE_DOMAIN_END}`);
  }
  return errors;
}

function isAcceptanceResult(value: unknown): value is AcceptanceResult {
  return (
    value !== null &&
    typeof value === "object" &&
    "certificate" in value &&
    "partitions" in value &&
    "decisions" in value
  );
}

function isAcceptanceCertificate(value: unknown): value is AcceptanceCertificate {
  return (
    value !== null &&
    typeof value === "object" &&
    "scheme" in value &&
    "manifest" in value &&
    "partitionDigest" in value &&
    !("partitions" in value)
  );
}

export function verifyCertificate(certificate: AcceptanceCertificate): VerificationResult {
  const errors: string[] = [];
  if (certificate.scheme !== "gsb-header-acceptance/v1") {
    errors.push("unexpected certificate scheme");
  }
  if (certificate.domainBits !== 48) errors.push("certificate domain is not 48 bits");
  const expected = analyzeAcceptance(
    policyFromManifest(certificate.manifest, certificate.bridges.accepted),
  );
  if (expected.certificate.policyDigest !== certificate.policyDigest) {
    errors.push("certificate policy digest differs from its manifest");
  }
  if (expected.certificate.partitionDigest !== certificate.partitionDigest) {
    errors.push("certificate partition digest differs on recomputation");
  }
  if (expected.certificate.decisionDigest !== certificate.decisionDigest) {
    errors.push("certificate decision digest differs on recomputation");
  }
  if (expected.certificate.riskDigest !== certificate.riskDigest) {
    errors.push("certificate risk digest differs on recomputation");
  }
  if (expected.certificate.bridgeDigest !== certificate.bridgeDigest) {
    errors.push("certificate bridge digest differs on recomputation");
  }
  if (canonicalJson(expected.certificate.boundaries) !== canonicalJson(certificate.boundaries)) {
    errors.push("certificate boundaries differ on recomputation");
  }
  if (canonicalJson(expected.certificate.coverage) !== canonicalJson(certificate.coverage)) {
    errors.push("certificate coverage differs on recomputation");
  }
  if (canonicalJson(expected.certificate.boundaryWitnesses) !== canonicalJson(certificate.boundaryWitnesses)) {
    errors.push("certificate boundary witnesses differ on recomputation");
  }
  return { ok: errors.length === 0, errors };
}

export function verifyAcceptance(certificate: AcceptanceCertificate): VerificationResult;
export function verifyAcceptance(result: AcceptanceResult, policy?: AcceptancePolicy): VerificationResult;
export function verifyAcceptance(policy: AcceptancePolicy, result: AcceptanceResult): VerificationResult;
export function verifyAcceptance(
  resultOrPolicy: AcceptanceResult | AcceptancePolicy | AcceptanceCertificate,
  policyOrResult?: AcceptancePolicy | AcceptanceResult,
): VerificationResult {
  if (isAcceptanceCertificate(resultOrPolicy)) {
    return verifyCertificate(resultOrPolicy);
  }
  const errors: string[] = [];
  const result = isAcceptanceResult(resultOrPolicy)
    ? resultOrPolicy
    : isAcceptanceResult(policyOrResult)
      ? policyOrResult
      : undefined;
  const policy = isAcceptanceResult(resultOrPolicy)
    ? isAcceptanceResult(policyOrResult)
      ? undefined
      : (policyOrResult as AcceptancePolicy | undefined)
    : (resultOrPolicy as AcceptancePolicy);
  if (result === undefined) {
    return { ok: false, errors: ["an acceptance result is required"] };
  }
  if (result === null || typeof result !== "object") {
    return { ok: false, errors: ["result is not an object"] };
  }
  if (result.scheme !== "gsb-header-acceptance/v1") {
    errors.push("unexpected acceptance scheme");
  }
  const certificate = result.certificate;
  if (certificate === undefined || certificate.manifest === undefined) {
    errors.push("certificate or manifest is missing");
    return { ok: false, errors };
  }
  if (certificate.domainBits !== 48) errors.push("certificate domain is not 48 bits");
  if (certificate.policyDigest !== canonicalDigest(certificate.manifest)) {
    errors.push("manifest digest does not match the certificate");
  }
  if (certificate.partitionDigest !== canonicalDigest(result.partitions)) {
    errors.push("partition digest does not match the partitions");
  }
  if (certificate.decisionDigest !== canonicalDigest(result.decisions)) {
    errors.push("decision digest does not match the decisions");
  }
  if (certificate.riskDigest !== canonicalDigest(result.risk)) {
    errors.push("risk digest does not match the risk verdict");
  }

  errors.push(...validatePartitionCells(result.partitions));
  if (certificate.coverage.cells !== result.partitions.length) {
    errors.push("coverage cell count does not match the partition");
  }
  if (certificate.coverage.total !== DEVICE_DOMAIN_END.toString(10)) {
    errors.push("coverage total does not match the 48-bit domain");
  }
  if (!certificate.coverage.complete) errors.push("coverage is not marked complete");

  const boundaries = certificate.boundaries.map((boundary) => BigInt(boundary));
  if (boundaries.length !== result.partitions.length + 1) {
    errors.push("certificate boundary count does not match the partition");
  } else {
    for (let index = 0; index < result.partitions.length; index += 1) {
      if (boundaries[index] !== BigInt(result.partitions[index]!.start)) {
        errors.push(`certificate boundary ${index} does not match the partition`);
      }
    }
    if (boundaries[boundaries.length - 1] !== DEVICE_DOMAIN_END) {
      errors.push("certificate boundary does not end at the 48-bit domain limit");
    }
  }

  const revocation = normalizeRangeSet(certificate.manifest.revocation);
  for (const witness of certificate.boundaryWitnesses) {
    const id = BigInt(witness.at);
    if (rangeContains(revocation, id) !== witness.revoked) {
      errors.push(`boundary witness ${witness.at} is inconsistent with revocation`);
    }
  }

  const expected = policy === undefined
    ? analyzeAcceptance(policyFromManifest(certificate.manifest, certificate.bridges.accepted))
    : analyzeAcceptance(policy);
  if (expected.certificate.policyDigest !== certificate.policyDigest) {
    errors.push("recomputed policy digest differs");
  }
  if (expected.certificate.partitionDigest !== certificate.partitionDigest) {
    errors.push("recomputed partition digest differs");
  }
  if (expected.certificate.decisionDigest !== certificate.decisionDigest) {
    errors.push("recomputed decision digest differs");
  }
  if (expected.certificate.riskDigest !== certificate.riskDigest) {
    errors.push("recomputed risk digest differs");
  }
  if (expected.certificate.bridgeDigest !== certificate.bridgeDigest) {
    errors.push("recomputed bridge digest differs");
  }
  if (canonicalJson(expected.partitions) !== canonicalJson(result.partitions)) {
    errors.push("recomputed partition cells differ");
  }
  return { ok: errors.length === 0, errors };
}

export function recomputeAcceptance(result: AcceptanceResult): AcceptanceResult;
export function recomputeAcceptance(policy: AcceptancePolicy): AcceptanceResult;
export function recomputeAcceptance(
  resultOrPolicy: AcceptanceResult | AcceptancePolicy,
): AcceptanceResult {
  if (isAcceptanceResult(resultOrPolicy)) {
    return analyzeAcceptance(
      policyFromManifest(
        resultOrPolicy.certificate.manifest,
        resultOrPolicy.certificate.bridges.accepted,
      ),
    );
  }
  return analyzeAcceptance(resultOrPolicy);
}

export function evaluateId(policy: AcceptancePolicy, idValue: ScalarDeviceId): IdDecision {
  const model = normalizePolicy(policy);
  const audits = auditBridges(model, policy);
  const id = toDeviceId(idValue);
  const device = model.deviceById.get(id.toString(10)) ?? syntheticDevice(model, id);
  const decision = decisionForDevice(model, audits, device);
  return {
    id: id.toString(10),
    revoked: decision.revoked,
    direct: decision.direct,
    bridged: decision.bridged,
    eligible: decision.eligible,
    authorization: decision.authorization,
  };
}

export function authorizeDevice(
  policy: AcceptancePolicy,
  deviceRef: string | ScalarDeviceId,
): DeviceDecision | undefined {
  const result = analyzeAcceptance(policy);
  const text = typeof deviceRef === "string" ? deviceRef : String(deviceRef);
  return result.decisions.find(
    (decision) => decision.device === text || decision.ids.includes(text),
  );
}

export function evaluateShares(
  policy: AcceptancePolicy,
  shares: readonly ShareInput[],
): ShareEvaluation {
  const model = normalizePolicy(policy);
  const audits = auditBridges(model, policy);
  const threshold = model.message.threshold;
  const records: ShareEvaluation["sources"][number][] = [];
  const distinct = new Set<string>();

  for (const share of shares) {
    const rawSource =
      share.source ??
      share.device ??
      share.deviceId ??
      share.alias ??
      "unknown";
    const sourceText =
      typeof rawSource === "string"
        ? rawSource
        : (() => {
            try {
              return toDeviceId(rawSource).toString(10);
            } catch {
              return String(rawSource);
            }
          })();
    const device = resolveDevice(model, sourceText);
    const physical = device?.key ?? sourceText;
    distinct.add(physical);

    let permissions: CapabilityEligibility;
    if (device !== undefined) {
      const shareHasScope =
        share.tenant !== undefined ||
        share.tenantId !== undefined ||
        share.contentGeneration !== undefined ||
        share.contentGen !== undefined ||
        share.keyGeneration !== undefined ||
        share.keyGen !== undefined ||
        share.purpose !== undefined ||
        share.usage !== undefined ||
        share.interval !== undefined ||
        share.deviceInterval !== undefined ||
        share.groupId !== undefined;
      if (!shareHasScope) {
        permissions = evaluateDeviceCapabilities(model, audits, device);
      } else {
        const capability = capabilityFromShare(model, share, device);
        permissions = device.ids.some(
          (id) =>
            rangeContains(capability.interval, id) &&
            (model.message.interval === null || rangeContains(model.message.interval, id)),
        )
          ? evaluateStandaloneCapability(model, audits, capability)
          : { direct: false, bridged: false, directCapabilities: [], bridgedCapabilities: [] };
      }
    } else {
      const capability = capabilityFromShare(model, share, undefined);
      permissions = evaluateStandaloneCapability(model, audits, capability);
    }
    const revoked = device?.revoked ?? false;
    records.push({
      source: physical,
      eligible: !revoked && (permissions.direct || permissions.bridged),
      direct: permissions.direct,
      bridged: permissions.bridged,
      revoked,
    });
  }

  const eligibleSources = new Set(
    records.filter((record) => record.eligible).map((record) => record.source),
  ).size;
  return {
    threshold,
    rawShares: shares.length,
    distinctSources: distinct.size,
    eligibleSources,
    canOpen: eligibleSources >= threshold,
    aliasCollapsed: shares.length > distinct.size,
    sources: records,
  };
}

function capabilityFromShare(
  model: NormalizedModel,
  share: ShareInput,
  device: NormalizedDevice | undefined,
): NormalizedCapability {
  const group = share.groupId === undefined ? undefined : model.groups.find((item) => item.groupId === share.groupId);
  const id = device?.ids[0] ?? 0n;
  const tenant = share.tenant ?? share.tenantId ?? group?.tenant ?? model.message.tenant;
  const contentGeneration =
    parseGenerationLoose(share.contentGeneration ?? share.contentGen) ??
    group?.contentGeneration ??
    model.message.contentGeneration;
  const keyGeneration =
    parseGenerationLoose(share.keyGeneration ?? share.keyGen) ??
    group?.keyGeneration ??
    model.message.keyGeneration;
  const purpose = share.purpose ?? share.usage ?? group?.purpose ?? model.message.purpose;
  const interval = normalizeRangeSet(
    (share.interval ?? share.deviceInterval ?? group?.interval ?? [{ start: id, end: id }]) as never,
  );
  return {
    capabilityId: `share:${device?.key ?? "anonymous"}:${rangeSignature(interval)}`,
    owner: device?.key ?? "anonymous",
    groupId: group?.groupId,
    tenant,
    contentGeneration,
    keyGeneration,
    purpose,
    interval,
  };
}

function parseGenerationLoose(value: unknown): number | undefined {
  if (value === undefined || value === null || value === "") return undefined;
  const number = typeof value === "number" ? value : Number(value);
  return Number.isSafeInteger(number) && number >= 0 ? number : undefined;
}

function evaluateStandaloneCapability(
  model: NormalizedModel,
  audits: readonly BridgeAudit[],
  capability: NormalizedCapability,
): CapabilityEligibility {
  const message = model.message;
  const direct =
    capability.tenant === message.tenant &&
    capability.contentGeneration === message.contentGeneration &&
    capability.keyGeneration === message.keyGeneration &&
    capability.purpose === message.purpose;
  const bridged = audits.some((audit) => bridgeAllowsCapability(audit, capability, model));
  return {
    direct,
    bridged,
    directCapabilities: direct ? [capability.capabilityId] : [],
    bridgedCapabilities: bridged ? [capability.capabilityId] : [],
  };
}

export function evaluateCoalition(
  policy: AcceptancePolicy,
  members: readonly (string | ScalarDeviceId)[],
): CoalitionDecision {
  const model = normalizePolicy(policy);
  const audits = auditBridges(model, policy);
  const refs = members.map((member) => {
    if (typeof member === "string") return member;
    try {
      return toDeviceId(member).toString(10);
    } catch {
      return String(member);
    }
  });
  return coalitionFromMembers(
    model,
    audits,
    `coalition:${canonicalDigest(refs).slice(0, 16)}`,
    refs,
  );
}

export function countDistinctSources(
  policy: AcceptancePolicy,
  shares: readonly ShareInput[],
): number {
  return evaluateShares(policy, shares).distinctSources;
}

export function canOpenMessage(
  policy: AcceptancePolicy,
  shares: readonly ShareInput[],
): boolean {
  return evaluateShares(policy, shares).canOpen;
}

export function resultJson(result: AcceptanceResult): string {
  return canonicalJson(result);
}

export function certificateJson(certificate: AcceptanceCertificate): string {
  return canonicalJson(certificate);
}

export function manifestJson(manifest: AcceptanceManifest): string {
  return canonicalJson(manifest);
}

export function partitionRanges(partitions: readonly PartitionCell[]): RangeSet {
  return partitions.map((cell) => ({ start: BigInt(cell.start), end: BigInt(cell.end) }));
}

/** Internal bridge used by the exhaustive verifier; kept public for audits. */
export function decideNormalizedDevice(
  model: NormalizedModel,
  audits: readonly BridgeAudit[],
  device: NormalizedDevice,
): DeviceDecision {
  return decisionForDevice(model, audits, device);
}

/** Internal bridge used by the exhaustive verifier; kept public for audits. */
export function normalizedDeviceForId(model: NormalizedModel, id: bigint): NormalizedDevice {
  return model.deviceById.get(id.toString(10)) ?? syntheticDevice(model, id);
}

/** Internal bridge used by the exhaustive verifier; kept public for audits. */
export function decideNormalizedCoalition(
  model: NormalizedModel,
  audits: readonly BridgeAudit[],
  coalitionId: string,
  memberRefs: readonly string[],
): CoalitionDecision {
  return coalitionFromMembers(model, audits, coalitionId, memberRefs);
}
