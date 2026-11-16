export const SPARKPLUG_NAMESPACE = "spBv1.0";
export const DEFAULT_REBIRTH_LIMIT = 3;
export const DEFAULT_REBIRTH_WINDOW_MS = 60_000;

export type AclAction = "publish" | "subscribe";
export type PrincipalKind = "application" | "edge";

export type AclReason =
  | "allowed"
  | "invalid-request"
  | "unknown-principal"
  | "invalid-credential"
  | "client-id-mismatch"
  | "invalid-topic"
  | "topic-not-allowed"
  | "group-mismatch"
  | "node-mismatch"
  | "invalid-bdseq"
  | "birth-required"
  | "birth-without-bdseq"
  | "bdseq-mismatch"
  | "rebirth-rate-limited";

export interface ApplicationCredential {
  username?: string;
  password?: string;
  clientId?: string;
}

export interface EdgeNodeRegistration {
  clientId: string;
  groupId: string;
  username?: string;
  password?: string;
}

export interface EdgeNodeInput {
  clientId?: string;
  clientid?: string;
  clientIdentifier?: string;
  id?: string;
  nodeId?: string;
  groupId?: string;
  group?: string;
  username?: string;
  user?: string;
  password?: string;
}

export type ApplicationInput = string | ApplicationCredential;

export interface Principal {
  kind?: string;
  role?: string;
  type?: string;
  clientId?: string;
  clientid?: string;
  groupId?: string;
  group?: string;
  nodeId?: string;
  node?: string;
  authenticated?: boolean;
  valid?: boolean;
}

export interface AclRequest {
  action?: AclAction | string;
  operation?: AclAction | string;
  clientId?: string;
  clientid?: string;
  clientIdentifier?: string;
  username?: string;
  user?: string;
  password?: string;
  topic?: string;
  payload?: unknown;
  message?: unknown;
  principal?: Principal | PrincipalKind | string;
  role?: PrincipalKind | string;
  kind?: PrincipalKind | string;
  groupId?: string;
  group?: string;
  nodeId?: string;
  node?: string;
}

export interface RebirthLimitOptions {
  limit?: number;
  maxPerMinute?: number;
  windowMs?: number;
}

export interface SparkplugAclOptions {
  application?: ApplicationInput | ApplicationInput[];
  applications?: ApplicationInput | ApplicationInput[];
  app?: ApplicationInput | ApplicationInput[];
  mainApp?: ApplicationInput | ApplicationInput[];
  mainApplication?: ApplicationInput | ApplicationInput[];
  primaryApp?: ApplicationInput | ApplicationInput[];
  primaryApplication?: ApplicationInput | ApplicationInput[];
  applicationClientId?: string;
  applicationClientIds?: string[];
  primaryApplicationClientId?: string;
  primaryApplicationClientIds?: string[];
  edgeNodes?:
    | EdgeNodeInput
    | EdgeNodeInput[]
    | Record<string, EdgeNodeInput | string>;
  edges?:
    | EdgeNodeInput
    | EdgeNodeInput[]
    | Record<string, EdgeNodeInput | string>;
  nodes?:
    | EdgeNodeInput
    | EdgeNodeInput[]
    | Record<string, EdgeNodeInput | string>;
  rebirth?: RebirthLimitOptions;
  rebirthLimit?: number;
  maxRebirthPerMinute?: number;
  rebirthWindowMs?: number;
  now?: () => number;
  clock?: () => number;
}

export interface AclDecision {
  allow: boolean;
  allowed: boolean;
  ok: boolean;
  reason: AclReason;
  code: AclReason;
  detail?: string;
  retryAfterMs?: number;
}

interface ResolvedPrincipal {
  kind: PrincipalKind;
  clientId: string;
  groupId?: string;
}

interface BirthState {
  bdSeq?: string;
  at: number;
}

interface ParsedPublishTopic {
  groupId: string;
  messageType: string;
  nodeId: string;
  deviceId?: string;
}

interface DecodedMetric {
  name?: string;
  value?: unknown;
}

interface ProtobufField {
  number: number;
  wireType: number;
  data: bigint | Uint8Array;
}

type BdSeqResult =
  | { found: false }
  | { found: true; valid: false }
  | { found: true; valid: true; value: string };

const utf8Decoder = new TextDecoder();
const maxUint64 = 18_446_744_073_709_551_615n;

function allowedDecision(): AclDecision {
  return {
    allow: true,
    allowed: true,
    ok: true,
    reason: "allowed",
    code: "allowed",
  };
}

function deniedDecision(
  reason: AclReason,
  detail?: string,
  retryAfterMs?: number,
): AclDecision {
  const decision: AclDecision = {
    allow: false,
    allowed: false,
    ok: false,
    reason,
    code: reason,
  };
  if (detail !== undefined) {
    decision.detail = detail;
  }
  if (retryAfterMs !== undefined) {
    decision.retryAfterMs = retryAfterMs;
  }
  return decision;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function firstString(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value === "string" && value.length > 0) {
      return value;
    }
  }
  return undefined;
}

function firstTrimmedString(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value === "string") {
      const trimmed = value.trim();
      if (trimmed.length > 0) {
        return trimmed;
      }
    }
  }
  return undefined;
}

