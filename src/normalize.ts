import { createHash, createHmac, timingSafeEqual } from "node:crypto";
import type {
  CanonicalNode,
  DuplicateKeyRecord,
  DuplicatePolicy,
  JsonValue,
  NumberValueMode,
} from "./canonical.ts";
import {
  asciiLowerCase,
  canonicalizeParsedJson,
  DEFAULT_PROTECTED_KEYS,
  deepFreeze,
  serializeCanonical,
  toPlainValue,
} from "./canonical.ts";
import type { ByteRange, JsonInput } from "./parser.ts";
import {
  DEFAULT_MAX_BYTES,
  DEFAULT_MAX_DEPTH,
  parseJsonOnce,
} from "./parser.ts";

export const NORMALIZED_REQUEST: unique symbol = Symbol.for(
  "pairwise-gsb.normalized-request.v1",
);

export interface AuthKeyIssue {
  code: "AUTH_KEY_CASE_MISMATCH";
  message: string;
  key: string;
  canonicalKey: string;
  path: string;
}

export interface NormalizedJsonDocument {
  root: CanonicalNode;
  node: CanonicalNode;
  value: JsonValue;
}

export interface NormalizeOptions {
  duplicatePolicy?: DuplicatePolicy;
  protectedKeys?: readonly string[];
  caseInsensitiveProtectedKeys?: boolean;
  /**
   * Reject a protected key whose source spelling differs from the canonical
   * spelling (for example `ROLE` instead of `role`). Defaults to true.
   */
  strictAuthKeyCase?: boolean;
  maxDepth?: number;
  maxBytes?: number;
  numberMode?: NumberValueMode;
}

export interface NormalizedRequest {
  readonly [NORMALIZED_REQUEST]: true;
  readonly document: NormalizedJsonDocument;
  /** Convenience alias for document.root. */
  readonly root: CanonicalNode;
  readonly canonicalRoot: CanonicalNode;
  readonly node: CanonicalNode;
  readonly value: JsonValue;
  readonly duplicates: readonly DuplicateKeyRecord[];
  readonly duplicateKeys: readonly DuplicateKeyRecord[];
  readonly protectedDuplicates: readonly DuplicateKeyRecord[];
  readonly auth: Readonly<Record<string, JsonValue>>;
  readonly authIssues: readonly AuthKeyIssue[];
  readonly canonicalJson: string;
  readonly signaturePayload: string;
  readonly bodyRange: ByteRange;
  /** SHA-256 of canonicalJson's UTF-8 bytes. */
  readonly digest: string;
  readonly policy: DuplicatePolicy;
  readonly stats: {
    readonly bytes: number;
    readonly nodes: number;
    readonly maxDepthSeen: number;
    readonly duplicateGroups: number;
    readonly protectedDuplicateGroups: number;
  };
}

export interface NormalizationFailure {
  ok: false;
  error: unknown;
}

export interface NormalizationSuccess {
  ok: true;
  normalized: NormalizedRequest;
}

export type NormalizationResult = NormalizationSuccess | NormalizationFailure;

export function sha256Hex(value: string | Uint8Array): string {
  return createHash("sha256").update(value).digest("hex");
}

export function hmacSha256Hex(
  value: string | Uint8Array,
  secret: string | Uint8Array,
): string {
  return createHmac("sha256", secret).update(value).digest("hex");
}

function defineValue(
  target: Record<string, JsonValue>,
  key: string,
  value: JsonValue,
): void {
  Object.defineProperty(target, key, {
    value,
    enumerable: true,
    configurable: false,
    writable: false,
  });
}

function extractAuth(
  root: CanonicalNode,
  plainRoot: JsonValue,
  options: {
    protectedKeys: readonly string[];
    caseInsensitiveProtectedKeys: boolean;
    strictAuthKeyCase: boolean;
  },
): {
  auth: Readonly<Record<string, JsonValue>>;
  authIssues: AuthKeyIssue[];
} {
  const auth: Record<string, JsonValue> = {};
  const authIssues: AuthKeyIssue[] = [];
  if (
    root.kind !== "object" ||
    plainRoot === null ||
    typeof plainRoot !== "object" ||
    Array.isArray(plainRoot)
  ) {
    return { auth, authIssues };
  }

  const canonicalNames = new Map<string, string>();
  for (const key of options.protectedKeys) {
    const normalized = options.caseInsensitiveProtectedKeys
      ? asciiLowerCase(key)
      : key;
    if (!canonicalNames.has(normalized)) {
      canonicalNames.set(normalized, key);
    }
  }

  for (const entry of root.entries) {
    const normalized = options.caseInsensitiveProtectedKeys
      ? asciiLowerCase(entry.key)
      : entry.key;
    const canonicalKey = canonicalNames.get(normalized);
    if (canonicalKey === undefined) {
      continue;
    }

    if (
      options.strictAuthKeyCase &&
      options.caseInsensitiveProtectedKeys &&
      entry.sourceKey !== canonicalKey
    ) {
      authIssues.push({
        code: "AUTH_KEY_CASE_MISMATCH",
        message: `Protected key ${JSON.stringify(entry.sourceKey)} must use canonical spelling ${JSON.stringify(canonicalKey)}`,
        key: entry.sourceKey,
        canonicalKey,
        path: entry.value.path,
      });
    }

    const descriptor = Object.getOwnPropertyDescriptor(plainRoot, canonicalKey);
    if (descriptor !== undefined) {
      defineValue(auth, canonicalKey, descriptor.value as JsonValue);
    }
  }

  return { auth, authIssues };
}

/**
 * Parses the request body exactly once and returns the immutable normalized
 * object shared by every downstream layer.
 */
