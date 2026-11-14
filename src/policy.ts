import {
  hasSensitiveParams,
  type SensitiveParamOptions,
} from "./params.ts";
import {
  getRequestTarget,
  isSensitivePath,
  matchesPathMatcher,
  normalizePath,
  type PathMatcher,
  type SensitivePathOptions,
} from "./paths.ts";

export const REFERRER_POLICY_HEADER = "Referrer-Policy";

export type ReferrerPolicyValue =
  | "no-referrer"
  | "no-referrer-when-downgrade"
  | "origin"
  | "origin-when-cross-origin"
  | "same-origin"
  | "strict-origin"
  | "strict-origin-when-cross-origin"
  | "unsafe-url";

export type ReferrerPolicy = ReferrerPolicyValue;
export type ReferrerPolicyName = ReferrerPolicyValue;

export const REFERRER_POLICY_VALUES = [
  "no-referrer",
  "no-referrer-when-downgrade",
  "origin",
  "origin-when-cross-origin",
  "same-origin",
  "strict-origin",
  "strict-origin-when-cross-origin",
  "unsafe-url",
] as const;

/**
 * Default for every non-sensitive response. Cross-origin requests receive the
 * bare origin (or nothing on a protocol downgrade), never the path or query.
 */
export const DEFAULT_REFERRER_POLICY: ReferrerPolicyValue =
  "strict-origin-when-cross-origin";
export const DEFAULT_POLICY = DEFAULT_REFERRER_POLICY;

/** Email, help center, reset, auth and account pages send no referrer at all. */
export const SENSITIVE_REFERRER_POLICY: ReferrerPolicyValue = "no-referrer";
export const SENSITIVE_POLICY = SENSITIVE_REFERRER_POLICY;

/** Redirect/relay responses must not leak the URL that initiated the hop. */
export const REDIRECT_REFERRER_POLICY: ReferrerPolicyValue = "no-referrer";

const POLICY_SET = new Set<string>(REFERRER_POLICY_VALUES);

export function isReferrerPolicyValue(
  value: unknown,
): value is ReferrerPolicyValue {
  return typeof value === "string" && POLICY_SET.has(value);
}

export function assertReferrerPolicyValue(
  value: unknown,
): asserts value is ReferrerPolicyValue {
  if (!isReferrerPolicyValue(value)) {
    throw new TypeError(`Invalid Referrer-Policy value: ${String(value)}`);
  }
}

export interface ReferrerPolicyRule {
  /** Path matcher (prefix, glob, RegExp or predicate). */
  match?: PathMatcher;
  /** Alias for `match`. */
  path?: PathMatcher;
  /** Prefix shorthand, matched at path-segment boundaries. */
  prefix?: string;
  policy: ReferrerPolicyValue;
  name?: string;
}

export interface ReferrerPolicyOptions extends SensitivePathOptions {
  defaultPolicy?: ReferrerPolicyValue;
  sensitivePolicy?: ReferrerPolicyValue;
  redirectPolicy?: ReferrerPolicyValue;
  /**
   * Ordered, first-match-wins overrides. Token-bearing URLs still fall back to
   * `sensitivePolicy` before these rules are considered.
   */
  rules?: readonly ReferrerPolicyRule[];
  headerName?: string;
  /** Cache-Control value added to sensitive responses. Defaults to no-store. */
  sensitiveCacheControl?: string | false;
  /** Cache-Control value added to every response. */
  cacheControlAll?: string | false;
  /** Overwrite an existing Referrer-Policy header. Defaults to true. */
  overwrite?: boolean;
}

const HEADER_NAME_PATTERN = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;

function validateHeaderName(name: string): string {
  if (!HEADER_NAME_PATTERN.test(name)) {
    throw new TypeError(`Invalid header name: ${name}`);
  }
  return name;
}