function hasOwn(value: Record<string, unknown>, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isConcreteTopicLevel(value: string): boolean {
  return (
    value.length > 0 &&
    !value.includes("\0") &&
    !value.includes("/") &&
    !value.includes("+") &&
    !value.includes("#")
  );
}

function isIdentity(value: string): boolean {
  return value.length > 0 && value.length <= 65_535 && !value.includes("\0");
}

function normalizePrincipalKind(value: unknown): PrincipalKind | undefined {
  if (typeof value !== "string") {
    return undefined;
  }
  switch (value.trim().toLowerCase().replace(/_/g, "-")) {
    case "application":
    case "app":
    case "primary":
    case "primary-app":
    case "primary-application":
      return "application";
    case "edge":
    case "edge-node":
    case "edgenode":
    case "node":
      return "edge";
    default:
      return undefined;
  }
}

function normalizeAction(value: unknown): AclAction | undefined {
  if (typeof value !== "string") {
    return undefined;
  }
  switch (value.trim().toLowerCase()) {
    case "publish":
    case "pub":
    case "write":
      return "publish";
    case "subscribe":
    case "sub":
    case "read":
      return "subscribe";
    default:
      return undefined;
  }
}

function normalizeApplication(input: unknown): ApplicationCredential | undefined {
  if (typeof input === "string") {
    const id = input.trim();
    return id.length > 0 ? { username: id, clientId: id } : undefined;
  }
  if (!isRecord(input)) {
    return undefined;
  }

  const username = firstTrimmedString(input.username, input.user);
  const clientId = firstTrimmedString(
    input.clientId,
    input.clientid,
    input.clientIdentifier,
  );
  const password =
    typeof input.password === "string" ? input.password : undefined;

  if (username === undefined && clientId === undefined) {
    return undefined;
  }
  return { username, clientId, password };
}

function normalizeEdge(
  input: unknown,
  fallbackClientId?: string,
): EdgeNodeRegistration | undefined {
  if (typeof input === "string") {
    if (fallbackClientId === undefined) {
      return undefined;
    }
    return { clientId: fallbackClientId, groupId: input };
  }
  if (!isRecord(input)) {
    return undefined;
  }

  const clientId = firstTrimmedString(
    input.clientId,
    input.clientid,
    input.clientIdentifier,
    input.id,
    input.nodeId,
    fallbackClientId,
  );
  const groupId = firstTrimmedString(input.groupId, input.group);
  if (clientId === undefined || groupId === undefined) {
    return undefined;
  }

  const registration: EdgeNodeRegistration = { clientId, groupId };
  const username = firstTrimmedString(input.username, input.user);
  if (username !== undefined) {
    registration.username = username;
  }
  if (typeof input.password === "string") {
    registration.password = input.password;
  }
  return registration;
}

function collectApplications(options: SparkplugAclOptions): ApplicationCredential[] {
  const result: ApplicationCredential[] = [];
  const rawInputs: unknown[] = [
    options.application,
    options.applications,
    options.app,
    options.mainApp,
    options.mainApplication,
    options.primaryApp,
    options.primaryApplication,
  ];

  for (const raw of rawInputs) {
    if (raw === undefined) {
      continue;
    }
    const entries = Array.isArray(raw) ? raw : [raw];
    for (const entry of entries) {
      const normalized = normalizeApplication(entry);
      if (normalized !== undefined) {
        result.push(normalized);
      }
    }
  }

  const clientIds: unknown[] = [
    options.applicationClientId,
    options.primaryApplicationClientId,
    ...(options.applicationClientIds ?? []),
    ...(options.primaryApplicationClientIds ?? []),
  ];
  for (const raw of clientIds) {
    if (typeof raw === "string" && raw.length > 0) {
      result.push({ clientId: raw });
    }
  }
  return result;
}

function collectEdgeNodes(options: SparkplugAclOptions): EdgeNodeRegistration[] {
  const result: EdgeNodeRegistration[] = [];
  const rawInputs: unknown[] = [options.edgeNodes, options.edges, options.nodes];

  for (const raw of rawInputs) {
    if (raw === undefined) {
      continue;
    }
    if (Array.isArray(raw)) {
      for (const entry of raw) {
        const normalized = normalizeEdge(entry);
        if (normalized !== undefined) {
          result.push(normalized);
        }
      }
      continue;
    }
    if (isRecord(raw)) {
      if (
        hasOwn(raw, "clientId") ||
        hasOwn(raw, "clientid") ||
        hasOwn(raw, "clientIdentifier") ||
        hasOwn(raw, "id") ||
        hasOwn(raw, "nodeId")
      ) {
        const normalized = normalizeEdge(raw);
        if (normalized !== undefined) {
          result.push(normalized);
        }
        continue;
      }
      for (const [clientId, entry] of Object.entries(raw)) {
        const normalized = normalizeEdge(entry, clientId);
        if (normalized !== undefined) {
          result.push(normalized);
        }
      }
    }
  }
  return result;
}

function positiveInteger(value: unknown, fallback: number): number {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) {
    return fallback;
  }
  return Math.max(1, Math.floor(value));
}

function nodeKey(groupId: string, clientId: string): string {
  return `${groupId}\u0000${clientId}`;
}

function parsePublishTopic(topic: unknown): ParsedPublishTopic | undefined {
  if (
    typeof topic !== "string" ||
    topic.length === 0 ||
    topic.length > 65_535 ||
    topic.includes("\0")
  ) {
    return undefined;
  }

  const levels = topic.split("/");
  if (levels.length !== 4 && levels.length !== 5) {
    return undefined;
  }
  if (levels[0] !== SPARKPLUG_NAMESPACE) {
    return undefined;
  }
  for (const level of levels) {
    if (!isConcreteTopicLevel(level)) {
      return undefined;
    }
  }

  return {
    groupId: levels[1],
    messageType: levels[2],
    nodeId: levels[3],
    deviceId: levels.length === 5 ? levels[4] : undefined,
  };
}

function isValidTopicFilter(topic: unknown): topic is string {
  if (
    typeof topic !== "string" ||
    topic.length === 0 ||
    topic.length > 65_535 ||
    topic.includes("\0")
  ) {
    return false;
  }

  const levels = topic.split("/");
  if (levels.some((level) => level.length === 0)) {
    return false;
  }
  for (let index = 0; index < levels.length; index += 1) {
    const level = levels[index];
    if (level === "#") {
      return index === levels.length - 1;
    }
    if (level === "+") {
      continue;
    }
    if (level.includes("+") || level.includes("#")) {
      return false;
    }
  }
  return true;
}

