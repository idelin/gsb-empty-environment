import { bridgeScopeOf, extractSignature, parseOptionalGeneration } from "./bridge.ts";
import { canonicalDigest, canonicalJson } from "./canonical.ts";
import {
  emptyRange,
  fullRange,
  normalizeRangeSet,
  rangeContains,
  rangeSignature,
  rangeToJSON,
  toDeviceId,
  tryDeviceId,
  DEVICE_ID_BITS,
  type RangeSet,
  type ScalarDeviceId,
} from "./interval.ts";
import type {
  AcceptanceManifest,
  AcceptancePolicy,
  BridgeDeclarationInput,
  CapabilityInput,
  CoalitionInput,
  DeviceInput,
  EncapsulationGroupInput,
  MessageRequirementInput,
  NormalizedBridge,
  NormalizedCapability,
  NormalizedCoalition,
  NormalizedDevice,
  NormalizedGroup,
  NormalizedMessage,
  NormalizedModel,
} from "./types.ts";

function firstDefined(...values: unknown[]): unknown {
  for (const value of values) {
    if (value !== undefined && value !== null && value !== "") return value;
  }
  return undefined;
}

function firstString(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value === "string" && value.length > 0) return value;
  }
  return undefined;
}

function field(object: object, ...keys: string[]): unknown {
  const record = object as Record<string, unknown>;
  for (const key of keys) {
    if (record[key] !== undefined) return record[key];
  }
  return undefined;
}

function asArray<T>(value: readonly T[] | T | undefined): readonly T[] {
  if (value === undefined) return [];
  return Array.isArray(value) ? value : [value as T];
}

function pointRange(id: bigint): RangeSet {
  return [{ start: id, end: id }];
}

function autoGroupId(scope: {
  tenant: string;
  contentGeneration: number;
  keyGeneration: number;
  purpose: string;
  interval: RangeSet;
}): string {
  return `group:${canonicalDigest({
    tenant: scope.tenant,
    contentGeneration: scope.contentGeneration,
    keyGeneration: scope.keyGeneration,
    purpose: scope.purpose,
    interval: rangeToJSON(scope.interval),
  }).slice(0, 24)}`;
}

function tenantOf(value: object): string {
  return (
    firstString(
      field(value, "tenant", "tenantId", "tenantName", "permanentTenant"),
      field(value, "tenantKey"),
    ) ?? "default"
  );
}

function purposeOf(value: object, fallback = "content"): string {
  return firstString(field(value, "purpose", "usage", "use", "intent"), fallback) ?? fallback;
}

function contentGenerationOf(value: object, fallback = 0): number {
  const raw = field(
    value,
    "contentGeneration",
    "contentGen",
    "contentRound",
    "roundGeneration",
    "round",
    "contentEpoch",
    "epoch",
  );
  return parseOptionalGeneration(raw, "contentGeneration") ?? fallback;
}

function keyGenerationOf(value: object, fallback = 0): number {
  const raw = field(value, "keyGeneration", "keyGen", "generation", "oldKeyGeneration", "keyEpoch");
  return parseOptionalGeneration(raw, "keyGeneration") ?? fallback;
}

function intervalOf(value: object, fallback?: RangeSet): RangeSet {
  const raw = field(
    value,
    "interval",
    "deviceInterval",
    "range",
    "deviceRange",
    "idRange",
    "deviceIdRange",
    "allowedInterval",
  );
  if (raw !== undefined) return normalizeRangeSet(raw as never);
  return fallback ?? emptyRange();
}

function groupFromInput(input: EncapsulationGroupInput): NormalizedGroup {
  const tenant = tenantOf(input);
  const contentGeneration = contentGenerationOf(input);
  const keyGeneration = keyGenerationOf(input);
  const purpose = purposeOf(input);
  const interval = intervalOf(input);
  const groupId =
    firstString(input.groupId, input.id, field(input, "keyId")) ??
    autoGroupId({ tenant, contentGeneration, keyGeneration, purpose, interval });
  return { groupId, tenant, contentGeneration, keyGeneration, purpose, interval };
}

