/**
 * Privacy-preserving filtering for aggregate attribution records.
 *
 * The filter only accepts aggregate pairs whose source and trigger identifiers
 * are numeric. It rejects records that expose a linkable user identifier or a
 * raw IP address, and pairs whose destinations disagree across sites.
 */

export type RejectionReason =
  | "invalid-record"
  | "invalid-source-id"
  | "invalid-trigger-id"
  | "identifier-mismatch"
  | "user-identifier"
  | "raw-ip"
  | "invalid-destination"
  | "destination-mismatch"
  | "cross-site-unverifiable";

export interface RejectedAggregateRecord {
  index: number;
  record: unknown;
  reason: RejectionReason;
  detail: string;
}

export interface AggregateFilterResult<T = unknown> {
  accepted: T[];
  rejected: RejectedAggregateRecord[];
}

export interface AggregateRecordValidationSuccess {
  ok: true;
  sourceId: number;
  triggerId: number;
  /** null means the record did not carry enough origin information to decide. */
  sameSite: boolean | null;
  destination?: string;
}

export interface AggregateRecordValidationFailure {
  ok: false;
  reason: RejectionReason;
  detail: string;
}

export type AggregateRecordValidation =
  | AggregateRecordValidationSuccess
  | AggregateRecordValidationFailure;

type PlainObject = Record<string, unknown>;

const USER_IDENTIFIER_KEYS = new Set([
  "uid",
  "userid",
  "useridentifier",
  "userkey",
  "usertoken",
  "user",
  "username",
  "email",
  "emailaddress",
  "phone",
  "phonenumber",
  "msisdn",
  "deviceid",
  "deviceidentifier",
  "advertisingid",
  "adid",
  "idfa",
  "gaid",
  "clientid",
  "clientidentifier",
  "customerid",
  "accountid",
  "subscriberid",
  "memberid",
  "visitorid",
  "sessionid",
  "cookie",
  "cookieid",
  "gclid",
  "dclid",
  "fbclid",
  "wbraid",
  "gbraid",
  "clickid",
  "fingerprint",
  "browserfingerprint",
  "ssn",
  "taxid",
]);

const USER_IDENTIFIER_PATTERN =
  /^(?:user|client|customer|account|subscriber|member|visitor|device|advertising|browser|cookie|session)(?:id|identifier|key|token|hash)$/;

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

const RAW_IP_KEYS = new Set([
  "ip",
  "ipaddress",
  "ipaddr",
  "ipv4",
  "ipv6",
  "clientip",
  "remoteip",
  "sourceip",
  "userip",
  "requestip",
  "serverip",
  "peerip",
  "proxyip",
  "forwardedip",
  "originip",
  "networkip",
  "hostip",
  "xforwardedfor",
  "forwardedfor",
  "xrealip",
  "trueclientip",
  "hashedip",
  "iphash",
]);

const RAW_IP_KEY_PATTERN =
  /^(?:raw|client|remote|source|user|request|server|peer|proxy|forwarded|origin|network|host)?(?:ip|ipaddress|ipaddr|ipv4|ipv6)$/;

const SOURCE_ID_KEYS = ["sourceId", "sourceID", "source_id", "sourceEventId", "source_event_id"];
const TRIGGER_ID_KEYS = ["triggerId", "triggerID", "trigger_id", "triggerEventId", "trigger_event_id"];
const NESTED_SOURCE_ID_KEYS = [...SOURCE_ID_KEYS, "id", "eventId", "event_id"];
const NESTED_TRIGGER_ID_KEYS = [...TRIGGER_ID_KEYS, "id", "eventId", "event_id"];

const SOURCE_SITE_KEYS = [
  "sourceSite",
  "source_site",
  "sourceOrigin",
  "source_origin",
  "sourceHost",
  "source_host",
  "sourceDomain",
  "source_domain",
];
const TRIGGER_SITE_KEYS = [
  "triggerSite",
  "trigger_site",
  "triggerOrigin",
  "trigger_origin",
  "triggerHost",
  "trigger_host",
  "triggerDomain",
  "trigger_domain",
];
const SITE_KEYS = ["site", "origin", "host", "domain"];

const SOURCE_DESTINATION_KEYS = [
  "sourceDestination",
  "source_destination",
  "sourceTarget",
  "source_target",
];
const TRIGGER_DESTINATION_KEYS = [
  "triggerDestination",
  "trigger_destination",
  "triggerTarget",
  "trigger_target",
];
const DESTINATION_KEYS = [
  "destination",
  "attributionDestination",
  "attribution_destination",
  "target",
  "targetSite",
  "target_site",
];

