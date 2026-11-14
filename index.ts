/**
 * HTTP method-override resolution for an API gateway.
 *
 * The gateway must never authorize only the transport method when a request
 * can ask the origin server to execute a different method (for example,
 * `POST` + `X-HTTP-Method-Override: DELETE`).  This module resolves the
 * effective method and, when authorization is requested, evaluates both the
 * transport method and the effective method.  Any deny/challenge wins.
 *
 * Method override is opt-in.  A declaration is honored only when:
 *   - `options.enabled === true`,
 *   - the request source is trusted (explicit origin allow-list, a same-origin
 *     request, or a gateway-provided trust callback/marker), and
 *   - the transport method is eligible (POST by default).
 *
 * Security controls (CSRF, audit and rate limiting) always use the original
 * transport method.  The returned `forwardHeaders`/`forwardBody` have all
 * override declarations removed so an origin server cannot honor a
 * declaration that the gateway ignored.
 */

export type HeaderValue = string | number | readonly string[] | null | undefined;

export type HeaderCollection =
  | Record<string, HeaderValue>
  | Map<string, HeaderValue>
  | Iterable<readonly [string, HeaderValue]>
  | { get(name: string): string | null | undefined };

export interface MethodRequest {
  method: string;
  url?: string | URL;
  headers?: HeaderCollection;
  body?: unknown;
  /** Origin asserted by the gateway (not necessarily the raw Origin header). */
  origin?: string;
  /** Gateway-normalized trust signal. Prefer `isTrustedSource` for raw requests. */
  trusted?: boolean;
  sourceTrusted?: boolean;
  [key: string]: unknown;
}

export type OverrideSource = "header" | "body" | "both";

export interface MethodDeclaration {
  source: "header" | "body";
  name: string;
  method: string;
  raw: string;
}

export interface SecurityControlRecords {
  csrf: { method: string };
  audit: { method: string };
  rateLimit: { method: string };
}

export interface MethodAuditRecord {
  /** Always the transport method, never the overridden method. */
  method: string;
  originalMethod: string;
  effectiveMethod: string;
  overrideDeclared: boolean;
  overrideApplied: boolean;
  overrideSource: OverrideSource | null;
  rejected: boolean;
  reasonCode: MethodRejectionCode | null;
}

export type MethodRejectionCode =
  | "INVALID_ORIGINAL_METHOD"
  | "INVALID_OVERRIDE_METHOD"
  | "CONFLICTING_HEADER_METHODS"
  | "CONFLICTING_BODY_METHODS"
  | "CONFLICTING_BODY_AND_HEADER"
  | "OVERRIDE_METHOD_NOT_ALLOWED"
  | "OVERRIDE_DISABLED"
  | "UNTRUSTED_OVERRIDE"
  | "ORIGINAL_METHOD_NOT_ELIGIBLE";

export interface MethodResolution {
  /** False when the request must be rejected before authorization. */
  valid: boolean;
  rejected: boolean;
  originalMethod: string;
  effectiveMethod: string;
  declaredMethod: string | null;
  overrideDeclared: boolean;
  /** True only when the declaration changed the method used for forwarding. */
  overrideApplied: boolean;
  /** True when the declaration was honored, even if it named the same method. */
  overrideAccepted: boolean;
  overrideSource: OverrideSource | null;
  declarations: MethodDeclaration[];
  reasonCode: MethodRejectionCode | null;
  reason: string | null;
  /** Unique methods that must all be authorized, original first. */
  authorizationMethods: string[];
  /** Control-plane records are always keyed by the original method. */
  security: SecurityControlRecords;
  csrfMethod: string;
  auditMethod: string;
  rateLimitMethod: string;
  audit: MethodAuditRecord;
  /** Header names that must not be forwarded to the origin. */
  stripHeaderNames: string[];
  /** Form fields that must be removed before the body is forwarded. */
  stripBodyFields: string[];
  forwardHeaders: Record<string, string | string[]>;
  forwardBody: unknown;
}

export interface MethodOverrideOptions {
  /** Method override is disabled unless this is explicitly `true`. */
  enabled?: boolean;
  /** Additional header names. The standard override headers are always recognized. */
  headerNames?: readonly string[];
  /** Additional form-field names. `_method` is always recognized. */
  formFields?: readonly string[];
  /** Exact origins allowed to use override, e.g. `https://app.example.com`. */
  trustedOrigins?: readonly string[];
  /** Gateway callback for trust decisions not expressible as an origin list. */
  isTrustedSource?: (request: MethodRequest) => boolean;
  /** Same-origin requests are trusted when both origins can be derived. */
  allowSameOrigin?: boolean;
  /** Transport methods eligible for override. Defaults to POST. */
  allowedOriginalMethods?: readonly string[];
  /** Methods that may be selected by an override declaration. */
  allowedOverrideMethods?: readonly string[];
  /**
   * What to do with a syntactically valid declaration that cannot be honored
   * (override disabled, untrusted source, ineligible transport method).
   * `ignore` keeps the original method but strips the declaration; `reject`
   * refuses the request. Conflicting or malformed declarations are always
   * rejected regardless of this setting.
   */
  unhonoredOverridePolicy?: "ignore" | "reject";
  /** Optional default authorizer for `createMethodOverrideGuard`. */
  authorize?: MethodAuthorizer;
}