function normalizeMessage(policy: AcceptancePolicy, groups: readonly NormalizedGroup[]): NormalizedMessage {
  const input: MessageRequirementInput =
    policy.message ??
    policy.requirement ??
    (policy.messageRequirement as MessageRequirementInput | undefined) ??
    ({} as MessageRequirementInput);
  const tenants = [...new Set(groups.map((group) => group.tenant))];
  const tenant =
    firstString(field(input, "tenant", "tenantId", "tenantKey")) ??
    (tenants.length === 1 ? tenants[0]! : tenants[0] ?? "default");
  const contentGeneration =
    parseOptionalGeneration(
      field(input, "contentGeneration", "contentGen", "contentRound", "contentEpoch", "epoch"),
      "message contentGeneration",
    ) ?? (groups.length > 0 ? Math.max(...groups.map((group) => group.contentGeneration)) : 0);
  const keyGeneration =
    parseOptionalGeneration(
      field(input, "keyGeneration", "keyGen", "generation", "keyEpoch"),
      "message keyGeneration",
    ) ?? (groups.length > 0 ? Math.max(...groups.map((group) => group.keyGeneration)) : 0);
  const purpose = purposeOf(input);
  const threshold = parseThresholdValue(
    firstDefined(
      field(input, "threshold", "requiredShares", "requiredSources", "shares"),
      field(input, "k", "quorum", "minimumSources", "minSources", "required", "needed"),
      policy.threshold,
    ),
    1,
  );
  const intervalRaw = field(
    input,
    "interval",
    "recipients",
    "deviceInterval",
    "audience",
    "idRange",
    "deviceIdRange",
  );
  const interval =
    intervalRaw === undefined || intervalRaw === null
      ? null
      : normalizeRangeSet(intervalRaw as never);
  return { tenant, contentGeneration, keyGeneration, purpose, threshold, interval };
}

function parseThresholdValue(value: unknown, fallback: number): number {
  if (value === undefined || value === null) return fallback;
  const number = typeof value === "number" ? value : Number(value);
  if (!Number.isSafeInteger(number) || number < 1) {
    throw new RangeError("threshold must be a positive integer");
  }
  return number;
}

const SHORTCUT_FIELDS = [
  "name",
  "deviceName",
  "deviceNames",
  "membership",
  "membershipIds",
  "members",
  "member",
  "memberIds",
  "groupName",
  "aliases",
] as const;

function normalizeBridge(
  input: BridgeDeclarationInput,
  index: number,
  message: NormalizedMessage,
): NormalizedBridge {
  const shortcutFields = SHORTCUT_FIELDS.filter(
    (name) => (input as Record<string, unknown>)[name] !== undefined,
  );
  const tenantProvided =
    firstString(field(input, "tenant", "tenantId", "tenantName")) !== undefined;
  let tenant: string | null = null;
  let contentGeneration: number | null = null;
  let keyGeneration: number | null = null;
  let purpose: string | null = null;
  let interval: RangeSet = [];
  let bridgeId = firstString(input.bridgeId, input.id) ?? `bridge:invalid:${index}`;
  let signature: string | null = null;

  try {
    const scope = bridgeScopeOf(input);
    tenant = scope.tenant;
    contentGeneration = scope.contentGeneration;
    keyGeneration = scope.keyGeneration;
    purpose = scope.purpose;
    interval = scope.interval;
    bridgeId = scope.bridgeId;
    signature = extractSignature(input);
  } catch {
    tenant =
      firstString(field(input, "tenant", "tenantId"), message.tenant) ?? null;
    contentGeneration = safeOptionalGeneration(
      field(input, "contentGeneration", "contentGen", "roundGeneration", "round"),
    );
    keyGeneration = safeOptionalGeneration(
      field(input, "keyGeneration", "keyGen", "generation", "oldKeyGeneration"),
    );
    purpose = firstString(field(input, "purpose", "usage", "use")) ?? null;
    try {
      interval = normalizeRangeSet(
        field(input, "interval", "deviceInterval", "range", "deviceRange") as never,
      );
    } catch {
      interval = [];
    }
    signature = extractSignature(input);
  }

  const tenantInferred = !tenantProvided;
  if (tenantInferred) tenant = message.tenant;
  const roundUnbound = contentGeneration === null;

  return {
    bridgeId,
    tenant,
    contentGeneration,
    keyGeneration,
    purpose,
    interval,
    signature,
    signedFlag:
      input.signed === true ||
      (typeof input.signature === "object" && input.signature !== null && input.signature.value !== undefined),
    shortcutFields,
    tenantInferred,
    roundUnbound,
  };
}