function asBytes(value: unknown): Uint8Array | undefined {
  if (value instanceof Uint8Array) {
    return value;
  }
  if (value instanceof ArrayBuffer) {
    return new Uint8Array(value);
  }
  if (ArrayBuffer.isView(value)) {
    return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
  }
  return undefined;
}

function readVarint(
  bytes: Uint8Array,
  offset: number,
): { value: bigint; offset: number } | undefined {
  let value = 0n;
  let shift = 0n;

  for (let index = 0; index < 10; index += 1) {
    if (offset >= bytes.length) {
      return undefined;
    }
    const byte = bytes[offset];
    offset += 1;
    value |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) {
      return { value, offset };
    }
    shift += 7n;
  }
  return undefined;
}

function readProtobufFields(bytes: Uint8Array): ProtobufField[] | undefined {
  const fields: ProtobufField[] = [];
  let offset = 0;

  while (offset < bytes.length) {
    const key = readVarint(bytes, offset);
    if (key === undefined) {
      return undefined;
    }
    offset = key.offset;

    const fieldNumber = Number(key.value >> 3n);
    const wireType = Number(key.value & 7n);
    if (fieldNumber <= 0) {
      return undefined;
    }

    if (wireType === 0) {
      const value = readVarint(bytes, offset);
      if (value === undefined) {
        return undefined;
      }
      fields.push({ number: fieldNumber, wireType, data: value.value });
      offset = value.offset;
      continue;
    }

    if (wireType === 1) {
      if (offset + 8 > bytes.length) {
        return undefined;
      }
      fields.push({
        number: fieldNumber,
        wireType,
        data: bytes.subarray(offset, offset + 8),
      });
      offset += 8;
      continue;
    }

    if (wireType === 2) {
      const length = readVarint(bytes, offset);
      if (
        length === undefined ||
        length.value > BigInt(bytes.length - length.offset)
      ) {
        return undefined;
      }
      offset = length.offset;
      const end = offset + Number(length.value);
      fields.push({
        number: fieldNumber,
        wireType,
        data: bytes.subarray(offset, end),
      });
      offset = end;
      continue;
    }

    if (wireType === 5) {
      if (offset + 4 > bytes.length) {
        return undefined;
      }
      fields.push({
        number: fieldNumber,
        wireType,
        data: bytes.subarray(offset, offset + 4),
      });
      offset += 4;
      continue;
    }

    return undefined;
  }
  return fields;
}

function decodeUtf8(bytes: Uint8Array): string {
  return utf8Decoder.decode(bytes);
}

function jsonValueFromBytes(bytes: Uint8Array): unknown | undefined {
  let offset = 0;
  while (offset < bytes.length) {
    const byte = bytes[offset];
    if (byte === 0x20 || byte === 0x09 || byte === 0x0a || byte === 0x0d) {
      offset += 1;
      continue;
    }
    break;
  }
  if (offset >= bytes.length) {
    return undefined;
  }
  if (bytes[offset] !== 0x7b && bytes[offset] !== 0x5b) {
    return undefined;
  }
  try {
    return JSON.parse(decodeUtf8(bytes));
  } catch {
    return undefined;
  }
}

function decodeProtobufMetrics(bytes: Uint8Array): DecodedMetric[] | undefined {
  const payloadFields = readProtobufFields(bytes);
  if (payloadFields === undefined) {
    return undefined;
  }

  const metrics: DecodedMetric[] = [];
  for (const field of payloadFields) {
    if (
      field.number !== 2 ||
      field.wireType !== 2 ||
      !(field.data instanceof Uint8Array)
    ) {
      continue;
    }
    const metricFields = readProtobufFields(field.data);
    if (metricFields === undefined) {
      return undefined;
    }

    let name: string | undefined;
    let value: unknown;
    for (const metricField of metricFields) {
      if (
        metricField.number === 1 &&
        metricField.wireType === 2 &&
        metricField.data instanceof Uint8Array
      ) {
        name = decodeUtf8(metricField.data);
      } else if (
        (metricField.number === 9 || metricField.number === 10) &&
        metricField.wireType === 0
      ) {
        value = metricField.data;
      } else if (
        metricField.number === 13 &&
        metricField.wireType === 0
      ) {
        value = metricField.data !== 0n;
      } else if (
        metricField.number === 14 &&
        metricField.wireType === 2 &&
        metricField.data instanceof Uint8Array
      ) {
        value = decodeUtf8(metricField.data);
      }
    }
    metrics.push({ name, value });
  }
  return metrics;
}

function metricValue(metric: Record<string, unknown>): unknown {
  if (metric.isNull === true || metric.is_null === true) {
    return undefined;
  }

  const candidates: string[] = [
    "value",
    "long_value",
    "longValue",
    "int_value",
    "intValue",
    "uint_value",
    "uintValue",
    "boolean_value",
    "booleanValue",
    "string_value",
    "stringValue",
  ];
  for (const key of candidates) {
    if (hasOwn(metric, key)) {
      return metric[key];
    }
  }
  return undefined;
}

function metricsFromValue(value: unknown): DecodedMetric[] | undefined {
  if (Array.isArray(value)) {
    const metrics: DecodedMetric[] = [];
    for (const entry of value) {
      if (!isRecord(entry)) {
        return undefined;
      }
      const name = firstString(
        entry.name,
        entry.metricName,
        entry.metric_name,
      );
      metrics.push({ name, value: metricValue(entry) });
    }
    return metrics;
  }
  if (!isRecord(value)) {
    return undefined;
  }
  if (Array.isArray(value.metrics)) {
    return metricsFromValue(value.metrics);
  }
  if (typeof value.name === "string") {
    return [{ name: value.name, value: metricValue(value) }];
  }
  return undefined;
}