export type MethodAuthorizationVerdict = "allow" | "challenge" | "deny";

export type MethodAuthorizationResult =
  | boolean
  | MethodAuthorizationVerdict
  | {
      allowed?: boolean;
      verdict?: MethodAuthorizationVerdict;
      reason?: string;
    };

export interface MethodAuthorizationContext {
  /** Method currently being evaluated (original or effective). */
  method: string;
  originalMethod: string;
  effectiveMethod: string;
  phase: "original" | "effective" | "original-and-effective";
  overrideApplied: boolean;
  /** CSRF must be checked against this method, never the override. */
  csrfMethod: string;
  /** Audit entries must use this method as their primary method. */
  auditMethod: string;
  /** Rate-limit buckets must use this method. */
  rateLimitMethod: string;
  request: MethodRequest;
  resolution: MethodResolution;
}

export type MethodAuthorizer = (
  context: MethodAuthorizationContext,
) => MethodAuthorizationResult | Promise<MethodAuthorizationResult>;

export interface MethodAuthorizationCheck {
  method: string;
  phase: "original" | "effective" | "original-and-effective";
  allowed: boolean;
  verdict: MethodAuthorizationVerdict;
  reason: string | null;
}

export interface MethodDecision extends MethodResolution {
  allowed: boolean;
  outcome: MethodAuthorizationVerdict;
  authorization: {
    evaluated: boolean;
    methods: string[];
    checks: MethodAuthorizationCheck[];
    deniedBy: string[];
    challengedBy: string[];
  };
}

export const DEFAULT_OVERRIDE_HEADERS = Object.freeze([
  "x-http-method-override",
  "x-method-override",
  "x-http-method",
]);

export const DEFAULT_OVERRIDE_FORM_FIELDS = Object.freeze(["_method"]);

export const DEFAULT_ALLOWED_ORIGINAL_METHODS = Object.freeze(["POST"]);

export const DEFAULT_ALLOWED_OVERRIDE_METHODS = Object.freeze([
  "GET",
  "HEAD",
  "POST",
  "PUT",
  "PATCH",
  "DELETE",
  "OPTIONS",
]);

const METHOD_TOKEN = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;

export function normalizeMethod(value: unknown): string | null {
  if (typeof value !== "string") return null;
  const method = value.trim().toUpperCase();
  return method.length > 0 && METHOD_TOKEN.test(method) ? method : null;
}

export function isValidMethod(value: unknown): boolean {
  return normalizeMethod(value) !== null;
}

function methodSet(values: readonly string[] | undefined, fallback: readonly string[]): Set<string> {
  return new Set((values ?? fallback).map((value) => String(value).trim().toUpperCase()));
}

function configuredHeaderNames(options: MethodOverrideOptions): string[] {
  return Array.from(
    new Set([...DEFAULT_OVERRIDE_HEADERS, ...(options.headerNames ?? [])].map((name) => name.toLowerCase())),
  );
}

function configuredFormFields(options: MethodOverrideOptions): string[] {
  return Array.from(new Set([...DEFAULT_OVERRIDE_FORM_FIELDS, ...(options.formFields ?? [])]));
}

function splitHeaderValue(value: string): string[] {
  return value
    .split(",")
    .map((part) => part.trim())
    .filter((part) => part.length > 0);
}

function flattenHeaderValue(value: HeaderValue): string[] {
  if (value === null || value === undefined) return [];
  if (Array.isArray(value)) {
    return value.flatMap((item) => splitHeaderValue(String(item)));
  }
  return splitHeaderValue(String(value));
}

/**
 * Return every value for a header, case-insensitively. Arrays and repeated
 * header names are preserved so conflicting declarations can be detected.
 */