function isPlainObject(value: unknown): value is PlainObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function normalizeKey(key: string): string {
  return key.replace(/[\s_-]/g, "").toLowerCase();
}

function readByAliases(object: PlainObject, aliases: readonly string[]): unknown {
  for (const alias of aliases) {
    if (
      Object.prototype.hasOwnProperty.call(object, alias) &&
      object[alias] !== undefined
    ) {
      return object[alias];
    }
  }

  const wanted = new Set(aliases.map(normalizeKey));
  for (const key of Object.keys(object)) {
    if (wanted.has(normalizeKey(key)) && object[key] !== undefined) {
      return object[key];
    }
  }

  return undefined;
}

function readId(
  object: PlainObject,
  aliases: readonly string[],
): { found: boolean; value?: unknown } {
  const value = readByAliases(object, aliases);
  return { found: value !== undefined, value };
}

function isNumericIdentifier(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function findForbiddenField(
  value: unknown,
  path = "",
  seen = new Set<object>(),
): { kind: "user-identifier" | "raw-ip"; path: string } | undefined {
  if (Array.isArray(value)) {
    if (seen.has(value)) {
      return undefined;
    }
    seen.add(value);

    for (let index = 0; index < value.length; index += 1) {
      const found = findForbiddenField(value[index], `${path}[${index}]`, seen);
      if (found) {
        return found;
      }
    }
    return undefined;
  }

  if (!isPlainObject(value)) {
    if (typeof value === "string") {
      if (containsRawIp(value)) {
        return { kind: "raw-ip", path: path || "value" };
      }
      if (EMAIL_PATTERN.test(value.trim())) {
        return { kind: "user-identifier", path: path || "value" };
      }
    }
    return undefined;
  }

  if (seen.has(value)) {
    return undefined;
  }
  seen.add(value);

  for (const [key, child] of Object.entries(value)) {
    const childPath = path ? `${path}.${key}` : key;
    const normalized = normalizeKey(key);

    if (USER_IDENTIFIER_KEYS.has(normalized) || USER_IDENTIFIER_PATTERN.test(normalized)) {
      return { kind: "user-identifier", path: childPath };
    }

    if (RAW_IP_KEYS.has(normalized) || RAW_IP_KEY_PATTERN.test(normalized)) {
      return { kind: "raw-ip", path: childPath };
    }

    const found = findForbiddenField(child, childPath, seen);
    if (found) {
      return found;
    }
  }

  return undefined;
}

export function containsRawIp(value: string): boolean {
  const trimmed = value.trim().replace(/^\[|\]$/g, "");
  if (!trimmed) {
    return false;
  }

  if (isIpv4(trimmed) || isLikelyIpv6(trimmed)) {
    return true;
  }

  if (value.includes("://")) {
    try {
      const hostname = new URL(value).hostname.replace(/^\[|\]$/g, "");
      if (isIpv4(hostname) || isLikelyIpv6(hostname)) {
        return true;
      }
    } catch {
      // Not a URL; fall through to token scanning.
    }
  }

  const ipv4Candidates = value.match(/(?<![\d.])(?:\d{1,3}\.){3}\d{1,3}(?![\d.])/g) ?? [];
  if (ipv4Candidates.some(isIpv4)) {
    return true;
  }

  const ipv6Candidates = value.match(/(?<![0-9a-f:])(?:[0-9a-f]{0,4}:){2,7}[0-9a-f]{0,4}(?![0-9a-f:])/gi) ?? [];
  return ipv6Candidates.some(isLikelyIpv6);
}

function isIpv4(value: string): boolean {
  const parts = value.split(".");
  if (parts.length !== 4) {
    return false;
  }

  return parts.every((part) => {
    if (!/^\d{1,3}$/.test(part)) {
      return false;
    }
    const octet = Number(part);
    return octet >= 0 && octet <= 255;
  });
}

function isLikelyIpv6(value: string): boolean {
  if (!value.includes(":") || !/^[0-9a-f:.]+$/i.test(value)) {
    return false;
  }

  const hasCompression = value.includes("::");
  if (hasCompression && value.indexOf("::") !== value.lastIndexOf("::")) {
    return false;
  }

  const groups = value.split(":");
  if (groups.some((group) => group.length > 4 || !/^[0-9a-f]*$/i.test(group))) {
    return false;
  }

  if (hasCompression) {
    return groups.length >= 3 && groups.length <= 9;
  }

  // A bare, fully expanded IPv6 address has eight groups. Requiring a hex
  // letter or eight groups avoids mistaking clock times such as 12:30:45 for
  // an address.
  return groups.length === 8 || /[a-f]/i.test(value);
}

function normalizeSite(value: string): string {
  const trimmed = value.trim().toLowerCase();
  if (!trimmed) {
    return "";
  }

  try {
    const url = new URL(trimmed.includes("://") ? trimmed : `https://${trimmed}`);
    return url.hostname.replace(/\.$/, "");
  } catch {
    return trimmed.replace(/\/+$/, "");
  }
}

function readString(
  object: PlainObject | undefined,
  aliases: readonly string[],
): { found: boolean; value?: string; invalid: boolean } {
  if (!object) {
    return { found: false, invalid: false };
  }

  const raw = readByAliases(object, aliases);
  if (raw === undefined) {
    return { found: false, invalid: false };
  }
  if (typeof raw !== "string" || raw.trim() === "") {
    return { found: true, invalid: true };
  }
  return { found: true, value: raw, invalid: false };
}

function readAllStrings(
  object: PlainObject | undefined,
  aliases: readonly string[],
): { values: string[]; invalid: boolean } {
  if (!object) {
    return { values: [], invalid: false };
  }

  const wanted = new Set(aliases.map(normalizeKey));
  const values: string[] = [];
  let invalid = false;

  for (const [key, raw] of Object.entries(object)) {
    if (!wanted.has(normalizeKey(key))) {
      continue;
    }
    if (typeof raw !== "string" || raw.trim() === "") {
      invalid = true;
    } else {
      values.push(raw);
    }
  }

  return { values, invalid };
}

function fail(reason: RejectionReason, detail: string): AggregateRecordValidationFailure {
  return { ok: false, reason, detail };
}

function compareDuplicateIds(
  first: { found: boolean; value?: unknown },
  second: { found: boolean; value?: unknown },
  label: "source" | "trigger",
): AggregateRecordValidationFailure | undefined {
  if (first.found && second.found && first.value !== second.value) {
    return fail(
      "identifier-mismatch",
      `${label} identifier appears more than once with different values`,
    );
  }
  return undefined;
}

/**
 * Validate one aggregate record without mutating it.
 */
export function validateAggregateRecord(record: unknown): AggregateRecordValidation {
  if (!isPlainObject(record)) {
    return fail("invalid-record", "record must be a plain object");
  }

  const forbidden = findForbiddenField(record);
  if (forbidden) {
    return fail(
      forbidden.kind,
      `${forbidden.kind === "user-identifier" ? "user identifier" : "raw IP"} at ${forbidden.path}`,
    );
  }

  const sourceObject = isPlainObject(record.source) ? record.source : undefined;
  const triggerObject = isPlainObject(record.trigger) ? record.trigger : undefined;

  const directSourceId = readId(record, SOURCE_ID_KEYS);
  const directTriggerId = readId(record, TRIGGER_ID_KEYS);
  const nestedSourceId = sourceObject
    ? readId(sourceObject, NESTED_SOURCE_ID_KEYS)
    : typeof record.source === "number"
      ? { found: true, value: record.source }
      : { found: false };
  const nestedTriggerId = triggerObject
    ? readId(triggerObject, NESTED_TRIGGER_ID_KEYS)
    : typeof record.trigger === "number"
      ? { found: true, value: record.trigger }
      : { found: false };

  const sourceId = directSourceId.found ? directSourceId.value : nestedSourceId.value;
  const triggerId = directTriggerId.found ? directTriggerId.value : nestedTriggerId.value;

  if (!isNumericIdentifier(sourceId)) {
    return fail("invalid-source-id", "source identifier must be a non-negative safe integer");
  }
  if (!isNumericIdentifier(triggerId)) {
    return fail("invalid-trigger-id", "trigger identifier must be a non-negative safe integer");
  }

  const sourceMismatch = compareDuplicateIds(directSourceId, nestedSourceId, "source");
  if (sourceMismatch) {
    return sourceMismatch;
  }
  const triggerMismatch = compareDuplicateIds(directTriggerId, nestedTriggerId, "trigger");
  if (triggerMismatch) {
    return triggerMismatch;
  }

  const sourceSite = readString(record, SOURCE_SITE_KEYS);
  const triggerSite = readString(record, TRIGGER_SITE_KEYS);
  const nestedSourceSite = readString(sourceObject, SITE_KEYS);
  const nestedTriggerSite = readString(triggerObject, SITE_KEYS);

  if (
    sourceSite.invalid ||
    triggerSite.invalid ||
    nestedSourceSite.invalid ||
    nestedTriggerSite.invalid
  ) {
    return fail("invalid-destination", "site/origin fields must be non-empty strings");
  }

  const sourceOrigin = sourceSite.value ?? nestedSourceSite.value;
  const triggerOrigin = triggerSite.value ?? nestedTriggerSite.value;

  const sourceDestination = readString(record, SOURCE_DESTINATION_KEYS);
  const triggerDestination = readString(record, TRIGGER_DESTINATION_KEYS);
  const nestedSourceDestination = readString(sourceObject, ["destination", "target"]);
  const nestedTriggerDestination = readString(triggerObject, ["destination", "target"]);
  const sharedDestination = readString(record, DESTINATION_KEYS);
  const sharedDestinations = readAllStrings(record, DESTINATION_KEYS);

  if (
    sourceDestination.invalid ||
    triggerDestination.invalid ||
    nestedSourceDestination.invalid ||
    nestedTriggerDestination.invalid ||
    sharedDestination.invalid ||
    sharedDestinations.invalid
  ) {
    return fail("invalid-destination", "destination fields must be non-empty strings");
  }

  const leftDestination =
    sourceDestination.value ?? nestedSourceDestination.value ?? sharedDestination.value;
  const rightDestination =
    triggerDestination.value ?? nestedTriggerDestination.value ?? sharedDestination.value;

  const declaredDestinations = [
    sourceDestination.value,
    triggerDestination.value,
    nestedSourceDestination.value,
    nestedTriggerDestination.value,
    ...sharedDestinations.values,
  ].filter((destination): destination is string => destination !== undefined);
  const normalizedDestinations = new Set(declaredDestinations.map(normalizeSite));

  if (normalizedDestinations.size > 1) {
    return fail(
      "destination-mismatch",
      `record declares conflicting destinations: ${[...normalizedDestinations].join(", ")}`,
    );
  }

  if (
    leftDestination &&
    rightDestination &&
    normalizeSite(leftDestination) !== normalizeSite(rightDestination)
  ) {
    return fail(
      "destination-mismatch",
      `source destination ${leftDestination} does not match trigger destination ${rightDestination}`,
    );
  }

  let sameSite: boolean | null = null;
  if (sourceOrigin && triggerOrigin) {
    sameSite = normalizeSite(sourceOrigin) === normalizeSite(triggerOrigin);
  } else if (typeof record.crossSite === "boolean") {
    sameSite = !record.crossSite;
  } else if (typeof record.cross_site === "boolean") {
    sameSite = !record.cross_site;
  }

  if (sameSite === false) {
    const declaredDestination = leftDestination ?? rightDestination;
    if (!declaredDestination) {
      return fail(
        "cross-site-unverifiable",
        "cross-site pair has no consistent destination to verify",
      );
    }
    if (sourceOrigin && triggerOrigin) {
      const destination = normalizeSite(declaredDestination);
      const normalizedSourceOrigin = normalizeSite(sourceOrigin);
      const normalizedTriggerOrigin = normalizeSite(triggerOrigin);
      if (
        destination !== normalizedSourceOrigin &&
        destination !== normalizedTriggerOrigin
      ) {
        return fail(
          "destination-mismatch",
          `cross-site destination ${declaredDestination} matches neither site`,
        );
      }
    }
  }

  return {
    ok: true,
    sourceId,
    triggerId,
    sameSite,
    destination: leftDestination,
  };
}

/**
 * Validate a batch of records and retain only privacy-safe aggregates.
 */
export function filterAggregateRecords<T = unknown>(
  records: Iterable<T>,
): AggregateFilterResult<T> {
  const accepted: T[] = [];
  const rejected: RejectedAggregateRecord[] = [];
  let index = 0;

  for (const record of records) {
    const validation = validateAggregateRecord(record);
    if (validation.ok) {
      accepted.push(record);
    } else {
      rejected.push({
        index,
        record,
        reason: validation.reason,
        detail: validation.detail,
      });
    }
    index += 1;
  }

  return { accepted, rejected };
}

/** Convenience predicate used by callers that only need a boolean answer. */
export function isValidAggregateRecord(record: unknown): boolean {
  return validateAggregateRecord(record).ok;
}

export const filterRecords = filterAggregateRecords;
export const processAggregateRecords = filterAggregateRecords;
export const validateRecord = validateAggregateRecord;

export default filterAggregateRecords;