function ruleMatches(rule: ReferrerPolicyRule, pathname: string): boolean {
  if (rule.match !== undefined && matchesPathMatcher(pathname, rule.match)) {
    return true;
  }
  if (rule.path !== undefined && matchesPathMatcher(pathname, rule.path)) {
    return true;
  }
  if (
    rule.prefix !== undefined &&
    matchesPathMatcher(pathname, rule.prefix)
  ) {
    return true;
  }
  return false;
}

/**
 * Resolves the graded policy for a request target or path.
 *
 * Precedence:
 * 1. A token-like query/fragment parameter always selects `sensitivePolicy`.
 * 2. Explicit first-match-wins rules.
 * 3. Sensitive path classification (email/help/reset/auth/account/...).
 * 4. `defaultPolicy`.
 */
export function resolveReferrerPolicy(
  input: unknown = "/",
  options: ReferrerPolicyOptions = {},
): ReferrerPolicyValue {
  const defaultPolicy = options.defaultPolicy ?? DEFAULT_REFERRER_POLICY;
  const sensitivePolicy =
    options.sensitivePolicy ?? SENSITIVE_REFERRER_POLICY;

  assertReferrerPolicyValue(defaultPolicy);
  assertReferrerPolicyValue(sensitivePolicy);
  if (options.redirectPolicy !== undefined) {
    assertReferrerPolicyValue(options.redirectPolicy);
  }

  const target = getRequestTarget(input);

  if (
    options.treatTokenQueryAsSensitive !== false &&
    hasSensitiveParams(target, options as SensitiveParamOptions)
  ) {
    return sensitivePolicy;
  }

  const pathname = normalizePath(target);

  if (options.rules !== undefined) {
    for (const rule of options.rules) {
      if (ruleMatches(rule, pathname)) {
        assertReferrerPolicyValue(rule.policy);
        return rule.policy;
      }
    }
  }

  if (isSensitivePath(target, options)) return sensitivePolicy;
  return defaultPolicy;
}

/** Alias used by path-routing callers. */
export const policyForPath = resolveReferrerPolicy;
export const policyFor = resolveReferrerPolicy;
export const buildReferrerPolicy = resolveReferrerPolicy;
export const getReferrerPolicy = resolveReferrerPolicy;

export interface ReferrerPolicyHeaders {
  [name: string]: string;
}

/** Builds the response headers for one request target. */
export function buildReferrerPolicyHeaders(
  input: unknown = "/",
  options: ReferrerPolicyOptions = {},
): ReferrerPolicyHeaders {
  const headerName = validateHeaderName(
    options.headerName ?? REFERRER_POLICY_HEADER,
  );
  const policy = resolveReferrerPolicy(input, options);
  const headers: ReferrerPolicyHeaders = { [headerName]: policy };

  if (
    isSensitivePath(input, options) &&
    options.sensitiveCacheControl !== false
  ) {
    headers["Cache-Control"] =
      options.sensitiveCacheControl ?? "no-store";
  }

  if (options.cacheControlAll !== false && options.cacheControlAll !== undefined) {
    headers["Cache-Control"] = options.cacheControlAll;
  }

  return headers;
}

/** Alias for `buildReferrerPolicyHeaders`. */
export const createReferrerPolicyHeaders = buildReferrerPolicyHeaders;

function isOptionsObject(value: unknown): value is ReferrerPolicyOptions {
  if (value === null || typeof value !== "object" || value instanceof URL) {
    return false;
  }
  const candidate = value as Record<string, unknown>;
  return (
    "defaultPolicy" in candidate ||
    "sensitivePolicy" in candidate ||
    "redirectPolicy" in candidate ||
    "rules" in candidate ||
    "headerName" in candidate ||
    "sensitiveCacheControl" in candidate ||
    "cacheControlAll" in candidate ||
    "extraSensitivePaths" in candidate ||
    "sensitivePathPatterns" in candidate ||
    "treatTokenQueryAsSensitive" in candidate ||
    "overwrite" in candidate ||
    "includePii" in candidate ||
    "extraNames" in candidate ||
    "allowNames" in candidate ||
    "stripOpaqueHashTokens" in candidate ||
    "keepHash" in candidate ||
    "stripCredentials" in candidate
  );
}

function isObjectRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object";
}

function looksLikeResponse(value: unknown): boolean {
  return (
    isObjectRecord(value) &&
    typeof (value as { setHeader?: unknown }).setHeader === "function"
  );
}

function looksLikeRequest(value: unknown): boolean {
  if (!isObjectRecord(value) || looksLikeResponse(value)) return false;
  const candidate = value as {
    url?: unknown;
    method?: unknown;
    originalUrl?: unknown;
    status?: unknown;
  };
  return (
    candidate.status === undefined &&
    (typeof candidate.url === "string" ||
      typeof candidate.method === "string" ||
      typeof candidate.originalUrl === "string")
  );
}

function looksLikeWebResponse(value: unknown): boolean {
  return (
    isObjectRecord(value) &&
    typeof (value as { status?: unknown }).status === "number" &&
    (value as { headers?: unknown }).headers !== undefined
  );
}

function looksLikeWebRequest(value: unknown): boolean {
  return (
    isObjectRecord(value) &&
    typeof (value as { url?: unknown }).url === "string" &&
    typeof (value as { method?: unknown }).method === "string" &&
    (value as { status?: unknown }).status === undefined
  );
}

function getHeader(target: unknown, name: string): string | null {
  if (target === null || target === undefined) return null;

  const nodeResponse = target as {
    getHeader?: (headerName: string) => unknown;
  };
  if (typeof nodeResponse.getHeader === "function") {
    const value = nodeResponse.getHeader(name);
    return typeof value === "string" ? value : null;
  }

  const headerLike = target as {
    get?: (headerName: string) => unknown;
  };
  if (typeof headerLike.get === "function") {
    const value = headerLike.get(name);
    return typeof value === "string" ? value : null;
  }

  if (target instanceof Map) {
    const value = target.get(name);
    return typeof value === "string" ? value : null;
  }

  if (typeof target === "object") {
    const key = Object.keys(target as Record<string, unknown>).find(
      (item) => item.toLowerCase() === name.toLowerCase(),
    );
    if (key !== undefined) {
      const value = (target as Record<string, unknown>)[key];
      return typeof value === "string" ? value : null;
    }
  }

  return null;
}

function setHeader(
  target: unknown,
  name: string,
  value: string,
): void {
  if (target === null || target === undefined) return;

  if (typeof (target as { setHeader?: unknown }).setHeader === "function") {
    (target as { setHeader(name: string, value: string): void }).setHeader(
      name,
      value,
    );
    return;
  }

  const headerLike = target as {
    set?: unknown;
    get?: unknown;
  };
  if (
    typeof headerLike.set === "function" &&
    typeof headerLike.get === "function"
  ) {
    (headerLike as { set(name: string, value: string): void }).set(name, value);
    return;
  }

  if (target instanceof Map) {
    target.set(name, value);
    return;
  }

  if (typeof target === "object") {
    const record = target as Record<string, string>;
    const key = Object.keys(record).find(
      (item) => item.toLowerCase() === name.toLowerCase(),
    );
    record[key ?? name] = value;
  }
}

function applyHeadersToTarget(
  target: unknown,
  headers: ReferrerPolicyHeaders,
): void {
  for (const [name, value] of Object.entries(headers)) {
    setHeader(target, name, value);
  }
}