export function getHeaderValues(headers: HeaderCollection | undefined, name: string): string[] {
  if (!headers) return [];
  const target = name.toLowerCase();
  const result: string[] = [];

  if (headers instanceof Map) {
    for (const [key, value] of headers) {
      if (String(key).toLowerCase() === target) result.push(...flattenHeaderValue(value));
    }
    return result;
  }

  const maybeGet = (headers as { get?: unknown }).get;
  if (typeof maybeGet === "function") {
    try {
      const value = (maybeGet as (key: string) => unknown).call(headers, name);
      if (typeof value === "string" || typeof value === "number") {
        result.push(...splitHeaderValue(String(value)));
      } else if (Array.isArray(value)) {
        result.push(...flattenHeaderValue(value as HeaderValue));
      }
      return result;
    } catch {
      // Fall through to iterable/object handling.
    }
  }

  const iterable = headers as { [Symbol.iterator]?: unknown };
  if (typeof iterable[Symbol.iterator] === "function" && !(headers instanceof String)) {
    try {
      for (const entry of headers as Iterable<unknown>) {
        if (!Array.isArray(entry) || entry.length < 2) continue;
        if (String(entry[0]).toLowerCase() !== target) continue;
        result.push(...flattenHeaderValue(entry[1] as HeaderValue));
      }
      return result;
    } catch {
      // Fall through to object handling.
    }
  }

  const maybeEntries = (headers as { entries?: unknown }).entries;
  if (typeof maybeEntries === "function") {
    try {
      for (const entry of (maybeEntries as () => Iterable<unknown>).call(headers)) {
        if (!Array.isArray(entry) || entry.length < 2) continue;
        if (String(entry[0]).toLowerCase() !== target) continue;
        result.push(...flattenHeaderValue(entry[1] as HeaderValue));
      }
      if (result.length > 0) return result;
    } catch {
      // Fall through to object handling.
    }
  }

  if (typeof headers === "object" && !Array.isArray(headers) && !(headers instanceof String)) {
    for (const [key, value] of Object.entries(headers as Record<string, HeaderValue>)) {
      if (key.toLowerCase() === target) result.push(...flattenHeaderValue(value));
    }
  }

  return result;
}

function firstHeader(headers: HeaderCollection | undefined, name: string): string | null {
  return getHeaderValues(headers, name)[0] ?? null;
}

function cloneHeaders(headers: HeaderCollection | undefined): Record<string, string | string[]> {
  const result: Record<string, string | string[]> = {};
  if (!headers) return result;

  if (headers instanceof Map) {
    for (const [key, value] of headers) {
      const values = flattenHeaderValue(value);
      if (values.length > 0) result[String(key).toLowerCase()] = values.length === 1 ? values[0] : values;
    }
    return result;
  }

  const iterable = headers as { [Symbol.iterator]?: unknown };
  if (typeof iterable[Symbol.iterator] === "function" && !(headers instanceof String)) {
    try {
      for (const entry of headers as Iterable<unknown>) {
        if (!Array.isArray(entry) || entry.length < 2) continue;
        const key = String(entry[0]).toLowerCase();
        const values = flattenHeaderValue(entry[1] as HeaderValue);
        if (values.length === 0) continue;
        const existing = result[key];
        if (existing === undefined) {
          result[key] = values.length === 1 ? values[0] : values;
        } else {
          const merged = (Array.isArray(existing) ? existing : [existing]).concat(values);
          result[key] = merged;
        }
      }
      return result;
    } catch {
      // Fall through to object handling.
    }
  }

  const maybeEntries = (headers as { entries?: unknown }).entries;
  if (typeof maybeEntries === "function") {
    try {
      let sawEntry = false;
      for (const entry of (maybeEntries as () => Iterable<unknown>).call(headers)) {
        if (!Array.isArray(entry) || entry.length < 2) continue;
        sawEntry = true;
        const key = String(entry[0]).toLowerCase();
        const values = flattenHeaderValue(entry[1] as HeaderValue);
        if (values.length === 0) continue;
        const existing = result[key];
        if (existing === undefined) {
          result[key] = values.length === 1 ? values[0] : values;
        } else {
          const merged = (Array.isArray(existing) ? existing : [existing]).concat(values);
          result[key] = merged;
        }
      }
      if (sawEntry) return result;
    } catch {
      // Fall through to object handling.
    }
  }

  if (typeof headers === "object" && !Array.isArray(headers) && !(headers instanceof String)) {
    for (const [key, value] of Object.entries(headers as Record<string, HeaderValue>)) {
      const values = flattenHeaderValue(value);
      if (values.length === 0) continue;
      result[key.toLowerCase()] = values.length === 1 ? values[0] : values;
    }
  }

  return result;
}

function stripHeaders(
  headers: HeaderCollection | undefined,
  names: readonly string[],
): Record<string, string | string[]> {
  const clone = cloneHeaders(headers);
  const stripped = new Set(names.map((name) => name.toLowerCase()));
  for (const key of Object.keys(clone)) {
    if (stripped.has(key.toLowerCase())) delete clone[key];
  }
  return clone;
}

