import { canonicalJson, cloneBytes, concatBytes, encodeText } from "./canonical.ts";
import { ErrorCode, PipelineError } from "./errors.ts";
import {
  constantTimeEqualHex,
  digestHex,
  normalizeDigestValue,
} from "./hash.ts";
import type { PlaceholderSpec, PlaceholderValue } from "./manifest.ts";

export interface RequestIdentity {
  readonly id?: string;
  readonly requestId?: string;
  readonly subject?: string;
  readonly body?: string | Uint8Array | ArrayBuffer;
  readonly bodyDigest?: string;
  readonly responseType?: string;
  readonly headers?: Readonly<Record<string, string>>;
  readonly placeholderValues?: Readonly<Record<string, PlaceholderValue>>;
  readonly values?: Readonly<Record<string, PlaceholderValue>>;
  readonly [key: string]: unknown;
}

export interface PlaceholderReplacement {
  readonly id: string;
  readonly offset: number;
  readonly sourceLength: number;
  readonly valueLength: number;
  readonly valueDigest: string;
  readonly bindingVerified: boolean;
}

export interface PlaceholderResult {
  readonly bytes: Uint8Array;
  readonly replacements: readonly PlaceholderReplacement[];
  readonly sourceLength: number;
  readonly finalLength: number;
}

export function normalizeRequestIdentity(
  value: RequestIdentity | string | number | null | undefined,
): RequestIdentity {
  if (value === undefined || value === null) {
    return {};
  }
  if (typeof value === "string") {
    return { requestId: value };
  }
  if (typeof value === "number") {
    return { requestId: String(value) };
  }
  return value;
}

function toValueBytes(
  value: unknown,
  encoding: PlaceholderSpec["encoding"],
  label: string,
): Uint8Array {
  if (value instanceof Uint8Array) {
    return cloneBytes(value);
  }
  if (value instanceof ArrayBuffer) {
    return new Uint8Array(value.slice(0));
  }
  if (
    Array.isArray(value) &&
    value.every(
      (byte) =>
        Number.isInteger(byte) &&
        (byte as number) >= 0 &&
        (byte as number) <= 255,
    )
  ) {
    return new Uint8Array(value as number[]);
  }
  if (typeof value === "string") {
    if (encoding === "hex") {
      if (!/^[0-9a-f]*$/i.test(value) || value.length % 2 !== 0) {
        throw new PipelineError(
          ErrorCode.PLACEHOLDER_VALUE,
          `${label} is not valid hexadecimal`,
          { value },
        );
      }
      return new Uint8Array(Buffer.from(value, "hex"));
    }
    if (encoding === "base64" || encoding === "base64url") {
      return new Uint8Array(Buffer.from(value, encoding));
    }
    return encodeText(value);
  }
  if (typeof value === "number" || typeof value === "bigint" || typeof value === "boolean") {
    return encodeText(String(value));
  }
  throw new PipelineError(
    ErrorCode.PLACEHOLDER_VALUE,
    `${label} cannot be converted to bytes`,
    { valueType: typeof value },
  );
}

function pathExists(identity: RequestIdentity, path: string): boolean {
  if (Object.prototype.hasOwnProperty.call(identity, path)) {
    return true;
  }
  const segments = path.split(".");
  let cursor: unknown = identity;
  for (const segment of segments) {
    if (cursor === null || typeof cursor !== "object") {
      return false;
    }
    const record = cursor as Record<string, unknown>;
    if (!Object.prototype.hasOwnProperty.call(record, segment)) {
      return false;
    }
    cursor = record[segment];
  }
  return true;
}

export function readIdentityField(
  identity: RequestIdentity,
  path: string,
): unknown {
  if (Object.prototype.hasOwnProperty.call(identity, path)) {
    return identity[path];
  }
  const segments = path.split(".");
  let cursor: unknown = identity;
  for (const segment of segments) {
    if (cursor === null || typeof cursor !== "object") {
      return undefined;
    }
    const record = cursor as Record<string, unknown>;
    if (!Object.prototype.hasOwnProperty.call(record, segment)) {
      return undefined;
    }
    cursor = record[segment];
  }
  return cursor;
}

export function requestBodyDigest(identity: RequestIdentity): string {
  if (typeof identity.bodyDigest === "string" && identity.bodyDigest.length > 0) {
    return identity.bodyDigest;
  }
  const body = identity.body ?? "";
  return `sha256:${digestHex("sha256", "gsb.request.body.v1", body)}`;
}

export function requestResponseType(identity: RequestIdentity): string {
  if (
    typeof identity.responseType === "string" &&
    identity.responseType.length > 0
  ) {
    return identity.responseType;
  }
  const nested = readIdentityField(identity, "response.type");
  return typeof nested === "string" && nested.length > 0 ? nested : "arraybuffer";
}