function normalizeUnsignedInteger(
  value: unknown,
  depth = 0,
): string | undefined {
  if (depth > 3) {
    return undefined;
  }
  if (typeof value === "bigint") {
    return value >= 0n && value <= maxUint64 ? value.toString() : undefined;
  }
  if (typeof value === "number") {
    return Number.isSafeInteger(value) && value >= 0 ? String(value) : undefined;
  }
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (!/^[0-9]+$/.test(trimmed) || trimmed.length > 20) {
      return undefined;
    }
    const parsed = BigInt(trimmed);
    return parsed <= maxUint64 ? parsed.toString() : undefined;
  }
  if (!isRecord(value)) {
    return undefined;
  }

  if (hasOwn(value, "low") && hasOwn(value, "high")) {
    const low = value.low;
    const high = value.high;
    if (Number.isInteger(low) && Number.isInteger(high)) {
      const lowPart = BigInt(Number(low) >>> 0);
      const highPart = BigInt(Number(high) >>> 0);
      const combined = (highPart << 32n) | lowPart;
      if (value.unsigned === false && (Number(high) & 0x80000000) !== 0) {
        return undefined;
      }
      return combined.toString();
    }
  }

  if (hasOwn(value, "value")) {
    return normalizeUnsignedInteger(value.value, depth + 1);
  }
  if (typeof value.toString === "function") {
    const rendered = value.toString();
    if (rendered !== "[object Object]") {
      return normalizeUnsignedInteger(rendered, depth + 1);
    }
  }
  return undefined;
}

function readBdSeqFromMetrics(metrics: DecodedMetric[] | undefined): BdSeqResult {
  if (metrics === undefined) {
    return { found: false };
  }

  let found = false;
  let invalid = false;
  const values = new Set<string>();
  for (const metric of metrics) {
    if (typeof metric.name !== "string") {
      continue;
    }
    if (metric.name.trim().toLowerCase() !== "bdseq") {
      continue;
    }
    found = true;
    const normalized = normalizeUnsignedInteger(metric.value);
    if (normalized === undefined) {
      invalid = true;
    } else {
      values.add(normalized);
    }
  }

  if (!found) {
    return { found: false };
  }
  if (invalid || values.size !== 1) {
    return { found: true, valid: false };
  }
  return { found: true, valid: true, value: [...values][0] };
}

function combineBdSeqResults(results: BdSeqResult[]): BdSeqResult {
  let found = false;
  let invalid = false;
  const values = new Set<string>();

  for (const result of results) {
    if (!result.found) {
      continue;
    }
    found = true;
    if (!result.valid) {
      invalid = true;
    } else {
      values.add(result.value);
    }
  }

  if (!found) {
    return { found: false };
  }
  if (invalid || values.size !== 1) {
    return { found: true, valid: false };
  }
  return { found: true, valid: true, value: [...values][0] };
}

function readBdSeq(payload: unknown): BdSeqResult {
  if (payload === undefined || payload === null) {
    return { found: false };
  }

  const bytes = asBytes(payload);
  if (bytes !== undefined) {
    const protobufResult = readBdSeqFromMetrics(
      decodeProtobufMetrics(bytes),
    );
    if (protobufResult.found) {
      return protobufResult;
    }
    const jsonValue = jsonValueFromBytes(bytes);
    return jsonValue === undefined
      ? protobufResult
      : readBdSeq(jsonValue);
  }

  if (typeof payload === "string") {
    const trimmed = payload.trim();
    if (trimmed.length === 0) {
      return { found: false };
    }
    if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
      try {
        return readBdSeq(JSON.parse(trimmed));
      } catch {
        return { found: false };
      }
    }
    const normalized = normalizeUnsignedInteger(trimmed);
    return normalized === undefined
      ? { found: false }
      : { found: true, valid: true, value: normalized };
  }

  if (typeof payload === "number" || typeof payload === "bigint") {
    const normalized = normalizeUnsignedInteger(payload);
    return normalized === undefined
      ? { found: true, valid: false }
      : { found: true, valid: true, value: normalized };
  }

  if (!isRecord(payload)) {
    return { found: false };
  }

  const results: BdSeqResult[] = [];
  const directKeys = ["bdSeq", "bdseq", "birthDeathSequence"];
  const directValues = new Set<string>();
  let directFound = false;
  let directInvalid = false;
  for (const key of directKeys) {
    if (!hasOwn(payload, key)) {
      continue;
    }
    directFound = true;
    const normalized = normalizeUnsignedInteger(payload[key]);
    if (normalized === undefined) {
      directInvalid = true;
    } else {
      directValues.add(normalized);
    }
  }
  if (directFound) {
    results.push(
      directInvalid || directValues.size !== 1
        ? { found: true, valid: false }
        : { found: true, valid: true, value: [...directValues][0] },
    );
  }

  if (Array.isArray(payload.metrics)) {
    results.push(readBdSeqFromMetrics(metricsFromValue(payload.metrics)));
  }
  if (typeof payload.name === "string") {
    if (payload.name.trim().toLowerCase() === "bdseq") {
      const normalized = normalizeUnsignedInteger(metricValue(payload));
      results.push(
        normalized === undefined
          ? { found: true, valid: false }
          : { found: true, valid: true, value: normalized },
      );
    }
  }
  return combineBdSeqResults(results);
}

function isRebirthMetricName(value: unknown): boolean {
  return (
    typeof value === "string" &&
    value.trim().toLowerCase() === "node control/rebirth"
  );
}

