import {
  HARD_LIMITS,
  boundedInteger,
  boundedString,
  boundedStringArray,
  deepFreeze,
  exactRecord,
  hasOwn,
  isOriginInList,
  ownDataProperty,
  parseCanonicalOrigin,
  parseHostPermissionOrigin,
  reject,
  requireDataRecord,
  ValidationError,
} from "./security.ts";

export const CONFIG_MANIFEST_KEY = "extension_security";
export const CONFIG_MESSAGE_TYPE = "extension:security-config";
export const CONFIG_PROTOCOL_VERSION = 1;

export const PAGE_OPERATIONS = Object.freeze(["status", "relay"] as const);
export type PageOperation = (typeof PAGE_OPERATIONS)[number];

function isDeclaredOperation(value: string): value is PageOperation {
  for (const operation of PAGE_OPERATIONS) {
    if (operation === value) {
      return true;
    }
  }
  return false;
}

export interface SecurityConfig {
  readonly version: 1;
  readonly allowedOrigins: readonly string[];
  readonly allowedOperations: readonly PageOperation[];
  readonly maxPageMessageBytes: number;
  readonly maxPageStringLength: number;
  readonly portIdleTimeoutMs: number;
  readonly handshakeCooldownMs: number;
  readonly pageBridgeEnabled: boolean;
}

const MANIFEST_OPTION_KEYS = Object.freeze([
  "version",
  "allowedOrigins",
  "allowedOperations",
  "maxPageMessageBytes",
  "maxPageStringLength",
  "portIdleTimeoutMs",
  "handshakeCooldownMs",
  "pageBridgeEnabled",
] as const);

const CONFIG_UPDATE_KEYS = Object.freeze([
  "allowedOrigins",
  "allowedOperations",
  "maxPageMessageBytes",
  "maxPageStringLength",
  "portIdleTimeoutMs",
  "handshakeCooldownMs",
  "pageBridgeEnabled",
] as const);

const TRUSTED_MESSAGE_KEYS = Object.freeze([
  "type",
  "protocolVersion",
  "config",
] as const);

function parseOperations(
  value: unknown,
  label: string,
): readonly PageOperation[] {
  const entries = boundedStringArray(value, label, {
    maxItems: PAGE_OPERATIONS.length,
    minLength: 1,
    maxLength: HARD_LIMITS.maxOperationLength,
  });
  const output: PageOperation[] = [];
  for (const entry of entries) {
    if (!isDeclaredOperation(entry)) {
      reject("not_allowed", `${label} contains an unsupported operation`);
    }
    output.push(entry);
  }
  return Object.freeze(output);
}

function parseOrigins(
  value: unknown,
  label: string,
): readonly string[] {
  const entries = boundedStringArray(value, label, {
    maxItems: HARD_LIMITS.maxOrigins,
    minLength: 1,
    maxLength: HARD_LIMITS.maxOriginLength,
  });
  const output: string[] = [];
  for (const entry of entries) {
    output.push(parseCanonicalOrigin(entry, label));
  }
  return Object.freeze(output);
}

function parseHostPermissions(value: unknown): readonly string[] {
  const entries = boundedStringArray(value, "host_permissions", {
    maxItems: HARD_LIMITS.maxOrigins,
    minLength: 1,
    maxLength: HARD_LIMITS.maxOriginLength,
  });
  const origins: string[] = [];
  const seen = new Set<string>();
  for (let index = 0; index < entries.length; index += 1) {
    const origin = parseHostPermissionOrigin(
      entries[index],
      `host_permissions[${index}]`,
    );
    if (seen.has(origin)) {
      reject("duplicate", "host_permissions contains duplicate origins");
    }
    seen.add(origin);
    origins.push(origin);
  }
  return Object.freeze(origins);
}

