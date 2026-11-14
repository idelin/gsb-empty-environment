import type { DuplicateKeyRecord, JsonValue } from "./canonical.ts";
import { deepFreeze } from "./canonical.ts";
import { JsonNormalizationError } from "./errors.ts";
import type {
  AdmissionHeaders,
  AdmissionReason,
  AdmissionDecision,
  GateOptions,
  LayerDecision,
} from "./gateway-types.ts";
import type {
  NormalizedJsonDocument,
  NormalizedRequest,
  NormalizeOptions,
} from "./normalize.ts";
import {
  assertNormalizedRequest,
  isNormalizedRequest,
  normalizeRequest,
  tryNormalizeRequest,
  verifyNormalizedSignature,
} from "./normalize.ts";

export type {
  AdmissionHeaders,
  AdmissionReason,
  AdmissionDecision,
  GateOptions,
  LayerDecision,
  SignaturePolicy,
} from "./gateway-types.ts";

const EMPTY_AUTH: Readonly<Record<string, JsonValue>> = Object.freeze({});

export function isGateOptionsLike(value: unknown): value is GateOptions {
  if (value === null || typeof value !== "object") {
    return false;
  }
  if (
    typeof (value as { get?: unknown }).get === "function"
  ) {
    return false;
  }
  const candidate = value as Record<string, unknown>;
  if (
    "duplicatePolicy" in candidate &&
    (typeof candidate.duplicatePolicy !== "string" ||
      candidate.duplicatePolicy === "")
  ) {
    return false;
  }
  if (
    "protectedKeys" in candidate &&
    !Array.isArray(candidate.protectedKeys)
  ) {
    return false;
  }
  if (
    "allowedRoles" in candidate &&
    !Array.isArray(candidate.allowedRoles)
  ) {
    return false;
  }
  if (
    "requiredAuthKeys" in candidate &&
    !Array.isArray(candidate.requiredAuthKeys)
  ) {
    return false;
  }
  if (
    "signature" in candidate &&
    candidate.signature !== false &&
    (candidate.signature === null || typeof candidate.signature !== "object")
  ) {
    return false;
  }
  if (
    "maxDepth" in candidate &&
    typeof candidate.maxDepth !== "number"
  ) {
    return false;
  }
  if (
    "maxBytes" in candidate &&
    typeof candidate.maxBytes !== "number"
  ) {
    return false;
  }
  return [
    "duplicatePolicy",
    "protectedKeys",
    "caseInsensitiveProtectedKeys",
    "strictAuthKeyCase",
    "maxDepth",
    "maxBytes",
    "numberMode",
    "signature",
    "allowedRoles",
    "requiredAuthKeys",
    "rejectStatus",
  ].some((key) => key in candidate);
}

function headerValue(
  headers: AdmissionHeaders | undefined,
  name: string,
): string | undefined {
  if (headers === undefined || headers === null) {
    return undefined;
  }

  const maybeHeaders = headers as { get?: (key: string) => string | null };
  if (typeof maybeHeaders.get === "function") {
    const value = maybeHeaders.get(name);
    return value === null || value === undefined ? undefined : value;
  }

  const wanted = name.toLowerCase();
  for (const [key, value] of Object.entries(
    headers as Record<string, string | string[] | undefined>,
  )) {
    if (key.toLowerCase() !== wanted || value === undefined) {
      continue;
    }
    return Array.isArray(value) ? value[0] : String(value);
  }
  return undefined;
}

function reasonFromDuplicate(duplicate: DuplicateKeyRecord): AdmissionReason {
  const code = duplicate.protected
    ? "PROTECTED_KEY_DUPLICATE"
    : "DUPLICATE_KEY_REJECTED";
  const keyList = duplicate.keys.map((key) => JSON.stringify(key)).join(", ");
  return {
    code,
    message: duplicate.protected
      ? `Protected key ${JSON.stringify(duplicate.key)} appears more than once (${keyList})`
      : `Duplicate key ${JSON.stringify(duplicate.key)} is rejected by policy`,
    path: duplicate.path,
    key: duplicate.key,
    occurrences: duplicate.occurrences.map((occurrence) => ({
      index: occurrence.index,
      keyRange: occurrence.keyRange,
      valueRange: occurrence.valueRange,
    })),
  };
}

function reasonFromError(error: unknown): AdmissionReason {
  if (error instanceof JsonNormalizationError) {
    return {
      code: error.code,
      message: error.message,
      ...(error.path === undefined ? {} : { path: error.path }),
      ...(error.offset === undefined ? {} : { offset: error.offset }),
    };
  }
  if (error instanceof Error) {
    return {
      code: "NORMALIZATION_ERROR",
      message: error.message,
    };
  }
  return {
    code: "NORMALIZATION_ERROR",
    message: String(error),
  };
}