function contentType(headers: HeaderCollection | undefined): string {
  return (firstHeader(headers, "content-type") ?? "").toLowerCase();
}

function isFormContentType(value: string): boolean {
  return value.includes("application/x-www-form-urlencoded") || value.includes("multipart/form-data");
}

function addBodyValues(values: unknown, destination: string[]): void {
  if (values === null || values === undefined) return;
  if (Array.isArray(values)) {
    for (const item of values) addBodyValues(item, destination);
    return;
  }
  destination.push(String(values));
}

function readBodyDeclarations(
  body: unknown,
  fields: readonly string[],
  headers: HeaderCollection | undefined,
): Array<{ name: string; raw: string }> {
  if (body === null || body === undefined) return [];

  const found: Array<{ name: string; raw: string }> = [];
  const addFromGetter = (getter: { getAll?: (key: string) => unknown; get?: (key: string) => unknown }) => {
    for (const field of fields) {
      let values: unknown;
      if (typeof getter.getAll === "function") {
        values = getter.getAll(field);
      } else if (typeof getter.get === "function") {
        values = getter.get(field);
      }
      const collected: string[] = [];
      addBodyValues(values, collected);
      for (const raw of collected) found.push({ name: field, raw });
    }
  };

  if (typeof URLSearchParams !== "undefined" && body instanceof URLSearchParams) {
    addFromGetter(body);
    return found;
  }

  if (typeof FormData !== "undefined" && body instanceof FormData) {
    addFromGetter(body);
    return found;
  }

  if (typeof body === "string") {
    if (!isFormContentType(contentType(headers))) return [];
    try {
      addFromGetter(new URLSearchParams(body));
    } catch {
      return [];
    }
    return found;
  }

  if (body instanceof Uint8Array) {
    if (!isFormContentType(contentType(headers))) return [];
    try {
      addFromGetter(new URLSearchParams(new TextDecoder().decode(body)));
    } catch {
      return [];
    }
    return found;
  }

  if (typeof body === "object" && body !== null) {
    const type = contentType(headers);
    // Parsed form bodies may legitimately arrive as objects/maps. Do not treat
    // `_method` in an explicitly JSON/text body as a form override.
    if (type.length > 0 && !isFormContentType(type)) return [];

    if (body instanceof Map) {
      for (const field of fields) {
        for (const [key, value] of body) {
          if (String(key) !== field) continue;
          const collected: string[] = [];
          addBodyValues(value, collected);
          for (const raw of collected) found.push({ name: field, raw });
        }
      }
      return found;
    }

    for (const field of fields) {
      if (!Object.prototype.hasOwnProperty.call(body, field)) continue;
      const collected: string[] = [];
      addBodyValues((body as Record<string, unknown>)[field], collected);
      for (const raw of collected) found.push({ name: field, raw });
    }
  }

  return found;
}

function stripBodyDeclarations(
  body: unknown,
  fields: readonly string[],
  headers: HeaderCollection | undefined,
): unknown {
  if (body === null || body === undefined) return body;

  if (typeof URLSearchParams !== "undefined" && body instanceof URLSearchParams) {
    const clone = new URLSearchParams(body);
    for (const field of fields) clone.delete(field);
    return clone;
  }

  if (typeof FormData !== "undefined" && body instanceof FormData) {
    const clone = new FormData();
    for (const [key, value] of body) {
      if (!fields.includes(key)) clone.append(key, value);
    }
    return clone;
  }

  if (typeof body === "string") {
    if (!isFormContentType(contentType(headers))) return body;
    const params = new URLSearchParams(body);
    for (const field of fields) params.delete(field);
    return params.toString();
  }

  if (body instanceof Uint8Array) {
    if (!isFormContentType(contentType(headers))) return body;
    const params = new URLSearchParams(new TextDecoder().decode(body));
    for (const field of fields) params.delete(field);
    return params.toString();
  }

  if (body instanceof Map) {
    const type = contentType(headers);
    if (type.length > 0 && !isFormContentType(type)) return body;
    const clone = new Map(body);
    for (const field of fields) clone.delete(field);
    return clone;
  }

  if (
    typeof body === "object" &&
    body !== null &&
    !Array.isArray(body) &&
    Object.getPrototypeOf(body) === Object.prototype
  ) {
    const type = contentType(headers);
    if (type.length > 0 && !isFormContentType(type)) return body;
    const clone: Record<string, unknown> = { ...(body as Record<string, unknown>) };
    for (const field of fields) delete clone[field];
    return clone;
  }

  return body;
}