function isRebirthCommandName(value: unknown): boolean {
  if (typeof value !== "string") {
    return false;
  }
  const normalized = value.trim().toLowerCase();
  return normalized === "rebirth" || normalized === "node control/rebirth";
}

function isTruthyCommandValue(value: unknown, depth = 0): boolean {
  if (depth > 3) {
    return false;
  }
  if (value === true || value === 1 || value === 1n) {
    return true;
  }
  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    return normalized === "true" || normalized === "1";
  }
  if (isRecord(value)) {
    if (hasOwn(value, "value")) {
      return isTruthyCommandValue(value.value, depth + 1);
    }
    if (hasOwn(value, "boolean_value") || hasOwn(value, "booleanValue")) {
      return isTruthyCommandValue(
        value.boolean_value ?? value.booleanValue,
        depth + 1,
      );
    }
  }
  return false;
}

function isRebirthMetric(metric: DecodedMetric): boolean {
  return (
    isRebirthMetricName(metric.name) &&
    isTruthyCommandValue(metric.value)
  );
}

function isRebirthPayload(payload: unknown): boolean {
  if (payload === undefined || payload === null) {
    return false;
  }

  const bytes = asBytes(payload);
  if (bytes !== undefined) {
    const metrics = decodeProtobufMetrics(bytes);
    if (metrics !== undefined && metrics.some(isRebirthMetric)) {
      return true;
    }
    const jsonValue = jsonValueFromBytes(bytes);
    return jsonValue === undefined
      ? false
      : isRebirthPayload(jsonValue);
  }

  if (typeof payload === "string") {
    const trimmed = payload.trim();
    if (trimmed.length === 0) {
      return false;
    }
    if (
      trimmed.toLowerCase() === "rebirth" ||
      isRebirthMetricName(trimmed)
    ) {
      return true;
    }
    if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
      try {
        return isRebirthPayload(JSON.parse(trimmed));
      } catch {
        return false;
      }
    }
    return false;
  }

  if (Array.isArray(payload)) {
    const metrics = metricsFromValue(payload);
    return metrics !== undefined && metrics.some(isRebirthMetric);
  }
  if (!isRecord(payload)) {
    return false;
  }

  if (
    isTruthyCommandValue(payload.rebirth) ||
    isTruthyCommandValue(payload.isRebirth)
  ) {
    return true;
  }
  if (
    isRebirthCommandName(payload.command) ||
    isRebirthCommandName(payload.action) ||
    isRebirthCommandName(payload.metric)
  ) {
    return true;
  }
  if (isRecord(payload.command)) {
    const commandMetrics = metricsFromValue(payload.command);
    if (
      commandMetrics !== undefined &&
      commandMetrics.some(isRebirthMetric)
    ) {
      return true;
    }
  }
  if (Array.isArray(payload.metrics)) {
    const metrics = metricsFromValue(payload.metrics);
    if (metrics !== undefined && metrics.some(isRebirthMetric)) {
      return true;
    }
  }
  if (
    typeof payload.name === "string" &&
    isRebirthMetricName(payload.name) &&
    isTruthyCommandValue(metricValue(payload))
  ) {
    return true;
  }
  return false;
}

export class SparkplugAcl {
  private readonly applications: ApplicationCredential[];
  private readonly edges = new Map<string, EdgeNodeRegistration>();
  private readonly edgeUsernames = new Map<string, EdgeNodeRegistration>();
  private readonly births = new Map<string, BirthState>();
  private readonly rebirthHistory = new Map<string, number[]>();
  private readonly rebirthLimit: number;
  private readonly rebirthWindowMs: number;
  private readonly clock: () => number;

  constructor(options: SparkplugAclOptions = {}) {
    this.applications = collectApplications(options);

    for (const registration of collectEdgeNodes(options)) {
      if (!isIdentity(registration.clientId)) {
        continue;
      }
      this.edges.set(registration.clientId, registration);
      if (registration.username !== undefined) {
        this.edgeUsernames.set(registration.username, registration);
      }
    }

    this.rebirthLimit = positiveInteger(
      options.rebirth?.limit ??
        options.rebirth?.maxPerMinute ??
        options.rebirthLimit ??
        options.maxRebirthPerMinute,
      DEFAULT_REBIRTH_LIMIT,
    );
    this.rebirthWindowMs = positiveInteger(
      options.rebirth?.windowMs ?? options.rebirthWindowMs,
      DEFAULT_REBIRTH_WINDOW_MS,
    );
    this.clock = options.now ?? options.clock ?? (() => Date.now());
  }

  authorize(request: AclRequest): AclDecision {
    if (!isRecord(request)) {
      return deniedDecision("invalid-request", "request must be an object");
    }

    const rawAction = request.action ?? request.operation;
    const action =
      normalizeAction(rawAction) ??
      (rawAction === undefined ? "publish" : undefined);
    if (action === undefined) {
      return deniedDecision("invalid-request", "unknown ACL action");
    }

    if (typeof request.topic !== "string" || request.topic.length === 0) {
      return deniedDecision("invalid-topic");
    }

    const resolution = this.resolvePrincipal(request);
    if (!resolution.ok) {
      return resolution.decision;
    }

    const payload =
      request.payload !== undefined ? request.payload : request.message;
    if (action === "publish") {
      return this.decidePublish(
        resolution.principal,
        request.topic,
        payload,
      );
    }
    return this.decideSubscribe(resolution.principal, request.topic);
  }

  check(request: AclRequest): AclDecision {
    return this.authorize(request);
  }

  decide(request: AclRequest): AclDecision {
    return this.authorize(request);
  }

  evaluate(request: AclRequest): AclDecision {
    return this.authorize(request);
  }

  isAllowed(request: AclRequest): boolean {
    return this.authorize(request).allow;
  }