function safeOptionalGeneration(value: unknown): number | null {
  try {
    return parseOptionalGeneration(value, "generation");
  } catch {
    return null;
  }
}

function capabilityFromGroup(
  owner: string,
  group: NormalizedGroup,
  capabilityId?: string,
): NormalizedCapability {
  return {
    capabilityId:
      capabilityId ??
      `cap:${owner}:${group.groupId}:c${group.contentGeneration}:k${group.keyGeneration}:${rangeSignature(
        group.interval,
      )}`,
    owner,
    groupId: group.groupId,
    tenant: group.tenant,
    contentGeneration: group.contentGeneration,
    keyGeneration: group.keyGeneration,
    purpose: group.purpose,
    interval: group.interval,
  };
}

function capabilityFromInput(
  owner: string,
  input: CapabilityInput,
  groups: ReadonlyMap<string, NormalizedGroup>,
  deviceId: bigint,
): NormalizedCapability {
  const groupRef = firstString(input.groupId, field(input, "group"));
  let group = groupRef === undefined ? undefined : groups.get(groupRef);
  if (group === undefined && groupRef !== undefined) {
    group = [...groups.values()].find((candidate) => candidate.groupId.startsWith(`${groupRef}#`));
  }
  const tenant = firstString(field(input, "tenant", "tenantId"), group?.tenant) ?? "default";
  const contentGeneration =
    parseOptionalGeneration(
      field(input, "contentGeneration", "contentGen", "contentRound", "contentEpoch", "epoch"),
      "capability contentGeneration",
    ) ??
    group?.contentGeneration ??
    0;
  const keyGeneration =
    parseOptionalGeneration(
      field(input, "keyGeneration", "keyGen", "generation", "oldKeyGeneration", "keyEpoch"),
      "capability keyGeneration",
    ) ??
    group?.keyGeneration ??
    0;
  const purpose =
    firstString(field(input, "purpose", "usage", "use", "intent"), group?.purpose) ?? "content";
  const interval = intervalOf(input as object, group?.interval ?? pointRange(deviceId));
  const capabilityId =
    firstString(input.capabilityId, input.keyId, field(input, "id")) ??
    `cap:${owner}:${group?.groupId ?? "anon"}:c${contentGeneration}:k${keyGeneration}:${purpose}:${rangeSignature(
      interval,
    )}`;
  return {
    capabilityId,
    owner,
    groupId: group?.groupId,
    tenant,
    contentGeneration,
    keyGeneration,
    purpose,
    interval,
  };
}

interface MutableDevice {
  key: string;
  ids: Set<bigint>;
  aliases: Set<string>;
  label?: string;
  className?: string;
  capabilities: Map<string, NormalizedCapability>;
  revoked: boolean;
  explicitCapabilities: boolean;
}