function normalizeOriginInput(value: string): string | null {
  const trimmed = value.trim();
  if (trimmed.length === 0 || trimmed.toLowerCase() === "null") return null;
  if (trimmed === "*") return "*";
  try {
    const url = new URL(trimmed.includes("://") ? trimmed : `https://${trimmed}`);
    return url.origin.toLowerCase();
  } catch {
    return null;
  }
}

function requestOrigin(request: MethodRequest): string | null {
  const explicit =
    typeof request.origin === "string" && request.origin.trim().length > 0
      ? request.origin
      : firstHeader(request.headers, "origin");
  return explicit ? normalizeOriginInput(explicit) : null;
}

function targetOrigin(request: MethodRequest): string | null {
  if (request.url !== undefined) {
    try {
      const url = new URL(String(request.url), "http://gateway.invalid");
      if (url.hostname !== "gateway.invalid") return url.origin.toLowerCase();
    } catch {
      // Fall through to host headers.
    }
  }

  const host = firstHeader(request.headers, "host") ?? firstHeader(request.headers, "x-forwarded-host");
  if (!host) return null;
  const proto = (firstHeader(request.headers, "x-forwarded-proto") ?? "https").split(",")[0].trim().toLowerCase();
  return normalizeOriginInput(`${proto}://${host}`);
}

/**
 * Determine whether the request source may use method override.
 *
 * `request.trusted`/`request.sourceTrusted` are accepted only as an explicit
 * gateway-normalized signal. A cross-site `Sec-Fetch-Site` header always
 * prevents implicit same-origin trust.
 */
export function isTrustedOverrideSource(
  request: MethodRequest,
  options: MethodOverrideOptions = {},
): boolean {
  if (request.trusted === true || request.sourceTrusted === true) return true;
  if (options.isTrustedSource) {
    try {
      if (options.isTrustedSource(request) === true) return true;
    } catch {
      return false;
    }
  }

  const fetchSite = (firstHeader(request.headers, "sec-fetch-site") ?? "").toLowerCase();
  const origin = requestOrigin(request);
  const destination = targetOrigin(request);

  if (fetchSite === "cross-site") return false;

  if (origin) {
    const trusted = (options.trustedOrigins ?? [])
      .map(normalizeOriginInput)
      .filter((value): value is string => value !== null);
    if (trusted.includes("*") || trusted.includes(origin)) return true;
  }

  if (options.allowSameOrigin !== false) {
    if (fetchSite === "same-origin") return true;
    if (origin !== null && destination !== null && origin === destination) return true;
  }

  return false;
}

function rejectResolution(input: {
  originalMethod: string;
  declarations: MethodDeclaration[];
  declaredMethod: string | null;
  overrideSource: OverrideSource | null;
  reasonCode: MethodRejectionCode;
  reason: string;
  security: SecurityControlRecords;
  stripHeaderNames: string[];
  stripBodyFields: string[];
  forwardHeaders: Record<string, string | string[]>;
  forwardBody: unknown;
}): MethodResolution {
  return {
    valid: false,
    rejected: true,
    originalMethod: input.originalMethod,
    effectiveMethod: input.originalMethod,
    declaredMethod: input.declaredMethod,
    overrideDeclared: input.declarations.length > 0,
    overrideApplied: false,
    overrideAccepted: false,
    overrideSource: input.overrideSource,
    declarations: input.declarations,
    reasonCode: input.reasonCode,
    reason: input.reason,
    authorizationMethods: [input.originalMethod],
    security: input.security,
    csrfMethod: input.originalMethod,
    auditMethod: input.originalMethod,
    rateLimitMethod: input.originalMethod,
    audit: {
      method: input.originalMethod,
      originalMethod: input.originalMethod,
      effectiveMethod: input.originalMethod,
      overrideDeclared: input.declarations.length > 0,
      overrideApplied: false,
      overrideSource: input.overrideSource,
      rejected: true,
      reasonCode: input.reasonCode,
    },
    stripHeaderNames: input.stripHeaderNames,
    stripBodyFields: input.stripBodyFields,
    forwardHeaders: input.forwardHeaders,
    forwardBody: input.forwardBody,
  };
}

/**
 * Resolve the transport and effective methods without running authorization.
 * This is synchronous and pure with respect to the request.
 */