/**
 * Stable bytes for binding a replacement value to a concrete request.  Raw
 * request bodies are represented by their digest so the binding stays cheap
 * and does not leak the body into cache keys.
 */
export function identityBindingBytes(identity: RequestIdentity): Uint8Array {
  const requestId =
    identity.requestId ??
    identity.id ??
    (readIdentityField(identity, "request.id") as string | undefined) ??
    null;
  const headers = identity.headers ?? {};
  const sortedHeaders = Object.fromEntries(
    Object.entries(headers).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)),
  );
  const binding = {
    requestId,
    subject: identity.subject ?? null,
    body: requestBodyDigest(identity),
    responseType: requestResponseType(identity),
    headers: sortedHeaders,
  };
  return encodeText(canonicalJson(binding));
}

export function computePlaceholderBinding(
  identity: RequestIdentity,
  spec: Pick<PlaceholderSpec, "bindingAlgorithm" | "bindingDomain"> & {
    readonly encoding?: PlaceholderSpec["encoding"];
  },
  value: Uint8Array | string,
): string {
  const valueBytes =
    value instanceof Uint8Array
      ? value
      : toValueBytes(value, spec.encoding ?? "utf8", "value");
  const joined = concatBytes([
    identityBindingBytes(identity),
    new Uint8Array([0]),
    valueBytes,
  ]);
  return digestHex(spec.bindingAlgorithm, spec.bindingDomain, joined);
}

function explicitIdentityValues(
  identity: RequestIdentity,
): Readonly<Record<string, PlaceholderValue>> | undefined {
  return identity.placeholderValues ?? identity.values;
}

function resolvePlaceholderValue(
  identity: RequestIdentity,
  spec: PlaceholderSpec,
): { value: Uint8Array; fromIdentity: boolean } {
  if (spec.field !== undefined) {
    if (!pathExists(identity, spec.field)) {
      throw new PipelineError(
        ErrorCode.PLACEHOLDER_MISSING,
        `request identity has no field for placeholder ${spec.id}`,
        { id: spec.id, field: spec.field },
      );
    }
    const fieldValue = readIdentityField(identity, spec.field);
    return {
      value: toValueBytes(
        fieldValue,
        spec.encoding,
        `placeholder ${spec.id} field`,
      ),
      fromIdentity: true,
    };
  }

  const values = explicitIdentityValues(identity);
  if (values !== undefined && Object.prototype.hasOwnProperty.call(values, spec.id)) {
    return {
      value: toValueBytes(
        values[spec.id],
        spec.encoding,
        `placeholder ${spec.id} value`,
      ),
      fromIdentity: true,
    };
  }

  if (pathExists(identity, spec.id)) {
    const value = readIdentityField(identity, spec.id);
    if (
      value instanceof Uint8Array ||
      typeof value === "string" ||
      typeof value === "number" ||
      typeof value === "bigint" ||
      typeof value === "boolean"
    ) {
      return {
        value: toValueBytes(
          value,
          spec.encoding,
          `placeholder ${spec.id} identity value`,
        ),
        fromIdentity: true,
      };
    }
  }

  if (spec.value !== undefined) {
    return { value: cloneBytes(spec.value), fromIdentity: false };
  }

  throw new PipelineError(
    ErrorCode.PLACEHOLDER_MISSING,
    `no request-bound value was supplied for placeholder ${spec.id}`,
    { id: spec.id },
  );
}

export interface ReplaceOptions {
  /**
   * When true (the default), a placeholder without a binding digest still has
   * to obtain its value from the request identity.  Literal manifest values
   * are accepted as a convenience for static assets.
   */
  readonly requireIdentitySource?: boolean;
}

/**
 * Replaces only the spans declared in the manifest, in a single non-recursive
 * pass.  Values are looked up from the request identity; a declared
 * bindingDigest is checked over the identity binding and replacement bytes.
 */