function mergeDevice(
  devices: Map<string, MutableDevice>,
  input: DeviceInput,
  groups: ReadonlyMap<string, NormalizedGroup>,
  deriveCapabilities: boolean,
): void {
  const rawId = field(input, "id", "deviceId");
  const aliases = [
    ...asArray(input.aliases),
    ...asArray(field(input, "names", "deviceAliases") as unknown[] | undefined),
  ];
  let canonicalId: bigint;
  try {
    canonicalId = toDeviceId(
      (rawId ?? aliases.find((alias) => tryDeviceId(alias) !== undefined) ?? 0) as ScalarDeviceId,
    );
  } catch {
    throw new TypeError("a device must carry a valid 48-bit id or numeric alias");
  }

  const physical =
    firstString(
      input.physicalId,
      input.physicalDeviceId,
      input.identity,
      field(input, "deviceKey", "canonicalId", "physicalKey"),
    ) ??
    `device:${canonicalId.toString(10)}`;

  let record = devices.get(physical);
  if (record === undefined) {
    record = {
      key: physical,
      ids: new Set<bigint>(),
      aliases: new Set<string>(),
      label: firstString(input.label, field(input, "name")),
      className: firstString(input.className, input.class, field(input, "classLabel")),
      capabilities: new Map<string, NormalizedCapability>(),
      revoked: input.revoked === true,
      explicitCapabilities: false,
    };
    devices.set(physical, record);
  } else {
    record.revoked ||= input.revoked === true;
    record.label ??= firstString(input.label, field(input, "name"));
    record.className ??= firstString(input.className, input.class, field(input, "classLabel"));
  }

  record.ids.add(canonicalId);
  record.aliases.add(canonicalId.toString(10));
  for (const alias of aliases) {
    if (alias === undefined || alias === null) continue;
    const aliasText = typeof alias === "string" ? alias : String(alias);
    if (aliasText.length > 0) record.aliases.add(aliasText);
    const aliasId = tryDeviceId(alias);
    if (aliasId !== undefined) record.ids.add(aliasId);
  }

  const explicitCaps =
    (Array.isArray(input.capabilities) ? input.capabilities : undefined) ??
    (Array.isArray(input.keys) ? input.keys : undefined) ??
    (Array.isArray(field(input, "credentials", "caps")) ? (field(input, "credentials", "caps") as CapabilityInput[]) : undefined);
  const declaredCaps = explicitCaps !== undefined;
  const groupRefs = [
    ...asArray(input.groups),
    ...asArray(field(input, "groupIds", "memberships") as unknown[] | undefined),
  ] as (string | EncapsulationGroupInput)[];

  if (declaredCaps) {
    record.explicitCapabilities = true;
    for (const rawCapability of explicitCaps ?? []) {
      const capability = capabilityFromInput(physical, rawCapability, groups, canonicalId);
      record.capabilities.set(capability.capabilityId, capability);
    }
  }

  for (const groupRef of groupRefs) {
    let group: NormalizedGroup | undefined;
    if (typeof groupRef === "string") {
      const matches = [...groups.values()].filter(
        (candidate) =>
          candidate.groupId === groupRef || candidate.groupId.startsWith(`${groupRef}#`),
      );
      if (matches.length > 0) {
        for (const match of matches) {
          const capability = capabilityFromGroup(physical, match);
          record.capabilities.set(capability.capabilityId, capability);
        }
        record.explicitCapabilities = true;
        continue;
      }
    } else if (groupRef !== null && typeof groupRef === "object") {
      group = groupFromInput(groupRef);
    }
    if (group === undefined) continue;
    const capability = capabilityFromGroup(physical, group);
    record.capabilities.set(capability.capabilityId, capability);
    record.explicitCapabilities = true;
  }

  if (
    deriveCapabilities &&
    !record.explicitCapabilities &&
    record.capabilities.size === 0
  ) {
    for (const group of groups.values()) {
      if (record.ids.size > 0 && [...record.ids].some((id) => rangeContains(group.interval, id))) {
        const capability = capabilityFromGroup(physical, group);
        record.capabilities.set(capability.capabilityId, capability);
      }
    }
  }
}

function normalizeRevocation(policy: AcceptancePolicy): RangeSet {
  const explicitDevices = policy.revokedDevices;
  if (Array.isArray(explicitDevices) && explicitDevices.every((item) => tryDeviceId(item) !== undefined)) {
    return normalizeRangeSet(
      explicitDevices.map((item) => {
        const id = toDeviceId(item as ScalarDeviceId);
        return [id, id] as const;
      }),
    );
  }
  const raw =
    policy.revoked ??
    policy.revocation ??
    policy.revokedIntervals ??
    (policy.revokedRanges as never) ??
    (policy.revokedDevices as never);
  if (raw === undefined) return [];
  return normalizeRangeSet(raw as never);
}