export function resolveRequestMethod(
  request: MethodRequest,
  options: MethodOverrideOptions = {},
): MethodResolution {
  const originalMethod = normalizeMethod(request.method);
  const originalLabel = originalMethod ?? String(request.method ?? "").trim().toUpperCase();

  const headerNames = configuredHeaderNames(options);
  const formFields = configuredFormFields(options);
  const stripHeaderNames = Array.from(
    new Set([...DEFAULT_OVERRIDE_HEADERS, ...headerNames].map((name) => name.toLowerCase())),
  );
  const stripBodyFields = Array.from(new Set(formFields));
  const forwardHeaders = stripHeaders(request.headers, stripHeaderNames);
  const forwardBody = stripBodyDeclarations(request.body, stripBodyFields, request.headers);
  const security: SecurityControlRecords = {
    csrf: { method: originalLabel },
    audit: { method: originalLabel },
    rateLimit: { method: originalLabel },
  };

  const declarations: MethodDeclaration[] = [];
  for (const name of headerNames) {
    for (const raw of getHeaderValues(request.headers, name)) {
      const method = normalizeMethod(raw);
      if (!method) {
        return rejectResolution({
          originalMethod: originalLabel,
          declarations,
          declaredMethod: null,
          overrideSource: declarations.length > 0 ? "header" : null,
          reasonCode: "INVALID_OVERRIDE_METHOD",
          reason: `Invalid method override value in ${name}: ${JSON.stringify(raw)}`,
          security,
          stripHeaderNames,
          stripBodyFields,
          forwardHeaders,
          forwardBody,
        });
      }
      declarations.push({ source: "header", name, method, raw });
    }
  }

  for (const entry of readBodyDeclarations(request.body, formFields, request.headers)) {
    const method = normalizeMethod(entry.raw);
    if (!method) {
      return rejectResolution({
        originalMethod: originalLabel,
        declarations,
        declaredMethod: null,
        overrideSource: declarations.some((declaration) => declaration.source === "header") ? "both" : "body",
        reasonCode: "INVALID_OVERRIDE_METHOD",
        reason: `Invalid method override value in form field ${entry.name}: ${JSON.stringify(entry.raw)}`,
        security,
        stripHeaderNames,
        stripBodyFields,
        forwardHeaders,
        forwardBody,
      });
    }
    declarations.push({ source: "body", name: entry.name, method, raw: entry.raw });
  }

  const headerMethods = Array.from(
    new Set(declarations.filter((entry) => entry.source === "header").map((entry) => entry.method)),
  );
  const bodyMethods = Array.from(
    new Set(declarations.filter((entry) => entry.source === "body").map((entry) => entry.method)),
  );
  const hasHeader = headerMethods.length > 0;
  const hasBody = bodyMethods.length > 0;
  const overrideSource: OverrideSource | null =
    hasHeader && hasBody ? "both" : hasHeader ? "header" : hasBody ? "body" : null;
  const declaredMethod = headerMethods[0] ?? bodyMethods[0] ?? null;

  if (!originalMethod) {
    return rejectResolution({
      originalMethod: originalLabel,
      declarations,
      declaredMethod,
      overrideSource,
      reasonCode: "INVALID_ORIGINAL_METHOD",
      reason: `Invalid HTTP method: ${JSON.stringify(request.method)}`,
      security,
      stripHeaderNames,
      stripBodyFields,
      forwardHeaders,
      forwardBody,
    });
  }

  if (headerMethods.length > 1) {
    return rejectResolution({
      originalMethod,
      declarations,
      declaredMethod,
      overrideSource,
      reasonCode: "CONFLICTING_HEADER_METHODS",
      reason: `Conflicting method override headers: ${headerMethods.join(", ")}`,
      security,
      stripHeaderNames,
      stripBodyFields,
      forwardHeaders,
      forwardBody,
    });
  }

  if (bodyMethods.length > 1) {
    return rejectResolution({
      originalMethod,
      declarations,
      declaredMethod,
      overrideSource,
      reasonCode: "CONFLICTING_BODY_METHODS",
      reason: `Conflicting method override form fields: ${bodyMethods.join(", ")}`,
      security,
      stripHeaderNames,
      stripBodyFields,
      forwardHeaders,
      forwardBody,
    });
  }

  if (hasHeader && hasBody && headerMethods[0] !== bodyMethods[0]) {
    return rejectResolution({
      originalMethod,
      declarations,
      declaredMethod,
      overrideSource,
      reasonCode: "CONFLICTING_BODY_AND_HEADER",
      reason: `Header declares ${headerMethods[0]} but body declares ${bodyMethods[0]}`,
      security,
      stripHeaderNames,
      stripBodyFields,
      forwardHeaders,
      forwardBody,
    });
  }

  if (declaredMethod === null) {
    return {
      valid: true,
      rejected: false,
      originalMethod,
      effectiveMethod: originalMethod,
      declaredMethod: null,
      overrideDeclared: false,
      overrideApplied: false,
      overrideAccepted: false,
      overrideSource: null,
      declarations,
      reasonCode: null,
      reason: null,
      authorizationMethods: [originalMethod],
      security,
      csrfMethod: originalMethod,
      auditMethod: originalMethod,
      rateLimitMethod: originalMethod,
      audit: {
        method: originalMethod,
        originalMethod,
        effectiveMethod: originalMethod,
        overrideDeclared: false,
        overrideApplied: false,
        overrideSource: null,
        rejected: false,
        reasonCode: null,
      },
      stripHeaderNames,
      stripBodyFields,
      forwardHeaders,
      forwardBody,
    };
  }

  const allowedOverrideMethods = methodSet(
    options.allowedOverrideMethods,
    DEFAULT_ALLOWED_OVERRIDE_METHODS,
  );
  if (!allowedOverrideMethods.has(declaredMethod)) {
    return rejectResolution({
      originalMethod,
      declarations,
      declaredMethod,
      overrideSource,
      reasonCode: "OVERRIDE_METHOD_NOT_ALLOWED",
      reason: `Method ${declaredMethod} is not allowed as an override target`,
      security,
      stripHeaderNames,
      stripBodyFields,
      forwardHeaders,
      forwardBody,
    });
  }

  const allowedOriginalMethods = methodSet(
    options.allowedOriginalMethods,
    DEFAULT_ALLOWED_ORIGINAL_METHODS,
  );

  let blockedReasonCode: MethodRejectionCode | null = null;
  let blockedReason: string | null = null;

  if (options.enabled !== true) {
    blockedReasonCode = "OVERRIDE_DISABLED";
    blockedReason = "Method override is disabled";
  } else if (!isTrustedOverrideSource(request, options)) {
    blockedReasonCode = "UNTRUSTED_OVERRIDE";
    blockedReason = "Method override source is not trusted";
  } else if (!allowedOriginalMethods.has(originalMethod)) {
    blockedReasonCode = "ORIGINAL_METHOD_NOT_ELIGIBLE";
    blockedReason = `Method override is not allowed from ${originalMethod}`;
  }

  if (blockedReasonCode !== null) {
    if (options.unhonoredOverridePolicy === "reject") {
      return rejectResolution({
        originalMethod,
        declarations,
        declaredMethod,
        overrideSource,
        reasonCode: blockedReasonCode,
        reason: blockedReason ?? "Method override was not honored",
        security,
        stripHeaderNames,
        stripBodyFields,
        forwardHeaders,
        forwardBody,
      });
    }

    return {
      valid: true,
      rejected: false,
      originalMethod,
      effectiveMethod: originalMethod,
      declaredMethod,
      overrideDeclared: true,
      overrideApplied: false,
      overrideAccepted: false,
      overrideSource,
      declarations,
      reasonCode: blockedReasonCode,
      reason: blockedReason,
      authorizationMethods: [originalMethod],
      security,
      csrfMethod: originalMethod,
      auditMethod: originalMethod,
      rateLimitMethod: originalMethod,
      audit: {
        method: originalMethod,
        originalMethod,
        effectiveMethod: originalMethod,
        overrideDeclared: true,
        overrideApplied: false,
        overrideSource,
        rejected: false,
        reasonCode: blockedReasonCode,
      },
      stripHeaderNames,
      stripBodyFields,
      forwardHeaders,
      forwardBody,
    };
  }

  const applied = declaredMethod !== originalMethod;
  const effectiveMethod = applied ? declaredMethod : originalMethod;

  return {
    valid: true,
    rejected: false,
    originalMethod,
    effectiveMethod,
    declaredMethod,
    overrideDeclared: true,
    overrideApplied: applied,
    overrideAccepted: true,
    overrideSource,
    declarations,
    reasonCode: null,
    reason: null,
    authorizationMethods: Array.from(new Set([originalMethod, effectiveMethod])),
    security,
    csrfMethod: originalMethod,
    auditMethod: originalMethod,
    rateLimitMethod: originalMethod,
    audit: {
      method: originalMethod,
      originalMethod,
      effectiveMethod,
      overrideDeclared: true,
      overrideApplied: applied,
      overrideSource,
      rejected: false,
      reasonCode: null,
    },
    stripHeaderNames,
    stripBodyFields,
    forwardHeaders,
    forwardBody,
  };
}