export function normalizeRequest(
  input: JsonInput,
  options: NormalizeOptions = {},
): NormalizedRequest {
  const duplicatePolicy = options.duplicatePolicy ?? "last";
  const protectedKeys = options.protectedKeys ?? DEFAULT_PROTECTED_KEYS;
  const caseInsensitiveProtectedKeys =
    options.caseInsensitiveProtectedKeys ?? true;
  const strictAuthKeyCase = options.strictAuthKeyCase ?? true;
  const numberMode = options.numberMode ?? "safe";
  if (
    numberMode !== "safe" &&
    numberMode !== "exact" &&
    numberMode !== "number" &&
    numberMode !== "bigint"
  ) {
    throw new TypeError(
      "numberMode must be 'safe', 'exact', 'number', or 'bigint'",
    );
  }

  const parsed = parseJsonOnce(input, {
    maxDepth: options.maxDepth ?? DEFAULT_MAX_DEPTH,
    maxBytes: options.maxBytes ?? DEFAULT_MAX_BYTES,
  });
  const canonical = canonicalizeParsedJson(parsed, {
    duplicatePolicy,
    protectedKeys,
    caseInsensitiveProtectedKeys,
  });
  const canonicalJson = serializeCanonical(canonical.root);
  const digest = sha256Hex(canonicalJson);
  const value = toPlainValue(canonical.root, numberMode);
  const { auth, authIssues } = extractAuth(canonical.root, value, {
    protectedKeys,
    caseInsensitiveProtectedKeys,
    strictAuthKeyCase,
  });
  const protectedDuplicates = canonical.duplicates.filter(
    (duplicate) => duplicate.protected,
  );

  const normalized: NormalizedRequest = {
    [NORMALIZED_REQUEST]: true,
    document: {
      root: canonical.root,
      node: canonical.root,
      value,
    },
    root: canonical.root,
    canonicalRoot: canonical.root,
    node: canonical.root,
    value,
    duplicates: canonical.duplicates,
    duplicateKeys: canonical.duplicates,
    protectedDuplicates,
    auth,
    authIssues,
    canonicalJson,
    signaturePayload: canonicalJson,
    bodyRange: { start: 0, end: parsed.stats.bytes },
    digest,
    policy: duplicatePolicy,
    stats: {
      bytes: parsed.stats.bytes,
      nodes: parsed.stats.nodes,
      maxDepthSeen: parsed.stats.maxDepthSeen,
      duplicateGroups: canonical.duplicates.length,
      protectedDuplicateGroups: protectedDuplicates.length,
    },
  };

  return deepFreeze(normalized);
}

export function normalizeJson(
  input: JsonInput,
  options: NormalizeOptions = {},
): NormalizedRequest {
  return normalizeRequest(input, options);
}

export function normalizeRequestBody(
  input: JsonInput,
  options: NormalizeOptions = {},
): NormalizedRequest {
  return normalizeRequest(input, options);
}

export function tryNormalizeRequest(
  input: JsonInput,
  options: NormalizeOptions = {},
): NormalizationResult {
  try {
    return { ok: true, normalized: normalizeRequest(input, options) };
  } catch (error) {
    return { ok: false, error };
  }
}

export function isNormalizedRequest(
  value: unknown,
): value is NormalizedRequest {
  if (value === null || typeof value !== "object") {
    return false;
  }
  const candidate = value as Record<PropertyKey, unknown>;
  const document = candidate.document as
    | { root?: unknown; value?: unknown }
    | null
    | undefined;
  return (
    candidate[NORMALIZED_REQUEST] === true &&
    typeof candidate.canonicalJson === "string" &&
    typeof candidate.digest === "string" &&
    Array.isArray(candidate.duplicates) &&
    Array.isArray(candidate.authIssues) &&
    document !== null &&
    typeof document === "object" &&
    document.root !== undefined &&
    "value" in document
  );
}

export function assertNormalizedRequest(
  value: unknown,
): asserts value is NormalizedRequest {
  if (!isNormalizedRequest(value)) {
    throw new TypeError(
      "A normalized request is required; do not pass or re-parse the raw body",
    );
  }
}

export function signNormalized(
  normalized: NormalizedRequest,
  secret?: string | Uint8Array,
): string {
  assertNormalizedRequest(normalized);
  return secret === undefined
    ? normalized.digest
    : hmacSha256Hex(normalized.canonicalJson, secret);
}

export function normalizeSignatureValue(signature: string): string {
  const trimmed = signature.trim();
  const separator = trimmed.indexOf("=");
  if (separator >= 0) {
    const algorithm = asciiLowerCase(trimmed.slice(0, separator));
    if (
      algorithm === "sha256" ||
      algorithm === "hmac-sha256" ||
      algorithm === "hmac_sha256"
    ) {
      return trimmed.slice(separator + 1).trim();
    }
  }
  return trimmed;
}

export function verifyNormalizedSignature(
  normalized: NormalizedRequest,
  signature: string,
  secret?: string | Uint8Array,
): boolean {
  const provided = normalizeSignatureValue(signature);
  if (!/^[0-9a-fA-F]{64}$/.test(provided)) {
    return false;
  }
  const expected = signNormalized(normalized, secret);
  return timingSafeEqual(
    Buffer.from(expected, "hex"),
    Buffer.from(provided.toLowerCase(), "hex"),
  );
}

export function computeDigest(
  value: NormalizedRequest | CanonicalNode | string | Uint8Array,
  secret?: string | Uint8Array,
): string {
  let payload: string | Uint8Array;
  if (isNormalizedRequest(value)) {
    payload = value.canonicalJson;
  } else if (typeof value === "string" || value instanceof Uint8Array) {
    payload = value;
  } else {
    payload = serializeCanonical(value);
  }
  return secret === undefined
    ? sha256Hex(payload)
    : hmacSha256Hex(payload, secret);
}