  authorizePublish(
    requestOrClientId: AclRequest | string,
    topic?: string,
    payload?: unknown,
  ): AclDecision {
    return this.authorize(
      this.withAction("publish", requestOrClientId, topic, payload),
    );
  }

  checkPublish(
    requestOrClientId: AclRequest | string,
    topic?: string,
    payload?: unknown,
  ): AclDecision {
    return this.authorizePublish(requestOrClientId, topic, payload);
  }

  publish(
    requestOrClientId: AclRequest | string,
    topic?: string,
    payload?: unknown,
  ): AclDecision {
    return this.authorizePublish(requestOrClientId, topic, payload);
  }

  canPublish(
    requestOrClientId: AclRequest | string,
    topic?: string,
    payload?: unknown,
  ): boolean {
    return this.authorizePublish(requestOrClientId, topic, payload).allow;
  }

  allowPublish(
    requestOrClientId: AclRequest | string,
    topic?: string,
    payload?: unknown,
  ): boolean {
    return this.canPublish(requestOrClientId, topic, payload);
  }

  authorizeSubscribe(
    requestOrClientId: AclRequest | string,
    topic?: string,
    payload?: unknown,
  ): AclDecision {
    return this.authorize(
      this.withAction("subscribe", requestOrClientId, topic, payload),
    );
  }

  checkSubscribe(
    requestOrClientId: AclRequest | string,
    topic?: string,
    payload?: unknown,
  ): AclDecision {
    return this.authorizeSubscribe(requestOrClientId, topic, payload);
  }

  subscribe(
    requestOrClientId: AclRequest | string,
    topic?: string,
    payload?: unknown,
  ): AclDecision {
    return this.authorizeSubscribe(requestOrClientId, topic, payload);
  }

  canSubscribe(
    requestOrClientId: AclRequest | string,
    topic?: string,
    payload?: unknown,
  ): boolean {
    return this.authorizeSubscribe(requestOrClientId, topic, payload).allow;
  }

  allowSubscribe(
    requestOrClientId: AclRequest | string,
    topic?: string,
    payload?: unknown,
  ): boolean {
    return this.canSubscribe(requestOrClientId, topic, payload);
  }

  lastBdSeq(groupId: string, clientId: string): string | undefined {
    return this.births.get(nodeKey(groupId, clientId))?.bdSeq;
  }

  getLastBdSeq(groupId: string, clientId: string): string | undefined {
    return this.lastBdSeq(groupId, clientId);
  }

  hasBirth(groupId: string, clientId: string): boolean {
    return this.births.has(nodeKey(groupId, clientId));
  }

  resetRateLimit(groupId?: string, clientId?: string): void {
    if (groupId !== undefined && clientId !== undefined) {
      this.rebirthHistory.delete(nodeKey(groupId, clientId));
      return;
    }
    this.rebirthHistory.clear();
  }

  clearRebirthHistory(groupId?: string, clientId?: string): void {
    this.resetRateLimit(groupId, clientId);
  }

  reset(): void {
    this.births.clear();
    this.rebirthHistory.clear();
  }

  clear(): void {
    this.reset();
  }

  private withAction(
    action: AclAction,
    requestOrClientId: AclRequest | string,
    topic?: string,
    payload?: unknown,
  ): AclRequest {
    if (typeof requestOrClientId === "string") {
      return {
        action,
        clientId: requestOrClientId,
        topic: topic ?? "",
        payload,
      };
    }
    const request: AclRequest = { ...requestOrClientId, action };
    if (topic !== undefined) {
      request.topic = topic;
    }
    if (payload !== undefined) {
      request.payload = payload;
    }
    return request;
  }

  private resolvePrincipal(
    request: AclRequest,
  ):
    | { ok: true; principal: ResolvedPrincipal }
    | { ok: false; decision: AclDecision } {
    const requestClientId = firstString(
      request.clientId,
      request.clientid,
      request.clientIdentifier,
    );
    if (requestClientId !== undefined && !isIdentity(requestClientId)) {
      return {
        ok: false,
        decision: deniedDecision("invalid-request", "invalid client id"),
      };
    }

    const username = firstString(request.username, request.user);
    const password =
      typeof request.password === "string" ? request.password : undefined;

    const rawPrincipal = request.principal ?? request.role ?? request.kind;
    const principalObject = isRecord(rawPrincipal)
      ? rawPrincipal
      : undefined;

    if (
      principalObject?.authenticated === false ||
      principalObject?.valid === false
    ) {
      return {
        ok: false,
        decision: deniedDecision("unknown-principal", "unauthenticated principal"),
      };
    }

    let rawKind: unknown = principalObject
      ? principalObject.kind ?? principalObject.role ?? principalObject.type
      : rawPrincipal;
    if (rawKind === undefined && request.role !== undefined) {
      rawKind = request.role;
    }
    if (rawKind === undefined && request.kind !== undefined) {
      rawKind = request.kind;
    }

    const explicitKind = normalizePrincipalKind(rawKind);
    if (explicitKind === "application") {
      return this.resolveApplicationPrincipal(request, principalObject);
    }
    if (explicitKind === "edge") {
      return this.resolveEdgePrincipal(
        request,
        principalObject,
        requestClientId,
      );
    }

    if (requestClientId === undefined) {
      return {
        ok: false,
        decision: deniedDecision("invalid-request", "missing client id"),
      };
    }

    const application = this.matchApplication(
      requestClientId,
      username,
      password,
    );
    if (application === "match") {
      return {
        ok: true,
        principal: { kind: "application", clientId: requestClientId },
      };
    }
    if (application === "invalid-credential") {
      return {
        ok: false,
        decision: deniedDecision(
          "invalid-credential",
          "application credentials rejected",
        ),
      };
    }

    const edge = this.matchEdge(
      requestClientId,
      username,
      password,
    );
    if (edge === "match") {
      const registration = this.edges.get(requestClientId);
      if (registration === undefined) {
        return {
          ok: false,
          decision: deniedDecision("unknown-principal"),
        };
      }
      return {
        ok: true,
        principal: {
          kind: "edge",
          clientId: registration.clientId,
          groupId: registration.groupId,
        },
      };
    }
    if (edge === "client-id-mismatch") {
      return {
        ok: false,
        decision: deniedDecision(
          "client-id-mismatch",
          "edge credential is bound to a different client id",
        ),
      };
    }
    if (edge === "invalid-credential") {
      return {
        ok: false,
        decision: deniedDecision(
          "invalid-credential",
          "edge credentials rejected",
        ),
      };
    }

    return {
      ok: false,
      decision: deniedDecision("unknown-principal"),
    };
  }