export const resolveMethodOverride = resolveRequestMethod;
export const resolveEffectiveMethod = resolveRequestMethod;

export function getEffectiveMethod(request: MethodRequest, options: MethodOverrideOptions = {}): string {
  return resolveRequestMethod(request, options).effectiveMethod;
}

function normalizeAuthorizationResult(
  value: MethodAuthorizationResult,
): { allowed: boolean; verdict: MethodAuthorizationVerdict; reason: string | null } {
  if (typeof value === "boolean") {
    return {
      allowed: value,
      verdict: value ? "allow" : "deny",
      reason: value ? null : "Authorization denied",
    };
  }

  if (value === "allow" || value === "challenge" || value === "deny") {
    return {
      allowed: value === "allow",
      verdict: value,
      reason: value === "allow" ? null : `Authorization ${value}`,
    };
  }

  if (value !== null && typeof value === "object") {
    const verdict =
      value.verdict ??
      (value.allowed === true ? "allow" : value.allowed === false ? "deny" : "deny");
    if (value.allowed === false) {
      return { allowed: false, verdict: "deny", reason: value.reason ?? "Authorization denied" };
    }
    return {
      allowed: verdict === "allow",
      verdict,
      reason: value.reason ?? (verdict === "allow" ? null : `Authorization ${verdict}`),
    };
  }

  return { allowed: false, verdict: "deny", reason: "Authorization returned no decision" };
}