function parseManifestOptions(
  value: unknown,
  hostOrigins: readonly string[],
): SecurityConfig {
  const options = exactRecord(value, MANIFEST_OPTION_KEYS, CONFIG_MANIFEST_KEY);
  const version = boundedInteger(
    ownDataProperty(options, "version", CONFIG_MANIFEST_KEY),
    `${CONFIG_MANIFEST_KEY}.version`,
    1,
    1,
  );
  if (version !== 1) {
    reject("invalid_value", "unsupported security configuration version");
  }

  let allowedOrigins: readonly string[];
  if (hasOwn(options, "allowedOrigins")) {
    allowedOrigins = parseOrigins(
      options.allowedOrigins,
      `${CONFIG_MANIFEST_KEY}.allowedOrigins`,
    );
    for (const origin of allowedOrigins) {
      if (!isOriginInList(origin, hostOrigins)) {
        reject(
          "not_allowed",
          "allowedOrigins must be covered by host_permissions",
        );
      }
    }
  } else {
    allowedOrigins = hostOrigins;
  }

  const allowedOperations = hasOwn(options, "allowedOperations")
    ? parseOperations(
        options.allowedOperations,
        `${CONFIG_MANIFEST_KEY}.allowedOperations`,
      )
    : PAGE_OPERATIONS;

  const maxPageMessageBytes = hasOwn(options, "maxPageMessageBytes")
    ? boundedInteger(
        options.maxPageMessageBytes,
        `${CONFIG_MANIFEST_KEY}.maxPageMessageBytes`,
        HARD_LIMITS.minPageMessageBytes,
        HARD_LIMITS.maxPageMessageBytes,
      )
    : 16 * 1024;

  const maxPageStringLength = hasOwn(options, "maxPageStringLength")
    ? boundedInteger(
        options.maxPageStringLength,
        `${CONFIG_MANIFEST_KEY}.maxPageStringLength`,
        HARD_LIMITS.minPageStringLength,
        HARD_LIMITS.maxPageStringLength,
      )
    : Math.min(4 * 1024, maxPageMessageBytes);

  const portIdleTimeoutMs = hasOwn(options, "portIdleTimeoutMs")
    ? boundedInteger(
        options.portIdleTimeoutMs,
        `${CONFIG_MANIFEST_KEY}.portIdleTimeoutMs`,
        1_000,
        5 * 60 * 1_000,
      )
    : 60_000;

  const handshakeCooldownMs = hasOwn(options, "handshakeCooldownMs")
    ? boundedInteger(
        options.handshakeCooldownMs,
        `${CONFIG_MANIFEST_KEY}.handshakeCooldownMs`,
        0,
        10_000,
      )
    : 250;

  let pageBridgeEnabled = true;
  if (hasOwn(options, "pageBridgeEnabled")) {
    const candidate = options.pageBridgeEnabled;
    if (typeof candidate !== "boolean") {
      reject("invalid_type", `${CONFIG_MANIFEST_KEY}.pageBridgeEnabled`);
    }
    pageBridgeEnabled = candidate;
  }

  if (maxPageStringLength > maxPageMessageBytes) {
    reject(
      "invalid_value",
      "maxPageStringLength cannot exceed maxPageMessageBytes",
    );
  }

  return deepFreeze({
    version: 1 as const,
    allowedOrigins,
    allowedOperations,
    maxPageMessageBytes,
    maxPageStringLength,
    portIdleTimeoutMs,
    handshakeCooldownMs,
    pageBridgeEnabled,
  });
}

/**
 * The manifest is extension-controlled data. Page DOM is intentionally never
 * consulted here, even when a page supplies an element with a familiar name.
 */
export function loadManifestConfig(manifest: unknown): SecurityConfig {
  const record = requireDataRecord(manifest, "manifest");
  const manifestVersion = ownDataProperty(record, "manifest_version", "manifest");
  if (manifestVersion !== 3) {
    reject("invalid_value", "only Manifest V3 is supported");
  }

  const hostOrigins = hasOwn(record, "host_permissions")
    ? parseHostPermissions(record.host_permissions)
    : Object.freeze([] as string[]);

  if (!hasOwn(record, CONFIG_MANIFEST_KEY)) {
    return deepFreeze({
      version: 1 as const,
      allowedOrigins: hostOrigins,
      allowedOperations: PAGE_OPERATIONS,
      maxPageMessageBytes: 16 * 1024,
      maxPageStringLength: 4 * 1024,
      portIdleTimeoutMs: 60_000,
      handshakeCooldownMs: 250,
      pageBridgeEnabled: true,
    });
  }

  return parseManifestOptions(
    ownDataProperty(record, CONFIG_MANIFEST_KEY, "manifest"),
    hostOrigins,
  );
}

function assertSubset(
  next: readonly string[],
  current: readonly string[],
  label: string,
): void {
  for (const entry of next) {
    let found = false;
    for (const existing of current) {
      if (existing === entry) {
        found = true;
        break;
      }
    }
    if (!found) {
      reject("not_allowed", `${label} cannot widen the manifest policy`);
    }
  }
}

/**
 * Trusted extension messages may only narrow the immutable manifest policy.
 * Updates are explicit field-by-field copies; no untrusted object is merged.
 */