  private resolveApplicationPrincipal(
    request: AclRequest,
    principal: Record<string, unknown> | undefined,
  ):
    | { ok: true; principal: ResolvedPrincipal }
    | { ok: false; decision: AclDecision } {
    const requestClientId = firstString(
      request.clientId,
      request.clientid,
      request.clientIdentifier,
    );
    const principalClientId = firstString(
      principal?.clientId,
      principal?.clientid,
    );
    if (
      principalClientId !== undefined &&
      requestClientId !== undefined &&
      principalClientId !== requestClientId
    ) {
      return {
        ok: false,
        decision: deniedDecision("client-id-mismatch"),
      };
    }

    const clientId = principalClientId ?? requestClientId ?? "application";
    return {
      ok: true,
      principal: { kind: "application", clientId },
    };
  }

  private resolveEdgePrincipal(
    request: AclRequest,
    principal: Record<string, unknown> | undefined,
    requestClientId: string | undefined,
  ):
    | { ok: true; principal: ResolvedPrincipal }
    | { ok: false; decision: AclDecision } {
    const principalClientId = firstString(
      principal?.clientId,
      principal?.clientid,
    );
    if (
      principalClientId !== undefined &&
      requestClientId !== undefined &&
      principalClientId !== requestClientId
    ) {
      return {
        ok: false,
        decision: deniedDecision("client-id-mismatch"),
      };
    }

    const clientId = principalClientId ?? requestClientId;
    if (clientId === undefined || !isIdentity(clientId)) {
      return {
        ok: false,
        decision: deniedDecision("invalid-request", "missing edge client id"),
      };
    }

    const principalNodeId = firstString(principal?.nodeId, principal?.node);
    if (principalNodeId !== undefined && principalNodeId !== clientId) {
      return {
        ok: false,
        decision: deniedDecision("node-mismatch"),
      };
    }

    const registration = this.edges.get(clientId);
    const principalGroupId = firstString(principal?.groupId, principal?.group);
    const requestGroupId = firstString(request.groupId, request.group);
    if (
      principalGroupId !== undefined &&
      requestGroupId !== undefined &&
      principalGroupId !== requestGroupId
    ) {
      return {
        ok: false,
        decision: deniedDecision("group-mismatch"),
      };
    }

    const groupId =
      principalGroupId ?? registration?.groupId ?? requestGroupId;
    if (groupId === undefined || !isConcreteTopicLevel(groupId)) {
      return {
        ok: false,
        decision: deniedDecision("invalid-request", "missing edge group id"),
      };
    }
    if (registration !== undefined && registration.groupId !== groupId) {
      return {
        ok: false,
        decision: deniedDecision("group-mismatch"),
      };
    }

    return {
      ok: true,
      principal: { kind: "edge", clientId, groupId },
    };
  }

  private matchApplication(
    clientId: string,
    username: string | undefined,
    password: string | undefined,
  ): "match" | "invalid-credential" | "none" {
    const usernameCandidates =
      username === undefined
        ? []
        : this.applications.filter(
            (application) => application.username === username,
          );

    if (usernameCandidates.length > 0) {
      for (const application of usernameCandidates) {
        if (
          application.password !== undefined &&
          application.password !== password
        ) {
          continue;
        }
        if (
          application.clientId !== undefined &&
          application.clientId !== clientId
        ) {
          continue;
        }
        return "match";
      }
      return "invalid-credential";
    }

    for (const application of this.applications) {
      if (application.clientId !== clientId) {
        continue;
      }
      if (
        username !== undefined &&
        application.username !== undefined &&
        application.username !== username
      ) {
        continue;
      }
      if (
        application.password !== undefined &&
        application.password !== password
      ) {
        continue;
      }
      return "match";
    }
    return "none";
  }

  private matchEdge(
    clientId: string,
    username: string | undefined,
    password: string | undefined,
  ): "match" | "client-id-mismatch" | "invalid-credential" | "none" {
    if (username !== undefined) {
      const usernameRegistration = this.edgeUsernames.get(username);
      if (
        usernameRegistration !== undefined &&
        usernameRegistration.clientId !== clientId
      ) {
        return "client-id-mismatch";
      }
    }

    const registration = this.edges.get(clientId);
    if (registration === undefined) {
      return "none";
    }
    if (
      registration.username !== undefined &&
      registration.username !== username
    ) {
      return "invalid-credential";
    }
    if (
      registration.password !== undefined &&
      registration.password !== password
    ) {
      return "invalid-credential";
    }
    return "match";
  }