export function deviceClassMembers(
  policy: AcceptancePolicy,
  devices: readonly NormalizedDevice[],
): { className: string; members: string[] }[] {
  const byId = new Map<string, NormalizedDevice>();
  for (const device of devices) {
    byId.set(device.key, device);
    for (const id of device.ids) byId.set(id.toString(10), device);
    for (const alias of device.aliases) byId.set(alias, device);
  }

  const classes: { className: string; members: string[] }[] = [];
  for (const input of policy.deviceClasses ?? []) {
    const name = firstString(input.name, input.className, input.label) ?? `class:${classes.length}`;
    const members = new Set<string>();
    for (const member of asArray(input.members)) {
      if (member !== null && typeof member === "object") {
        const id = field(member as object, "id", "deviceId");
        const resolved =
          id === undefined ? undefined : tryDeviceId(id as ScalarDeviceId);
        if (resolved !== undefined) members.add(`device:${resolved.toString(10)}`);
        continue;
      }
      const ref = String(member);
      const device = byId.get(ref);
      members.add(device?.key ?? ref);
    }
    classes.push({ className: name, members: [...members].sort() });
  }

  const grouped = new Map<string, string[]>();
  for (const device of devices) {
    if (device.className !== undefined) {
      const list = grouped.get(device.className) ?? [];
      list.push(device.key);
      grouped.set(device.className, list);
    }
  }
  for (const [className, members] of grouped) {
    classes.push({ className, members: members.sort() });
  }
  return classes;
}

function normalizeCoalitions(policy: AcceptancePolicy): NormalizedCoalition[] {
  const inputs = [
    ...asArray(policy.allowedCoalitions),
    ...asArray(policy.coalitions),
  ] as CoalitionInput[];
  const result: NormalizedCoalition[] = [];
  const seen = new Set<string>();
  for (let index = 0; index < inputs.length; index += 1) {
    const input = inputs[index]!;
    const members = asArray(input.members ?? input.devices);
    const memberRefs = members.map((member) => {
      if (member !== null && typeof member === "object") {
        const id = field(member as object, "id", "deviceId", "value");
        if (id !== undefined) return String(id);
      }
      return String(member);
    });
    const coalitionId =
      firstString(input.coalitionId, input.id, input.name) ??
      `coalition:${canonicalDigest(memberRefs).slice(0, 16)}`;
    if (seen.has(coalitionId)) continue;
    seen.add(coalitionId);
    result.push({ coalitionId, memberRefs });
  }
  return result.sort((a, b) => a.coalitionId.localeCompare(b.coalitionId));
}