export function narrowConfig(
  current: SecurityConfig,
  configUpdate: unknown,
): SecurityConfig {
  const update = exactRecord(
    configUpdate,
    CONFIG_UPDATE_KEYS,
    "configUpdate",
  );

  let allowedOrigins = current.allowedOrigins;
  if (hasOwn(update, "allowedOrigins")) {
    const candidate = parseOrigins(update.allowedOrigins, "allowedOrigins");
    assertSubset(candidate, current.allowedOrigins, "allowedOrigins");
    allowedOrigins = candidate;
  }

  let allowedOperations = current.allowedOperations;
  if (hasOwn(update, "allowedOperations")) {
    const candidate = parseOperations(
      update.allowedOperations,
      "allowedOperations",
    );
    assertSubset(candidate, current.allowedOperations, "allowedOperations");
    allowedOperations = candidate;
  }

  let maxPageMessageBytes = current.maxPageMessageBytes;
  if (hasOwn(update, "maxPageMessageBytes")) {
    const candidate = boundedInteger(
      update.maxPageMessageBytes,
      "maxPageMessageBytes",
      HARD_LIMITS.minPageMessageBytes,
      current.maxPageMessageBytes,
    );
    maxPageMessageBytes = candidate;
  }

  let maxPageStringLength = current.maxPageStringLength;
  if (hasOwn(update, "maxPageStringLength")) {
    const candidate = boundedInteger(
      update.maxPageStringLength,
      "maxPageStringLength",
      HARD_LIMITS.minPageStringLength,
      current.maxPageStringLength,
    );
    maxPageStringLength = candidate;
  }

  if (maxPageStringLength > maxPageMessageBytes) {
    reject(
      "invalid_value",
      "maxPageStringLength cannot exceed maxPageMessageBytes",
    );
  }

  const portIdleTimeoutMs = hasOwn(update, "portIdleTimeoutMs")
    ? boundedInteger(
        update.portIdleTimeoutMs,
        "portIdleTimeoutMs",
        1_000,
        5 * 60 * 1_000,
      )
    : current.portIdleTimeoutMs;

  const handshakeCooldownMs = hasOwn(update, "handshakeCooldownMs")
    ? boundedInteger(update.handshakeCooldownMs, "handshakeCooldownMs", 0, 10_000)
    : current.handshakeCooldownMs;

  let pageBridgeEnabled = current.pageBridgeEnabled;
  if (hasOwn(update, "pageBridgeEnabled")) {
    if (typeof update.pageBridgeEnabled !== "boolean") {
      reject("invalid_type", "pageBridgeEnabled must be a boolean");
    }
    if (update.pageBridgeEnabled && !current.pageBridgeEnabled) {
      reject("not_allowed", "a trusted update cannot re-enable the bridge");
    }
    pageBridgeEnabled = update.pageBridgeEnabled;
  }

  return deepFreeze({
    version: 1 as const,
    allowedOrigins,
    allowedOperations,
    maxPageMessageBytes,
    maxPageStringLength,
    portIdleTimeoutMs,
    handshakeCooldownMs,
    pageBridgeEnabled,
  });
}

export function parseTrustedConfigMessage(
  message: unknown,
  current: SecurityConfig,
): SecurityConfig {
  const record = exactRecord(message, TRUSTED_MESSAGE_KEYS, "trustedMessage");
  const type = boundedString(record.type, "trustedMessage.type", 1, 64);
  if (type !== CONFIG_MESSAGE_TYPE) {
    reject("not_allowed", "unexpected trusted message type");
  }
  const protocolVersion = boundedInteger(
    record.protocolVersion,
    "trustedMessage.protocolVersion",
    1,
    1,
  );
  if (protocolVersion !== CONFIG_PROTOCOL_VERSION) {
    reject("invalid_value", "unsupported trusted message protocol");
  }
  return narrowConfig(current, record.config);
}

export function isOperationAllowed(
  config: SecurityConfig,
  operation: string,
): operation is PageOperation {
  for (const allowed of config.allowedOperations) {
    if (allowed === operation) {
      return true;
    }
  }
  return false;
}

export function tryLoadManifestConfig(
  manifest: unknown,
): Readonly<
  | { ok: true; config: SecurityConfig }
  | { ok: false; error: ValidationError }
> {
  try {
    return Object.freeze({
      ok: true as const,
      config: loadManifestConfig(manifest),
    });
  } catch (error) {
    if (error instanceof ValidationError) {
      return Object.freeze({ ok: false as const, error });
    }
    throw error;
  }
}