function wrapWebResponse(
  response: unknown,
  headers: ReferrerPolicyHeaders,
): unknown {
  if (response === null || typeof response !== "object") return response;

  const candidate = response as {
    headers?: { set?: unknown; get?: unknown };
    status?: number;
    statusText?: string;
    body?: unknown;
  };

  if (
    candidate.headers === undefined ||
    typeof candidate.headers.set !== "function" ||
    typeof candidate.headers.get !== "function"
  ) {
    return response;
  }

  const ResponseCtor = (globalThis as { Response?: unknown }).Response;
  const HeadersCtor = (globalThis as { Headers?: unknown }).Headers;
  if (typeof ResponseCtor !== "function" || typeof HeadersCtor !== "function") {
    applyHeadersToTarget(candidate.headers, headers);
    return response;
  }

  try {
    const headersInstance = new (
      HeadersCtor as new (init?: unknown) => { set(name: string, value: string): void }
    )(candidate.headers);
    for (const [name, value] of Object.entries(headers)) {
      headersInstance.set(name, value);
    }
    return new (
      ResponseCtor as new (
        body?: unknown,
        init?: { status?: number; statusText?: string; headers?: unknown },
      ) => unknown
    )(candidate.body, {
      status: candidate.status,
      statusText: candidate.statusText,
      headers: headersInstance,
    });
  } catch {
    try {
      applyHeadersToTarget(candidate.headers, headers);
    } catch {
      // Some runtimes expose immutable Response headers.
    }
    return response;
  }
}

/**
 * Applies the graded policy to a Node response, Headers object, plain header
 * record, or fetch Response. Returns the (possibly cloned) target.
 */
export function applyReferrerPolicy(
  target: unknown,
  requestOrOptions?: unknown,
  maybeOptions?: ReferrerPolicyOptions,
): unknown {
  let targetToApply = target;
  let request: unknown = requestOrOptions;
  let options = maybeOptions;

  if (looksLikeRequest(target) && looksLikeResponse(requestOrOptions)) {
    targetToApply = requestOrOptions;
    request = target;
  } else if (
    looksLikeWebRequest(target) &&
    looksLikeWebResponse(requestOrOptions)
  ) {
    targetToApply = requestOrOptions;
    request = target;
  }

  if (maybeOptions === undefined && isOptionsObject(requestOrOptions)) {
    options = requestOrOptions;
    request = undefined;
  }

  const headers = buildReferrerPolicyHeaders(request ?? "/", options);

  if (targetToApply === undefined) return headers;

  const filterForOverwrite = (targetHeaders: unknown): ReferrerPolicyHeaders => {
    if (options?.overwrite !== false) return headers;
    return Object.fromEntries(
      Object.entries(headers).filter(
        ([name]) => getHeader(targetHeaders, name) === null,
      ),
    );
  };

  const nodeResponse = targetToApply as { setHeader?: unknown };
  if (typeof nodeResponse.setHeader === "function") {
    applyHeadersToTarget(targetToApply, filterForOverwrite(targetToApply));
    return targetToApply;
  }

  const webResponse = targetToApply as { headers?: unknown };
  if (
    webResponse.headers !== undefined &&
    typeof (webResponse.headers as { get?: unknown }).get === "function"
  ) {
    return wrapWebResponse(
      targetToApply,
      filterForOverwrite(webResponse.headers),
    );
  }

  const plainHeaders = (targetToApply as { headers?: unknown }).headers;
  const headersTarget =
    plainHeaders !== null &&
    typeof plainHeaders === "object" &&
    !Array.isArray(plainHeaders)
      ? plainHeaders
      : targetToApply;
  applyHeadersToTarget(headersTarget, filterForOverwrite(headersTarget));
  return targetToApply;
}

/**
 * Writes a policy header directly, or resolves it from a request target when
 * the second argument is not one of the standard policy values.
 */