export function normalizePolicy(policy: AcceptancePolicy = {}): NormalizedModel {
  if (policy.domainBits !== undefined && policy.domainBits !== DEVICE_ID_BITS) {
    throw new RangeError(`only ${DEVICE_ID_BITS}-bit device ids are supported`);
  }
  const deriveCapabilities = policy.deriveCapabilities !== false;

  const groupInputs = [
    ...asArray(policy.groups),
    ...asArray(policy.encapsulationGroups),
  ] as EncapsulationGroupInput[];
  const groups: NormalizedGroup[] = [];
  const groupMap = new Map<string, NormalizedGroup>();
  for (const input of groupInputs) {
    let group = groupFromInput(input);
    const existing = groupMap.get(group.groupId);
    if (existing !== undefined) {
      const identical =
        existing.tenant === group.tenant &&
        existing.contentGeneration === group.contentGeneration &&
        existing.keyGeneration === group.keyGeneration &&
        existing.purpose === group.purpose &&
        rangeSignature(existing.interval) === rangeSignature(group.interval);
      if (identical) continue;
      let suffix = 2;
      let candidateId = `${group.groupId}#${suffix}`;
      while (groupMap.has(candidateId)) {
        suffix += 1;
        candidateId = `${group.groupId}#${suffix}`;
      }
      group = { ...group, groupId: candidateId };
    }
    groupMap.set(group.groupId, group);
    groups.push(group);
  }
  groups.sort((a, b) => a.groupId.localeCompare(b.groupId));

  const message = normalizeMessage(policy, groups);
  const bridges = [
    ...asArray(policy.bridges),
    ...asArray(policy.bridgeDeclarations),
  ].map((input, index) => normalizeBridge(input as BridgeDeclarationInput, index, message));
  bridges.sort((a, b) => a.bridgeId.localeCompare(b.bridgeId));

  const deviceMap = new Map<string, MutableDevice>();
  for (const input of asArray(policy.devices)) {
    mergeDevice(deviceMap, input, groupMap, deriveCapabilities);
  }
  for (const deviceClass of asArray(policy.deviceClasses)) {
    const className = firstString(deviceClass.name, deviceClass.className, deviceClass.label);
    for (const device of asArray(deviceClass.devices)) {
      mergeDevice(
        deviceMap,
        className === undefined ? device : { ...device, className: device.className ?? className },
        groupMap,
        deriveCapabilities,
      );
    }
    for (const member of asArray(deviceClass.members)) {
      if (member !== null && typeof member === "object" && !Array.isArray(member)) {
        mergeDevice(
          deviceMap,
          className === undefined ? (member as DeviceInput) : { ...(member as DeviceInput), className },
          groupMap,
          deriveCapabilities,
        );
      }
    }
  }

  const revocation = normalizeRevocation(policy);
  const aliasToDevice = new Map<string, string>();
  const aliasConflicts: string[] = [];
  const registerAlias = (alias: string, key: string): void => {
    const previous = aliasToDevice.get(alias);
    if (previous !== undefined && previous !== key) {
      aliasConflicts.push(`${alias}:${previous}:${key}`);
      return;
    }
    aliasToDevice.set(alias, key);
  };

  const devices: NormalizedDevice[] = [];
  for (const record of deviceMap.values()) {
    const ids = [...record.ids].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
    const aliases = [...record.aliases].sort();
    const revoked =
      record.revoked || ids.some((id) => rangeContains(revocation, id));
    const capabilities = [...record.capabilities.values()].sort((a, b) =>
      a.capabilityId.localeCompare(b.capabilityId),
    );
    const device: NormalizedDevice = {
      key: record.key,
      ids,
      aliases,
      label: record.label,
      className: record.className,
      capabilities,
      revoked,
      explicitCapabilities: record.explicitCapabilities,
    };
    devices.push(device);
    registerAlias(record.key, record.key);
    for (const id of ids) registerAlias(id.toString(10), record.key);
    for (const alias of aliases) registerAlias(alias, record.key);
  }
  devices.sort((a, b) => a.key.localeCompare(b.key));

  const deviceById = new Map<string, NormalizedDevice>();
  for (const device of devices) {
    deviceById.set(device.key, device);
    for (const id of device.ids) deviceById.set(id.toString(10), device);
    for (const alias of device.aliases) deviceById.set(alias, device);
  }

  return {
    domainBits: 48,
    groups,
    devices,
    bridges,
    revocation,
    message,
    allowedCoalitions: normalizeCoalitions(policy),
    aliasToDevice,
    deviceById,
    aliasConflicts,
    deriveCapabilities,
  };
}