  private decidePublish(
    principal: ResolvedPrincipal,
    topic: string,
    payload: unknown,
  ): AclDecision {
    const parsed = parsePublishTopic(topic);
    if (parsed === undefined) {
      return deniedDecision("invalid-topic");
    }

    if (principal.kind === "application") {
      if (
        parsed.messageType !== "NCMD" &&
        parsed.messageType !== "DCMD"
      ) {
        return deniedDecision(
          "topic-not-allowed",
          "applications may only publish NCMD and DCMD",
        );
      }
      if (parsed.messageType === "NCMD" && parsed.deviceId !== undefined) {
        return deniedDecision("invalid-topic", "NCMD has no device level");
      }
      if (parsed.messageType === "DCMD" && parsed.deviceId === undefined) {
        return deniedDecision("invalid-topic", "DCMD requires a device id");
      }

      if (isRebirthPayload(payload)) {
        const limit = this.consumeRebirth(
          parsed.groupId,
          parsed.nodeId,
        );
        if (!limit.allowed) {
          return deniedDecision(
            "rebirth-rate-limited",
            "rebirth limit exceeded for this edge node",
            limit.retryAfterMs,
          );
        }
      }
      return allowedDecision();
    }

    if (
      parsed.messageType !== "NBIRTH" &&
      parsed.messageType !== "NDATA" &&
      parsed.messageType !== "NDEATH"
    ) {
      return deniedDecision(
        "topic-not-allowed",
        "edge nodes may only publish NBIRTH, NDATA and NDEATH",
      );
    }
    if (parsed.deviceId !== undefined) {
      return deniedDecision("invalid-topic", "node message has no device level");
    }
    if (parsed.groupId !== principal.groupId) {
      return deniedDecision("group-mismatch");
    }
    if (parsed.nodeId !== principal.clientId) {
      return deniedDecision("node-mismatch");
    }

    const now = this.currentTime();
    const key = nodeKey(parsed.groupId, parsed.nodeId);
    if (parsed.messageType === "NBIRTH") {
      const bdSeq = readBdSeq(payload);
      if (bdSeq.found && !bdSeq.valid) {
        return deniedDecision("invalid-bdseq", "NBIRTH bdSeq is invalid");
      }
      this.births.set(key, {
        bdSeq: bdSeq.found ? bdSeq.value : undefined,
        at: now,
      });
      return allowedDecision();
    }

    if (parsed.messageType === "NDEATH") {
      const birth = this.births.get(key);
      if (birth === undefined) {
        return deniedDecision(
          "birth-required",
          "NDEATH requires a prior NBIRTH from the same node",
        );
      }
      const bdSeq = readBdSeq(payload);
      if (!bdSeq.found || !bdSeq.valid) {
        return deniedDecision("invalid-bdseq", "NDEATH bdSeq is invalid");
      }
      if (birth.bdSeq === undefined) {
        return deniedDecision(
          "birth-without-bdseq",
          "latest NBIRTH did not contain a bdSeq",
        );
      }
      if (birth.bdSeq !== bdSeq.value) {
        return deniedDecision(
          "bdseq-mismatch",
          "NDEATH bdSeq does not match the latest NBIRTH",
        );
      }
    }

    return allowedDecision();
  }

  private decideSubscribe(
    principal: ResolvedPrincipal,
    topic: string,
  ): AclDecision {
    if (!isValidTopicFilter(topic)) {
      return deniedDecision("invalid-topic");
    }
    const levels = topic.split("/");

    if (principal.kind === "application") {
      if (
        levels.length < 2 ||
        levels[0] !== SPARKPLUG_NAMESPACE
      ) {
        return deniedDecision(
          "topic-not-allowed",
          "application subscriptions must stay in spBv1.0",
        );
      }
      return allowedDecision();
    }

    if (
      levels.length < 4 ||
      levels[0] !== SPARKPLUG_NAMESPACE ||
      levels[1] !== principal.groupId ||
      levels[3] !== principal.clientId
    ) {
      return deniedDecision(
        "topic-not-allowed",
        "edge subscriptions must target the node's own command topics",
      );
    }

    if (levels[2] === "NCMD") {
      if (levels.length !== 4) {
        return deniedDecision("invalid-topic");
      }
      return allowedDecision();
    }

    if (levels[2] === "DCMD") {
      if (levels.length !== 5) {
        return deniedDecision("invalid-topic");
      }
      const deviceLevel = levels[4];
      if (deviceLevel === "+" || deviceLevel === "#") {
        return allowedDecision();
      }
      return isConcreteTopicLevel(deviceLevel)
        ? allowedDecision()
        : deniedDecision("invalid-topic");
    }

    return deniedDecision(
      "topic-not-allowed",
      "edge nodes may only subscribe to their own NCMD and DCMD topics",
    );
  }

  private consumeRebirth(
    groupId: string,
    nodeId: string,
  ): { allowed: boolean; retryAfterMs?: number } {
    const now = this.currentTime();
    const key = nodeKey(groupId, nodeId);
    const history = this.rebirthHistory.get(key) ?? [];
    const cutoff = now - this.rebirthWindowMs;

    let expired = 0;
    while (expired < history.length && history[expired] <= cutoff) {
      expired += 1;
    }
    if (expired > 0) {
      history.splice(0, expired);
    }

    if (history.length >= this.rebirthLimit) {
      this.rebirthHistory.set(key, history);
      const retryAfterMs = Math.max(
        1,
        Math.ceil(history[0] + this.rebirthWindowMs - now),
      );
      return { allowed: false, retryAfterMs };
    }

    history.push(now);
    this.rebirthHistory.set(key, history);
    return { allowed: true };
  }

  private currentTime(): number {
    const value = Number(this.clock());
    return Number.isFinite(value) ? value : Date.now();
  }
}

export function createSparkplugAcl(
  options: SparkplugAclOptions = {},
): SparkplugAcl {
  return new SparkplugAcl(options);
}

export const createAcl = createSparkplugAcl;

export {
  SparkplugAcl as AclEngine,
  SparkplugAcl as SparkplugAclEngine,
  SparkplugAcl as Acl,
};

export default SparkplugAcl;
