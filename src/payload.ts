const UINT64_MAX = (1n << 64n) - 1n;
const MAX_PAYLOAD_BYTES = 1024 * 1024;
const BD_SEQ_METRIC = "bdSeq";
const REBIRTH_METRIC = "Node Control/Rebirth";

interface PayloadMetric {
  name?: string;
  intValue?: bigint;
  longValue?: bigint;
  booleanValue?: boolean;
  stringValue?: string;
}

interface ParsedPayload {
  metrics: PayloadMetric[];
  directBdSeq?: bigint;
}

export type BdSeqExtraction =
  | { ok: true; bdSeq: bigint }
  | {
      ok: false;
      reason:
        | "BIRTH_SEQUENCE_MISSING"
        | "BIRTH_SEQUENCE_INVALID"
        | "UNPARSEABLE_PAYLOAD";
    };

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function toBigInt(value: unknown): bigint | undefined {
  if (typeof value === "bigint") {
    return value >= 0n && value <= UINT64_MAX ? value : undefined;
  }

  if (typeof value === "number") {
    if (!Number.isInteger(value) || value < 0) return undefined;
    // Reject values that cannot be represented exactly as a JS number.
    if (!Number.isSafeInteger(value)) return undefined;
    return BigInt(value);
  }

  if (typeof value === "string") {
    const trimmed = value.trim();
    if (/^[0-9]+$/.test(trimmed)) {
      try {
        const parsed = BigInt(trimmed);
        return parsed >= 0n && parsed <= UINT64_MAX ? parsed : undefined;
      } catch {
        return undefined;
      }
    }
    if (/^0x[0-9a-f]+$/i.test(trimmed)) {
      try {
        const parsed = BigInt(trimmed);
        return parsed >= 0n && parsed <= UINT64_MAX ? parsed : undefined;
      } catch {
        return undefined;
      }
    }
    return undefined;
  }

  // protobuf.js represents uint64 as { low, high, unsigned } by default.
  if (isRecord(value) && typeof value.low === "number" && typeof value.high === "number") {
    const low = BigInt(value.low >>> 0);
    const high = BigInt(value.high >>> 0);
    const combined = (high << 32n) | low;
    return combined >= 0n && combined <= UINT64_MAX ? combined : undefined;
  }

  return undefined;
}

function toBoolean(value: unknown): boolean | undefined {
  if (typeof value === "boolean") return value;
  if (value === 1 || value === "1" || value === "true") return true;
  if (value === 0 || value === "0" || value === "false") return false;
  return undefined;
}

function metricFromJson(value: Record<string, unknown>): PayloadMetric {
  const metric: PayloadMetric = {};
  if (typeof value.name === "string") metric.name = value.name;

  const declaredType = value.type ?? value.dataType ?? value.datatype;
  const rawValue = value.value;
  const intValue =
    value.intValue ?? value.int_value ?? (declaredType === "Int32" ? rawValue : undefined);
  const longValue =
    value.longValue ?? value.long_value ?? (declaredType === "Int64" ? rawValue : undefined);
  const boolValue =
    value.booleanValue ?? value.boolean_value ?? (declaredType === "Boolean" ? rawValue : undefined);
  const stringValue =
    value.stringValue ?? value.string_value ?? (declaredType === "String" ? rawValue : undefined);

  const int = toBigInt(intValue);
  const long = toBigInt(longValue);
  if (long !== undefined) metric.longValue = long;
  if (int !== undefined) metric.intValue = int;

  // The Tahu/Sparkplug JSON representation generally puts the value in
  // `value`; infer its type from the metric's declared datatype when needed.
  if (metric.longValue === undefined && metric.intValue === undefined) {
    if (declaredType === "Boolean" || typeof rawValue === "boolean") {
      const bool = toBoolean(rawValue);
      if (bool !== undefined) metric.booleanValue = bool;
    } else if (declaredType === "String" && typeof rawValue === "string") {
      metric.stringValue = rawValue;
    } else {
      const numeric = toBigInt(rawValue);
      if (numeric !== undefined) metric.longValue = numeric;
    }
  }

  const boolean = toBoolean(boolValue);
  if (boolean !== undefined) metric.booleanValue = boolean;
  if (typeof stringValue === "string") metric.stringValue = stringValue;

  return metric;
}