function rejectedDecision(resolution: MethodResolution): MethodDecision {
  return {
    ...resolution,
    allowed: false,
    outcome: "deny",
    authorization: {
      evaluated: false,
      methods: resolution.authorizationMethods,
      checks: [],
      deniedBy: [],
      challengedBy: [],
    },
  };
}

/**
 * Authorize both the transport method and the effective method.
 *
 * The authorizer is called for every unique method even if an earlier check
 * denies. This prevents an implementation from accidentally short-circuiting
 * and gives audit/risk hooks a complete view. A challenge or deny from either
 * method is stricter than an allow.
 */
export async function authorizeRequest(
  request: MethodRequest,
  options: MethodOverrideOptions = {},
  authorize: MethodAuthorizer | undefined = options.authorize,
): Promise<MethodDecision> {
  const resolution = resolveRequestMethod(request, options);
  if (!resolution.valid) return rejectedDecision(resolution);

  if (typeof authorize !== "function") {
    return {
      ...resolution,
      allowed: false,
      outcome: "deny",
      authorization: {
        evaluated: false,
        methods: resolution.authorizationMethods,
        checks: [],
        deniedBy: [],
        challengedBy: [],
      },
    };
  }

  const methods = resolution.authorizationMethods;
  const checks: MethodAuthorizationCheck[] = [];

  for (const method of methods) {
    const phase: MethodAuthorizationContext["phase"] =
      methods.length === 1 ? "original-and-effective" : method === resolution.originalMethod ? "original" : "effective";

    let result: MethodAuthorizationResult;
    try {
      result = await authorize({
        method,
        originalMethod: resolution.originalMethod,
        effectiveMethod: resolution.effectiveMethod,
        phase,
        overrideApplied: resolution.overrideApplied,
        csrfMethod: resolution.originalMethod,
        auditMethod: resolution.originalMethod,
        rateLimitMethod: resolution.originalMethod,
        request,
        resolution,
      });
    } catch (error) {
      checks.push({
        method,
        phase,
        allowed: false,
        verdict: "deny",
        reason: error instanceof Error ? error.message : String(error),
      });
      continue;
    }

    const normalized = normalizeAuthorizationResult(result);
    checks.push({ method, phase, ...normalized });
  }

  const deniedBy = checks.filter((check) => check.verdict === "deny").map((check) => check.method);
  const challengedBy = checks.filter((check) => check.verdict === "challenge").map((check) => check.method);
  const outcome: MethodAuthorizationVerdict =
    deniedBy.length > 0 ? "deny" : challengedBy.length > 0 ? "challenge" : "allow";

  return {
    ...resolution,
    allowed: outcome === "allow",
    outcome,
    authorization: {
      evaluated: true,
      methods,
      checks,
      deniedBy,
      challengedBy,
    },
  };
}

export const decideRequestMethod = authorizeRequest;
export const evaluateRequestMethod = authorizeRequest;

export interface MethodOverrideGuard {
  (request: MethodRequest, authorize?: MethodAuthorizer): Promise<MethodDecision>;
  resolve(request: MethodRequest): MethodResolution;
}

/**
 * Create a reusable gateway guard. The returned function resolves and
 * authorizes a request; `guard.resolve` exposes the synchronous resolution.
 */
export function createMethodOverrideGuard(options: MethodOverrideOptions = {}): MethodOverrideGuard {
  const guard = ((request: MethodRequest, authorize?: MethodAuthorizer) =>
    authorizeRequest(request, options, authorize ?? options.authorize)) as MethodOverrideGuard;
  guard.resolve = (request: MethodRequest) => resolveRequestMethod(request, options);
  return guard;
}

export const createMethodGate = createMethodOverrideGuard;