export function modelToManifest(model: NormalizedModel): AcceptanceManifest {
  return {
    domainBits: 48,
    groups: model.groups.map((group) => ({
      groupId: group.groupId,
      tenant: group.tenant,
      contentGeneration: group.contentGeneration,
      keyGeneration: group.keyGeneration,
      purpose: group.purpose,
      interval: rangeToJSON(group.interval),
    })),
    devices: model.devices.map((device) => ({
      key: device.key,
      ids: device.ids.map((id) => id.toString(10)),
      aliases: device.aliases,
      ...(device.label === undefined ? {} : { label: device.label }),
      ...(device.className === undefined ? {} : { className: device.className }),
      revoked: device.revoked,
      capabilities: device.capabilities.map((capability) => ({
        capabilityId: capability.capabilityId,
        ...(capability.groupId === undefined ? {} : { groupId: capability.groupId }),
        tenant: capability.tenant,
        contentGeneration: capability.contentGeneration,
        keyGeneration: capability.keyGeneration,
        purpose: capability.purpose,
        interval: rangeToJSON(capability.interval),
      })),
    })),
    bridges: model.bridges.map((bridge) => ({
      bridgeId: bridge.bridgeId,
      tenant: bridge.tenant,
      contentGeneration: bridge.contentGeneration,
      keyGeneration: bridge.keyGeneration,
      purpose: bridge.purpose,
      interval: rangeToJSON(bridge.interval),
      signature: bridge.signature,
      signedFlag: bridge.signedFlag,
      shortcutFields: bridge.shortcutFields,
      tenantInferred: bridge.tenantInferred,
      roundUnbound: bridge.roundUnbound,
    })),
    revocation: rangeToJSON(model.revocation),
    message: {
      tenant: model.message.tenant,
      contentGeneration: model.message.contentGeneration,
      keyGeneration: model.message.keyGeneration,
      purpose: model.message.purpose,
      threshold: model.message.threshold,
      interval: model.message.interval === null ? null : rangeToJSON(model.message.interval),
    },
    allowedCoalitions: model.allowedCoalitions.map((coalition) => ({
      coalitionId: coalition.coalitionId,
      memberRefs: coalition.memberRefs,
    })),
  };
}

export function policyFromManifest(
  manifest: AcceptanceManifest,
  trustedBridgeIds: readonly string[] = [],
): AcceptancePolicy {
  return {
    domainBits: manifest.domainBits,
    groups: manifest.groups.map((group) => ({
      groupId: group.groupId,
      tenant: group.tenant,
      contentGeneration: group.contentGeneration,
      keyGeneration: group.keyGeneration,
      purpose: group.purpose,
      interval: group.interval,
    })),
    devices: manifest.devices.map((device) => ({
      id: device.ids[0]!,
      physicalId: device.key,
      aliases: device.aliases,
      label: device.label,
      className: device.className,
      revoked: device.revoked,
      capabilities: device.capabilities.map((capability) => ({
        capabilityId: capability.capabilityId,
        groupId: capability.groupId,
        tenant: capability.tenant,
        contentGeneration: capability.contentGeneration,
        keyGeneration: capability.keyGeneration,
        purpose: capability.purpose,
        interval: capability.interval,
      })),
    })),
    bridges: manifest.bridges.map((bridge) => {
      const raw: BridgeDeclarationInput = {
        bridgeId: bridge.bridgeId,
        tenant: bridge.tenantInferred ? undefined : bridge.tenant ?? undefined,
        contentGeneration: bridge.contentGeneration ?? undefined,
        keyGeneration: bridge.keyGeneration ?? undefined,
        purpose: bridge.purpose ?? undefined,
        interval: bridge.interval,
        signature: bridge.signature ?? undefined,
        signed: bridge.signedFlag,
      };
      for (const shortcut of bridge.shortcutFields) {
        (raw as Record<string, unknown>)[shortcut] = "__manifest__";
      }
      return raw;
    }),
    revocation: manifest.revocation,
    message: manifest.message,
    allowedCoalitions: manifest.allowedCoalitions.map((coalition) => ({
      coalitionId: coalition.coalitionId,
      members: coalition.memberRefs,
    })),
    deriveCapabilities: false,
    trustedBridgeIds,
    rejectUnverifiedSignatures: true,
  };
}

export function normalizedManifestDigest(manifest: AcceptanceManifest): string {
  return canonicalDigest(manifest);
}

/** Re-export for callers that want a stable string form without hashing. */
export function normalizedManifestJson(manifest: AcceptanceManifest): string {
  return canonicalJson(manifest);
}

export function fullDeviceDomain(): RangeSet {
  return fullRange();
}