function payloadFromJsonObject(value: Record<string, unknown>): ParsedPayload {
  const payload: ParsedPayload = { metrics: [] };
  const direct = value.bdSeq ?? value.bd_seq;
  if (direct !== undefined) {
    const parsed = toBigInt(direct);
    if (parsed !== undefined) payload.directBdSeq = parsed;
  }

  const rawMetrics = value.metrics;
  if (Array.isArray(rawMetrics)) {
    for (const item of rawMetrics) {
      if (isRecord(item)) payload.metrics.push(metricFromJson(item));
    }
  } else if (isRecord(rawMetrics)) {
    payload.metrics.push(metricFromJson(rawMetrics));
  }

  return payload;
}

function decodeUtf8(bytes: Uint8Array): string {
  return new TextDecoder().decode(bytes);
}

function readVarint(
  bytes: Uint8Array,
  start: number,
): { value: bigint; next: number } | null {
  let value = 0n;
  let shift = 0n;
  let offset = start;
  while (offset < bytes.length && shift <= 63n) {
    const byte = bytes[offset];
    offset += 1;
    value |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) return { value, next: offset };
    shift += 7n;
  }
  return null;
}

function readDelimited(
  bytes: Uint8Array,
  start: number,
): { data: Uint8Array; next: number } | null {
  const length = readVarint(bytes, start);
  if (length === null) return null;
  const byteLength = Number(length.value);
  if (!Number.isSafeInteger(byteLength) || byteLength < 0) return null;
  const end = length.next + byteLength;
  if (end > bytes.length) return null;
  return { data: bytes.subarray(length.next, end), next: end };
}

function skipField(
  bytes: Uint8Array,
  offset: number,
  wireType: number,
): number | null {
  if (wireType === 0) {
    const result = readVarint(bytes, offset);
    return result === null ? null : result.next;
  }
  if (wireType === 1) {
    return offset + 8 <= bytes.length ? offset + 8 : null;
  }
  if (wireType === 2) {
    const result = readDelimited(bytes, offset);
    return result === null ? null : result.next;
  }
  if (wireType === 5) {
    return offset + 4 <= bytes.length ? offset + 4 : null;
  }
  // Groups (3/4) are not used by the Sparkplug schema.
  return null;
}

function parseMetric(bytes: Uint8Array): PayloadMetric | null {
  const metric: PayloadMetric = {};
  let offset = 0;

  while (offset < bytes.length) {
    const tag = readVarint(bytes, offset);
    if (tag === null) return null;
    offset = tag.next;
    const fieldNumber = Number(tag.value >> 3n);
    const wireType = Number(tag.value & 7n);
    if (fieldNumber === 0) return null;

    if (wireType === 2) {
      const value = readDelimited(bytes, offset);
      if (value === null) return null;
      offset = value.next;
      if (fieldNumber === 1) {
        metric.name = decodeUtf8(value.data);
      } else if (fieldNumber === 15) {
        metric.stringValue = decodeUtf8(value.data);
      }
      continue;
    }

    if (wireType === 0) {
      const value = readVarint(bytes, offset);
      if (value === null) return null;
      offset = value.next;
      if (fieldNumber === 10) metric.intValue = value.value;
      if (fieldNumber === 11) metric.longValue = value.value;
      if (fieldNumber === 14) metric.booleanValue = value.value !== 0n;
      continue;
    }

    const next = skipField(bytes, offset, wireType);
    if (next === null) return null;
    offset = next;
  }

  return metric;
}

function parseProtobufPayload(bytes: Uint8Array): ParsedPayload | null {
  if (bytes.length > MAX_PAYLOAD_BYTES) return null;
  const payload: ParsedPayload = { metrics: [] };
  let offset = 0;

  while (offset < bytes.length) {
    const tag = readVarint(bytes, offset);
    if (tag === null) return null;
    offset = tag.next;
    const fieldNumber = Number(tag.value >> 3n);
    const wireType = Number(tag.value & 7n);
    if (fieldNumber === 0) return null;

    if (wireType === 2) {
      const value = readDelimited(bytes, offset);
      if (value === null) return null;
      offset = value.next;
      if (fieldNumber === 2) {
        const metric = parseMetric(value.data);
        if (metric === null) return null;
        payload.metrics.push(metric);
      }
      continue;
    }

    const next = skipField(bytes, offset, wireType);
    if (next === null) return null;
    offset = next;
  }

  return payload;
}