export function setReferrerPolicyHeader(
  target: unknown,
  inputOrPolicy: unknown = "/",
  options: ReferrerPolicyOptions = {},
): unknown {
  const headerName = validateHeaderName(
    options.headerName ?? REFERRER_POLICY_HEADER,
  );
  const policy = isReferrerPolicyValue(inputOrPolicy)
    ? inputOrPolicy
    : resolveReferrerPolicy(inputOrPolicy, options);
  setHeader(target, headerName, policy);

  if (
    !isReferrerPolicyValue(inputOrPolicy) &&
    isSensitivePath(inputOrPolicy, options) &&
    options.sensitiveCacheControl !== false
  ) {
    setHeader(
      target,
      "Cache-Control",
      options.sensitiveCacheControl ?? "no-store",
    );
  }
  return target;
}

/** Scheme-neutral alias for `setReferrerPolicyHeader`. */
export const setReferrerPolicy = setReferrerPolicyHeader;

/** Returns the current policy header, or null when it is missing. */
export function getReferrerPolicyHeader(target: unknown): string | null {
  return getHeader(target, REFERRER_POLICY_HEADER);
}

/** Alias for `applyReferrerPolicy`. */
export const applyReferrerPolicyHeaders = applyReferrerPolicy;

/** Sets the policy on a fetch response and returns a new immutable response. */
export function applyReferrerPolicyToResponse(
  response: unknown,
  requestOrPath: unknown = "/",
  options: ReferrerPolicyOptions = {},
): unknown {
  return applyReferrerPolicy(response, requestOrPath, options);
}

/** Wraps a fetch-style handler so every response carries the graded policy. */
export function withReferrerPolicy(
  handler: (...args: any[]) => unknown,
  options: ReferrerPolicyOptions = {},
): (...args: any[]) => unknown {
  return function referrerPolicyWrapper(...args: any[]): unknown {
    const result = handler(...args);
    if (result !== null && typeof (result as Promise<unknown>).then === "function") {
      return (result as Promise<unknown>).then((response) =>
        applyReferrerPolicyToResponse(response, args[0], options),
      );
    }
    return applyReferrerPolicyToResponse(result, args[0], options);
  };
}

/**
 * Express/Connect/Node middleware. It always writes a Referrer-Policy header,
 * replacing a missing or permissive value.
 */
export function createReferrerPolicyMiddleware(
  options: ReferrerPolicyOptions = {},
): (
  req: unknown,
  res: unknown,
  next?: () => void,
) => unknown {
  return function referrerPolicyMiddleware(req, res, next) {
    applyReferrerPolicy(res, req, options);
    if (typeof next === "function") next();
    return res;
  };
}

/** Short alias for the Express-style middleware factory. */
export const referrerPolicyMiddleware = createReferrerPolicyMiddleware;
/** Alias used by portals that expose a `referrerPolicy` middleware. */
export const referrerPolicy = createReferrerPolicyMiddleware;

export class ReferrerPolicyGrader {
  options: ReferrerPolicyOptions;

  constructor(options: ReferrerPolicyOptions = {}) {
    this.options = options;
  }

  resolve(input: unknown = "/"): ReferrerPolicyValue {
    return resolveReferrerPolicy(input, this.options);
  }

  isSensitive(input: unknown): boolean {
    return isSensitivePath(input, this.options);
  }

  headers(input: unknown = "/"): ReferrerPolicyHeaders {
    return buildReferrerPolicyHeaders(input, this.options);
  }

  apply(response: unknown, request: unknown = "/"): unknown {
    return applyReferrerPolicy(response, request, this.options);
  }

  middleware(): ReturnType<typeof createReferrerPolicyMiddleware> {
    return createReferrerPolicyMiddleware(this.options);
  }
}

/** Alias so callers can keep policy grading separate from the portal. */
export const createReferrerPolicyGrader = (
  options: ReferrerPolicyOptions = {},
): ReferrerPolicyGrader => new ReferrerPolicyGrader(options);

/** Convenience function returning just the policy value. */
export function referrerPolicyFor(
  input: unknown = "/",
  options: ReferrerPolicyOptions = {},
): ReferrerPolicyValue {
  return resolveReferrerPolicy(input, options);
}