export function replacePlaceholders(
  decoded: Uint8Array,
  placeholders: readonly PlaceholderSpec[],
  identity: RequestIdentity,
  options: ReplaceOptions = {},
): PlaceholderResult {
  const requireIdentitySource = options.requireIdentitySource ?? false;
  const sorted = [...placeholders].sort((a, b) => a.offset - b.offset);
  const chunks: Uint8Array[] = [];
  const replacements: PlaceholderReplacement[] = [];
  let cursor = 0;

  for (const spec of sorted) {
    if (
      spec.offset < cursor ||
      spec.offset + spec.length > decoded.byteLength
    ) {
      throw new PipelineError(
        ErrorCode.PLACEHOLDER_RANGE,
        `placeholder ${spec.id} lies outside the decoded representation`,
        {
          id: spec.id,
          offset: spec.offset,
          length: spec.length,
          decodedLength: decoded.byteLength,
          cursor,
        },
      );
    }
    if (spec.offset > cursor) {
      chunks.push(decoded.slice(cursor, spec.offset));
    }
    const source = decoded.subarray(spec.offset, spec.offset + spec.length);
    if (spec.token !== undefined && !bytesEqual(source, spec.token)) {
      throw new PipelineError(
        ErrorCode.PLACEHOLDER_TOKEN,
        `placeholder ${spec.id} does not occupy its declared source span`,
        {
          id: spec.id,
          offset: spec.offset,
          length: spec.length,
          expectedToken: Buffer.from(spec.token).toString("hex"),
          actual: Buffer.from(source).toString("hex"),
        },
      );
    }

    const resolved = resolvePlaceholderValue(identity, spec);
    if (
      requireIdentitySource &&
      !resolved.fromIdentity &&
      spec.binding !== "none"
    ) {
      throw new PipelineError(
        ErrorCode.PLACEHOLDER_BINDING,
        `placeholder ${spec.id} value is not bound to the request identity`,
        { id: spec.id },
      );
    }

    let bindingVerified = false;
    if (spec.binding === "identity") {
      if (spec.bindingDigest !== undefined) {
        const actual = computePlaceholderBinding(identity, spec, resolved.value);
        if (!constantTimeEqualHex(spec.bindingDigest, actual)) {
          throw new PipelineError(
            ErrorCode.PLACEHOLDER_BINDING,
            `placeholder ${spec.id} is not bound to this request identity`,
            {
              id: spec.id,
              algorithm: spec.bindingAlgorithm,
              domain: spec.bindingDomain,
              expected: spec.bindingDigest,
              actual,
            },
          );
        }
        bindingVerified = true;
      } else if (resolved.fromIdentity) {
        bindingVerified = true;
      }
    }

    chunks.push(resolved.value);
    replacements.push({
      id: spec.id,
      offset: spec.offset,
      sourceLength: spec.length,
      valueLength: resolved.value.byteLength,
      valueDigest: digestHex(
        "sha256",
        "gsb.placeholder.value.v1",
        resolved.value,
      ),
      bindingVerified,
    });
    cursor = spec.offset + spec.length;
  }

  if (cursor < decoded.byteLength) {
    chunks.push(decoded.slice(cursor));
  }
  const bytes = chunks.length === 0 ? new Uint8Array(0) : concatBytes(chunks);
  return {
    bytes,
    replacements,
    sourceLength: decoded.byteLength,
    finalLength: bytes.byteLength,
  };
}

/**
 * Stable digest of the request-bound placeholder inputs.  It can be computed
 * before decoded bytes exist, which lets an asset host look up a verified
 * cache entry without weakening the per-identity binding.
 */
export function placeholderIdentityDigest(
  identity: RequestIdentity,
  placeholders: readonly PlaceholderSpec[],
): string {
  const entries = [...placeholders]
    .sort((a, b) =>
      a.offset === b.offset
        ? a.id < b.id
          ? -1
          : a.id > b.id
            ? 1
            : 0
        : a.offset - b.offset,
    )
    .map((spec) => {
      let resolved: { value: Uint8Array; fromIdentity: boolean };
      try {
        resolved = resolvePlaceholderValue(identity, spec);
      } catch (error) {
        if (error instanceof PipelineError) {
          return {
            id: spec.id,
            offset: spec.offset,
            sourceLength: spec.length,
            unresolved: true,
          };
        }
        throw error;
      }
      let bindingVerified = false;
      if (spec.binding === "identity") {
        if (spec.bindingDigest !== undefined) {
          bindingVerified = constantTimeEqualHex(
            spec.bindingDigest,
            computePlaceholderBinding(identity, spec, resolved.value),
          );
        } else if (resolved.fromIdentity) {
          bindingVerified = true;
        }
      }
      return {
        id: spec.id,
        offset: spec.offset,
        sourceLength: spec.length,
        valueLength: resolved.value.byteLength,
        valueDigest: digestHex(
          "sha256",
          "gsb.placeholder.value.v1",
          resolved.value,
        ),
        bindingVerified,
      };
    });
  return digestHex(
    "sha256",
    "gsb.placeholder.binding.v1",
    concatBytes([
      identityBindingBytes(identity),
      new Uint8Array([0]),
      encodeText(canonicalJson({ entries })),
    ]),
  );
}

function bytesEqual(left: Uint8Array, right: Uint8Array): boolean {
  if (left.byteLength !== right.byteLength) {
    return false;
  }
  for (let index = 0; index < left.byteLength; index += 1) {
    if (left[index] !== right[index]) {
      return false;
    }
  }
  return true;
}

export function normalizePlaceholderBindingDigest(
  identity: RequestIdentity,
  spec: Pick<PlaceholderSpec, "bindingAlgorithm" | "bindingDomain">,
  value: string | Uint8Array,
): string {
  return normalizeDigestValue(
    computePlaceholderBinding(identity, spec, value),
    spec.bindingAlgorithm,
  );
}