function looksLikeJson(bytes: Uint8Array): boolean {
  for (let index = 0; index < bytes.length; index += 1) {
    const byte = bytes[index];
    if (byte === 0x20 || byte === 0x09 || byte === 0x0a || byte === 0x0d) continue;
    if (byte === 0xef && bytes[index + 1] === 0xbb && bytes[index + 2] === 0xbf) {
      return looksLikeJson(bytes.subarray(index + 3));
    }
    return byte === 0x7b; // "{"
  }
  return false;
}

function parsePayload(payload: unknown): ParsedPayload | null {
  if (payload instanceof ArrayBuffer) {
    return parsePayload(new Uint8Array(payload));
  }

  if (payload instanceof Uint8Array) {
    if (payload.length === 0 || payload.length > MAX_PAYLOAD_BYTES) return null;
    if (looksLikeJson(payload)) {
      try {
        const value: unknown = JSON.parse(decodeUtf8(payload));
        if (isRecord(value)) return payloadFromJsonObject(value);
      } catch {
        return null;
      }
      return null;
    }
    return parseProtobufPayload(payload);
  }

  if (typeof payload === "string") {
    const trimmed = payload.trim();
    if (trimmed.length === 0) return null;
    if (trimmed.startsWith("{")) {
      try {
        const value: unknown = JSON.parse(trimmed);
        if (isRecord(value)) return payloadFromJsonObject(value);
      } catch {
        return null;
      }
      return null;
    }
    const numeric = toBigInt(trimmed);
    return numeric === undefined ? null : { metrics: [], directBdSeq: numeric };
  }

  if (typeof payload === "number" || typeof payload === "bigint") {
    const numeric = toBigInt(payload);
    return numeric === undefined ? null : { metrics: [], directBdSeq: numeric };
  }

  if (isRecord(payload)) return payloadFromJsonObject(payload);
  return null;
}

/**
 * Extract the Sparkplug birth/death sequence (`bdSeq`) from a payload.
 *
 * Supports the protobuf wire format, Tahu-style JSON objects, JSON encoded in
 * a Buffer/Uint8Array, and a direct numeric bdSeq value used by lightweight
 * integrations.
 */
export function extractBdSeq(payload: unknown): BdSeqExtraction {
  const parsed = parsePayload(payload);
  if (parsed === null) {
    return { ok: false, reason: "UNPARSEABLE_PAYLOAD" };
  }

  const candidates: bigint[] = [];
  if (parsed.directBdSeq !== undefined) candidates.push(parsed.directBdSeq);

  for (const metric of parsed.metrics) {
    if (metric.name !== BD_SEQ_METRIC) continue;
    if (metric.longValue !== undefined) {
      candidates.push(metric.longValue);
    } else if (metric.intValue !== undefined) {
      candidates.push(metric.intValue);
    } else {
      return { ok: false, reason: "BIRTH_SEQUENCE_INVALID" };
    }
  }

  if (candidates.length === 0) {
    return { ok: false, reason: "BIRTH_SEQUENCE_MISSING" };
  }

  const first = candidates[0];
  if (candidates.some((candidate) => candidate !== first)) {
    return { ok: false, reason: "BIRTH_SEQUENCE_INVALID" };
  }

  return { ok: true, bdSeq: first };
}

/**
 * Returns true only for an NCMD-style Sparkplug Rebirth metric with a truthy
 * boolean value. Non-Rebirth commands are deliberately not rate limited.
 */
export function isRebirthCommand(payload: unknown): boolean {
  const parsed = parsePayload(payload);
  if (parsed === null) return false;

  return parsed.metrics.some(
    (metric) => metric.name === REBIRTH_METRIC && metric.booleanValue === true,
  );
}

export const SPARKPLUG_BDSEQ_METRIC = BD_SEQ_METRIC;
export const SPARKPLUG_REBIRTH_METRIC = REBIRTH_METRIC;