function authReason(
  code: string,
  message: string,
  key?: string,
  path?: string,
): AdmissionReason {
  return {
    code,
    message,
    ...(key === undefined ? {} : { key }),
    ...(path === undefined ? {} : { path }),
  };
}

function describeValue(value: unknown): string {
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function collectReasons(
  normalized: NormalizedRequest,
  headers: AdmissionHeaders | undefined,
  options: GateOptions,
): AdmissionReason[] {
  const reasons: AdmissionReason[] = [];

  for (const duplicate of normalized.duplicates) {
    if (duplicate.protected || duplicate.resolution === "reject") {
      reasons.push(reasonFromDuplicate(duplicate));
    }
  }

  for (const issue of normalized.authIssues) {
    reasons.push({
      code: issue.code,
      message: issue.message,
      key: issue.key,
      path: issue.path,
    });
  }

  if (options.allowedRoles !== undefined) {
    const role = normalized.auth.role;
    if (role === undefined) {
      reasons.push(
        authReason("ROLE_MISSING", "An allowed role is required"),
      );
    } else {
      const roles = Array.isArray(role) ? role : [role];
      const allowed = options.allowedRoles;
      if (
        !roles.some(
          (candidate) =>
            typeof candidate === "string" && allowed.includes(candidate),
        )
      ) {
        reasons.push(
          authReason(
            "ROLE_NOT_ALLOWED",
            `Role ${describeValue(role)} is not allowed`,
            "role",
            "$.role",
          ),
        );
      }
    }
  }

  if (options.requiredAuthKeys !== undefined) {
    for (const key of options.requiredAuthKeys) {
      if (!Object.hasOwn(normalized.auth, key)) {
        reasons.push(
          authReason(
            "AUTH_KEY_MISSING",
            `Required auth key ${JSON.stringify(key)} is missing`,
            key,
          ),
        );
      }
    }
  }

  const signaturePolicy =
    options.signature === false
      ? false
      : (options.signature ?? { header: "x-json-signature" });
  if (signaturePolicy !== false) {
    const header = signaturePolicy.header ?? "x-json-signature";
    const supplied = signaturePolicy.expected ?? headerValue(headers, header);
    if (supplied === undefined || supplied.length === 0) {
      if (signaturePolicy.required === true) {
        reasons.push(
          authReason(
            "SIGNATURE_MISSING",
            `Required signature header ${JSON.stringify(header)} is missing`,
          ),
        );
      }
    } else if (
      !verifyNormalizedSignature(
        normalized,
        supplied,
        signaturePolicy.secret,
      )
    ) {
      reasons.push(
        authReason(
          "SIGNATURE_INVALID",
          "Signature does not match the canonical collapsed document",
          undefined,
          "$",
        ),
      );
    }
  }

  return reasons;
}

function buildLayerDecision(
  layer: "waf" | "router",
  reasons: readonly AdmissionReason[],
  normalized: NormalizedRequest | null,
): LayerDecision {
  const allow = reasons.length === 0;
  const document: NormalizedJsonDocument | null =
    normalized === null ? null : normalized.document;
  const auth: Readonly<Record<string, JsonValue>> =
    normalized === null ? EMPTY_AUTH : normalized.auth;

  return deepFreeze({
    layer,
    decision: allow ? "allow" : "deny",
    allow,
    reasons,
    normalized,
    document,
    auth,
    digest: normalized === null ? null : normalized.digest,
  });
}

function buildDecision(
  reasons: readonly AdmissionReason[],
  normalized: NormalizedRequest | null,
): AdmissionDecision {
  const frozenReasons = deepFreeze([...reasons]);
  const waf = buildLayerDecision("waf", frozenReasons, normalized);
  const router = buildLayerDecision("router", frozenReasons, normalized);
  const allow = frozenReasons.length === 0;
  const consistent =
    waf.allow === router.allow &&
    waf.normalized === router.normalized &&
    waf.document === router.document &&
    waf.auth === router.auth &&
    waf.reasons === router.reasons &&
    waf.digest === router.digest;

  return deepFreeze({
    ok: allow,
    allow,
    decision: allow ? "allow" : "deny",
    reasons: frozenReasons,
    normalized,
    digest: normalized === null ? null : normalized.digest,
    waf,
    router,
    consistent,
  });
}

function decisionFromError(error: unknown): AdmissionDecision {
  return buildDecision([reasonFromError(error)], null);
}

/**
 * Admits a raw request by normalizing it once and evaluating the WAF and
 * router from the same immutable object.
 */
export function admitRequest(
  input: Parameters<typeof normalizeRequest>[0] | NormalizedRequest,
  headersOrOptions: AdmissionHeaders | GateOptions = {},
  options: GateOptions = {},
): AdmissionDecision {
  const secondArgumentIsOptions = isGateOptionsLike(headersOrOptions);
  const headers: AdmissionHeaders = secondArgumentIsOptions
    ? {}
    : (headersOrOptions as AdmissionHeaders);
  const effectiveOptions: GateOptions = secondArgumentIsOptions
    ? { ...(headersOrOptions as GateOptions), ...options }
    : options;
  if (isNormalizedRequest(input)) {
    return admitNormalized(input, headers, effectiveOptions);
  }
  const result = tryNormalizeRequest(input, {
    duplicatePolicy: effectiveOptions.duplicatePolicy,
    protectedKeys: effectiveOptions.protectedKeys,
    caseInsensitiveProtectedKeys:
      effectiveOptions.caseInsensitiveProtectedKeys,
    strictAuthKeyCase: effectiveOptions.strictAuthKeyCase,
    maxDepth: effectiveOptions.maxDepth,
    maxBytes: effectiveOptions.maxBytes,
    numberMode: effectiveOptions.numberMode,
  });
  if (!result.ok) {
    return decisionFromError(result.error);
  }
  return admitNormalized(result.normalized, headers, effectiveOptions);
}

/**
 * Admission for an already normalized request. This function cannot parse a
 * raw body and throws when given one.
 */
export function admitNormalized(
  normalized: NormalizedRequest,
  headersOrOptions: AdmissionHeaders | GateOptions = {},
  options: GateOptions = {},
): AdmissionDecision {
  assertNormalizedRequest(normalized);
  const secondArgumentIsOptions = isGateOptionsLike(headersOrOptions);
  const headers: AdmissionHeaders = secondArgumentIsOptions
    ? {}
    : (headersOrOptions as AdmissionHeaders);
  const effectiveOptions: GateOptions = secondArgumentIsOptions
    ? { ...(headersOrOptions as GateOptions), ...options }
    : options;
  return buildDecision(
    collectReasons(normalized, headers, effectiveOptions),
    normalized,
  );
}

export function evaluateWaf(
  normalized: NormalizedRequest,
  headersOrOptions: AdmissionHeaders | GateOptions = {},
  options: GateOptions = {},
): LayerDecision {
  return admitNormalized(normalized, headersOrOptions, options).waf;
}

export function routeRequest(
  normalized: NormalizedRequest,
  headersOrOptions: AdmissionHeaders | GateOptions = {},
  options: GateOptions = {},
): LayerDecision {
  return admitNormalized(normalized, headersOrOptions, options).router;
}

export function normalizedBody(
  normalized: NormalizedRequest,
): JsonValue {
  assertNormalizedRequest(normalized);
  return normalized.document.value;
}

export interface RequestGate {
  normalize(input: Parameters<typeof normalizeRequest>[0], options?: NormalizeOptions): NormalizedRequest;
  admit(input: Parameters<typeof normalizeRequest>[0] | NormalizedRequest, headersOrOptions?: AdmissionHeaders | GateOptions, options?: GateOptions): AdmissionDecision;
  admitNormalized(normalized: NormalizedRequest, headersOrOptions?: AdmissionHeaders | GateOptions, options?: GateOptions): AdmissionDecision;
  waf(normalized: NormalizedRequest, headersOrOptions?: AdmissionHeaders | GateOptions, options?: GateOptions): LayerDecision;
  route(normalized: NormalizedRequest, headersOrOptions?: AdmissionHeaders | GateOptions, options?: GateOptions): LayerDecision;
  handle(input: Parameters<typeof normalizeRequest>[0] | NormalizedRequest, headersOrOptions?: AdmissionHeaders | GateOptions, options?: GateOptions): AdmissionDecision;
}

export function createRequestGate(options: GateOptions = {}): RequestGate {
  const merged = (override: GateOptions): GateOptions => ({
    ...options,
    ...override,
    protectedKeys: override.protectedKeys ?? options.protectedKeys,
    signature:
      override.signature === undefined ? options.signature : override.signature,
  });

  return Object.freeze({
    normalize(input, override = {}) {
      return normalizeRequest(input, {
        ...options,
        ...override,
        protectedKeys: override.protectedKeys ?? options.protectedKeys,
      });
    },
    admit(input, headersOrOptions = {}, override = {}) {
      if (isGateOptionsLike(headersOrOptions)) {
        return admitRequest(input, {}, merged({ ...headersOrOptions, ...override }));
      }
      return admitRequest(input, headersOrOptions, merged(override));
    },
    admitNormalized(normalized, headersOrOptions = {}, override = {}) {
      if (isGateOptionsLike(headersOrOptions)) {
        return admitNormalized(
          normalized,
          {},
          merged({ ...headersOrOptions, ...override }),
        );
      }
      return admitNormalized(normalized, headersOrOptions, merged(override));
    },
    waf(normalized, headersOrOptions = {}, override = {}) {
      if (isGateOptionsLike(headersOrOptions)) {
        return evaluateWaf(
          normalized,
          {},
          merged({ ...headersOrOptions, ...override }),
        );
      }
      return evaluateWaf(normalized, headersOrOptions, merged(override));
    },
    route(normalized, headersOrOptions = {}, override = {}) {
      if (isGateOptionsLike(headersOrOptions)) {
        return routeRequest(
          normalized,
          {},
          merged({ ...headersOrOptions, ...override }),
        );
      }
      return routeRequest(normalized, headersOrOptions, merged(override));
    },
    handle(input, headersOrOptions = {}, override = {}) {
      if (isGateOptionsLike(headersOrOptions)) {
        return admitRequest(input, {}, merged({ ...headersOrOptions, ...override }));
      }
      return admitRequest(input, headersOrOptions, merged(override));
    },
  });
}

export const createGateway = createRequestGate;
export const createAdmissionGate = createRequestGate;
export const createGate = createRequestGate;

export interface GatewayRequestLike {
  rawBody?: unknown;
  body?: unknown;
  headers?: AdmissionHeaders;
  normalizedRequest?: NormalizedRequest;
  admission?: AdmissionDecision;
  [key: string]: unknown;
}

export interface GatewayResponseLike {
  statusCode?: number;
  status?: (code: number) => GatewayResponseLike;
  setHeader?: (name: string, value: string) => unknown;
  end?: (body?: string) => unknown;
  json?: (body: unknown) => unknown;
}

export interface MiddlewareOptions extends GateOptions {
  /** Defaults to 400 for a policy/parse denial. */
  rejectStatus?: number;
}

function isRawBody(value: unknown): value is string | Uint8Array | ArrayBuffer {
  return (
    typeof value === "string" ||
    value instanceof Uint8Array ||
    value instanceof ArrayBuffer
  );
}

function sendRejection(
  response: GatewayResponseLike,
  status: number,
  decision: AdmissionDecision,
): void {
  const body = {
    error: "request_rejected",
    reasons: decision.reasons,
    digest: decision.digest,
  };
  const payload = JSON.stringify(body);
  if (typeof response.status === "function") {
    const next = response.status(status) ?? response;
    if (typeof next.json === "function") {
      next.json(body);
      return;
    }
  }
  response.statusCode = status;
  if (typeof response.setHeader === "function") {
    response.setHeader("content-type", "application/json; charset=utf-8");
  }
  if (typeof response.end === "function") {
    response.end(payload);
  }
}

/**
 * Minimal Node-style middleware. It requires the raw body to still be
 * available on `req.rawBody` (or as a string/byte `req.body`). On success it
 * replaces `req.body` with the normalized value and exposes the shared object
 * on `req.normalizedRequest`.
 */
export function createAdmissionMiddleware(
  options: MiddlewareOptions = {},
) {
  const gate = createRequestGate(options);
  const rejectStatus = options.rejectStatus ?? 400;

  return function admissionMiddleware(
    request: GatewayRequestLike,
    response: GatewayResponseLike,
    next: (error?: unknown) => unknown,
  ): unknown {
    let raw: unknown;
    if (isRawBody(request.rawBody)) {
      raw = request.rawBody;
    } else if (isRawBody(request.body)) {
      raw = request.body;
    } else {
      const decision = buildDecision(
        [
          authReason(
            "RAW_BODY_UNAVAILABLE",
            "The raw request bytes are required; req.body was already parsed",
          ),
        ],
        null,
      );
      request.admission = decision;
      sendRejection(response, rejectStatus, decision);
      return undefined;
    }

    const decision = gate.admit(raw, request.headers ?? {}, options);
    request.admission = decision;
    if (decision.allow && decision.normalized !== null) {
      request.normalizedRequest = decision.normalized;
      request.body = decision.normalized.document.value;
    }

    if (!decision.allow) {
      sendRejection(response, rejectStatus, decision);
      return undefined;
    }
    return next();
  };
}

export function attachNormalizedRequest<
  T extends GatewayRequestLike,
>(
  request: T,
  decision: AdmissionDecision,
): T {
  request.admission = decision;
  if (decision.normalized !== null) {
    request.normalizedRequest = decision.normalized;
    request.body = decision.normalized.document.value;
  }
  return request;
}
