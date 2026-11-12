import {
  getRegistrableDomain,
  normalizeHostname,
} from "./public-suffix.ts";

export type CorpPolicy = "same-origin" | "same-site" | "cross-origin";
export type CorpPolicyOrNone = CorpPolicy | null;

export type CorpDecisionReason =
  | "no-policy"
  | "cross-origin"
  | "same-origin"
  | "same-site"
  | "origin-mismatch"
  | "site-mismatch"
  | "invalid-origin";

export type CorpHeaderValue =
  | string
  | readonly string[]
  | null
  | undefined;

export type HeaderBag =
  | Headers
  | ReadonlyMap<string, string>
  | Record<string, string | readonly string[] | null | undefined>
  | readonly (readonly [string, string])[];

export interface ParsedOrigin {
  readonly scheme: string;
  readonly hostname: string;
  readonly port: string;
  readonly origin: string;
}

export interface CorpEvaluationInput {
  /** URL of the protected response/resource. */
  resourceUrl?: string | URL;
  /** Alias for {@link resourceUrl}, useful when wrapping Fetch responses. */
  responseUrl?: string | URL;
  /** Alias for {@link resourceUrl}. */
  url?: string | URL;

  /** Origin (or full URL) of the document/client requesting the resource. */
  initiatorOrigin?: string | URL;
  /** Alias for {@link initiatorOrigin}. */
  requestOrigin?: string | URL;
  /** Alias for {@link initiatorOrigin}. */
  origin?: string | URL;

  /** Raw `Cross-Origin-Resource-Policy` header value. */
  corpHeader?: CorpHeaderValue;
  /** Alias for {@link corpHeader}. */
  header?: CorpHeaderValue;
  /** An already-decoded policy value; invalid values become no policy. */
  policy?: CorpPolicy | string | null;

  /** Header bag from which the CORP header is read when not passed directly. */
  headers?: HeaderBag;
}

export interface CorpDecision {
  /** Parsed policy, or null when the header was absent or malformed. */
  readonly policy: CorpPolicy | null;
  /** True when the response body may be delivered to the initiator. */
  readonly allowed: boolean;
  /** Inverse of {@link allowed}; the body must not be delivered. */
  readonly blocked: boolean;
  readonly action: "allow" | "block";
  readonly reason: CorpDecisionReason;
  readonly initiatorOrigin: string | null;
  readonly resourceOrigin: string | null;
  readonly sameOrigin: boolean;
  readonly sameSite: boolean;
  readonly initiatorSite: string | null;
  readonly resourceSite: string | null;
}

export interface CorpEvaluationResult<T> extends CorpDecision {
  /**
   * Present only for allowed responses. A blocked result deliberately has no
   * body property, so accidental spreading cannot leak protected bytes.
   */
  readonly body?: T;
}

export interface CorpResponseResult<T> extends CorpDecision {
  readonly body: T | null;
  readonly response: Response | null;
}

/**
 * Parse a single CORP field value. Header parsing follows the Fetch rule:
 * OWS is trimmed, recognized lower-case tokens are accepted, and everything
 * else (including comma-combined duplicate fields) means "no policy".
 */
export function parseCorpHeader(value: CorpHeaderValue): CorpPolicy | null {
  if (Array.isArray(value)) {
    if (value.length !== 1) return null;
    return parseCorpHeader(value[0]);
  }

  if (typeof value !== "string") return null;

  const token = value.replace(/^[ \t]+|[ \t]+$/gu, "");
  if (token === "same-origin" || token === "same-site" || token === "cross-origin") {
    return token;
  }

  return null;
}

export const parseCorpPolicy = parseCorpHeader;
export const parseCrossOriginResourcePolicyHeader = parseCorpHeader;

function isHeadersLike(value: unknown): value is Headers {
  return (
    typeof value === "object" &&
    value !== null &&
    typeof (value as { get?: unknown }).get === "function"
  );
}

function headerValuesFromBag(headers: HeaderBag | undefined): string[] {
  if (!headers) return [];

  if (headers instanceof Map) {
    const values: string[] = [];
    for (const [name, value] of headers) {
      if (name.toLowerCase() === "cross-origin-resource-policy") {
        values.push(String(value));
      }
    }
    return values;
  }

  if (isHeadersLike(headers)) {
    const value = headers.get("cross-origin-resource-policy");
    return value == null ? [] : [value];
  }

  if (Array.isArray(headers)) {
    const values: string[] = [];
    for (const entry of headers) {
      if (
        Array.isArray(entry) &&
        entry.length >= 2 &&
        String(entry[0]).toLowerCase() === "cross-origin-resource-policy"
      ) {
        values.push(String(entry[1]));
      }
    }
    return values;
  }

  const values: string[] = [];
  for (const [name, value] of Object.entries(headers as Record<string, unknown>)) {
    if (name.toLowerCase() !== "cross-origin-resource-policy" || value == null) continue;
    if (Array.isArray(value)) {
      for (const item of value) values.push(String(item));
    } else {
      values.push(String(value));
    }
  }
  return values;
}

/** Decode the CORP policy from a Fetch `Headers`, Map, tuple list, or object. */
export function getCorpPolicyFromHeaders(headers: HeaderBag | undefined): CorpPolicy | null {
  const values = headerValuesFromBag(headers);
  if (values.length !== 1) return null;
  return parseCorpHeader(values[0]);
}

function normalizeScheme(scheme: string): string {
  return scheme.toLowerCase().replace(/:$/u, "");
}

/** Parse an absolute URL/origin into the tuple used by origin/site checks. */
export function parseOrigin(value: string | URL | null | undefined): ParsedOrigin | null {
  if (value == null) return null;

  let url: URL;
  try {
    url = value instanceof URL ? new URL(value.href) : new URL(String(value).trim());
  } catch {
    return null;
  }

  const scheme = normalizeScheme(url.protocol);
  const hostname = normalizeHostname(url);
  if (!scheme || !hostname) return null;

  const port = url.port;
  const origin = `${scheme}://${hostname}${port ? `:${port}` : ""}`;

  return Object.freeze({ scheme, hostname, port, origin });
}

export const getOrigin = parseOrigin;

/** Exact origin comparison, including scheme and effective port. */
export function isSameOrigin(
  left: string | URL | null | undefined,
  right: string | URL | null | undefined,
): boolean {
  const a = parseOrigin(left);
  const b = parseOrigin(right);
  return a !== null && b !== null && a.origin === b.origin;
}

export const sameOrigin = isSameOrigin;

/**
 * Same-site comparison: equal schemes and equal registrable domains from the
 * public suffix list. Ports are not part of a site; IP hosts and hosts without
 * a registrable domain fall back to an exact origin comparison.
 */
export function isSameSite(
  left: string | URL | null | undefined,
  right: string | URL | null | undefined,
): boolean {
  const a = parseOrigin(left);
  const b = parseOrigin(right);
  if (a === null || b === null || a.scheme !== b.scheme) return false;

  const leftSite = getRegistrableDomain(a.hostname);
  const rightSite = getRegistrableDomain(b.hostname);

  if (leftSite === null || rightSite === null) {
    return a.origin === b.origin;
  }

  return leftSite === rightSite;
}

export const sameSite = isSameSite;

/** The scheme + registrable-domain site tuple used by CORP `same-site`. */
export function getSite(
  value: string | URL | null | undefined,
): string | null {
  const parsed = parseOrigin(value);
  if (parsed === null) return null;

  const registrable = getRegistrableDomain(parsed.hostname);
  if (registrable === null) {
    // IP hosts and hosts that are themselves public suffixes have no
    // registrable domain; in those cases the origin is the site.
    return parsed.origin;
  }

  return `${parsed.scheme}://${registrable}`;
}

function firstDefined<T>(...values: (T | undefined)[]): T | undefined {
  return values.find((value) => value !== undefined);
}

function headerFromInput(input: CorpEvaluationInput): CorpHeaderValue {
  if ("corpHeader" in input && input.corpHeader !== undefined) return input.corpHeader;
  if ("header" in input && input.header !== undefined) return input.header;
  if ("policy" in input && input.policy !== undefined) return input.policy;
  return undefined;
}

function policyFromInput(input: CorpEvaluationInput): CorpPolicy | null {
  const explicit = headerFromInput(input);
  if (explicit !== undefined) {
    if (explicit === null) return null;
    // `policy` may already be a policy string; parsing is idempotent.
    return parseCorpHeader(explicit);
  }

  return getCorpPolicyFromHeaders(input.headers);
}

function toEvaluationInput(
  first: CorpEvaluationInput | string | URL,
  second?: string | URL | CorpHeaderValue,
  third?: CorpHeaderValue,
): CorpEvaluationInput {
  if (typeof first === "string" || first instanceof URL) {
    return {
      initiatorOrigin: first,
      resourceUrl: second as string | URL | undefined,
      corpHeader: third,
    };
  }
  return first;
}

/**
 * Evaluate a CORP-protected response.
 *
 * Object form is preferred. A positional form is also accepted:
 * `evaluateCorp(initiatorOrigin, resourceUrl, header)`. The comparison is
 * symmetric for the policies supported by CORP.
 */
export function evaluateCorp(input: CorpEvaluationInput): CorpDecision;
export function evaluateCorp(
  initiatorOrigin: string | URL,
  resourceUrl: string | URL,
  header?: CorpHeaderValue,
): CorpDecision;
export function evaluateCorp(
  first: CorpEvaluationInput | string | URL,
  second?: string | URL | CorpHeaderValue,
  third?: CorpHeaderValue,
): CorpDecision {
  const input = toEvaluationInput(first, second, third);
  const policy = policyFromInput(input);

  const resource = parseOrigin(
    firstDefined(input.resourceUrl, input.responseUrl, input.url),
  );
  const initiator = parseOrigin(
    firstDefined(input.initiatorOrigin, input.requestOrigin, input.origin),
  );

  const sameOrigin = resource !== null && initiator !== null &&
    resource.origin === initiator.origin;
  const sameSite = resource !== null && initiator !== null &&
    isSameSite(resource.origin, initiator.origin);

  const initiatorSite = initiator === null
    ? null
    : getRegistrableDomain(initiator.hostname);
  const resourceSite = resource === null
    ? null
    : getRegistrableDomain(resource.hostname);

  let allowed: boolean;
  let reason: CorpDecisionReason;

  if (policy === null) {
    allowed = true;
    reason = "no-policy";
  } else if (policy === "cross-origin") {
    allowed = true;
    reason = "cross-origin";
  } else if (resource === null || initiator === null) {
    // A policy that restricts access cannot be enforced without both URLs.
    allowed = false;
    reason = "invalid-origin";
  } else if (policy === "same-origin") {
    allowed = sameOrigin;
    reason = sameOrigin ? "same-origin" : "origin-mismatch";
  } else {
    allowed = sameSite;
    reason = sameSite ? "same-site" : "site-mismatch";
  }

  return Object.freeze({
    policy,
    allowed,
    blocked: !allowed,
    action: allowed ? "allow" : "block",
    reason,
    initiatorOrigin: initiator?.origin ?? null,
    resourceOrigin: resource?.origin ?? null,
    sameOrigin,
    sameSite,
    initiatorSite,
    resourceSite,
  });
}

export const checkCorp = evaluateCorp;
export const evaluateCorpResponse = evaluateCorp;
export const evaluateCrossOriginResourcePolicy = evaluateCorp;
export const evaluateCorpPolicy = evaluateCorp;
export const checkCrossOriginResourcePolicy = evaluateCorp;

export function isCorpAllowed(input: CorpEvaluationInput): boolean {
  return evaluateCorp(input).allowed;
}

export function shouldBlockResponse(input: CorpEvaluationInput): boolean {
  return evaluateCorp(input).blocked;
}

export function isResponseBodyAllowed(input: CorpEvaluationInput): boolean {
  return evaluateCorp(input).allowed;
}

/**
 * Attach a response body only when CORP allows it. Blocked results have no
 * `body` property at all.
 */
export function applyCorpPolicy<T>(
  input: CorpEvaluationInput & { body: T },
): CorpEvaluationResult<T> {
  const decision = evaluateCorp(input);
  if (decision.blocked) return decision;
  return Object.freeze({ ...decision, body: input.body });
}

export const filterCorpBody = applyCorpPolicy;
export const enforceCorp = applyCorpPolicy;

export class CorpBlockedError extends Error {
  readonly decision: CorpDecision;

  constructor(decision: CorpDecision) {
    super(
      `Cross-Origin-Resource-Policy blocked ${decision.resourceOrigin ?? "unknown resource"} ` +
        `from ${decision.initiatorOrigin ?? "unknown initiator"}`,
    );
    this.name = "CorpBlockedError";
    this.decision = decision;
  }
}

function looksLikeDecision(value: unknown): value is CorpDecision {
  return (
    typeof value === "object" &&
    value !== null &&
    typeof (value as { allowed?: unknown }).allowed === "boolean" &&
    "policy" in value
  );
}

/** Throw a typed error instead of allowing caller code to read the body. */
export function assertCorpAllowed(
  value: CorpEvaluationInput | CorpDecision,
): CorpDecision {
  const decision = looksLikeDecision(value) ? value : evaluateCorp(value);
  if (decision.blocked) throw new CorpBlockedError(decision);
  return decision;
}

export const requireCorpAllowed = assertCorpAllowed;

/**
 * Read a Fetch response body only after CORP allows it. A blocked body is
 * cancelled and null is returned; no bytes are ever exposed.
 */
export async function readCorpResponseBody(
  response: Response,
  input: CorpEvaluationInput | string | URL = {},
): Promise<ArrayBuffer | null> {
  const options: CorpEvaluationInput = typeof input === "string" || input instanceof URL
    ? { initiatorOrigin: input }
    : input;

  const decision = evaluateCorp({
    ...options,
    resourceUrl: firstDefined(
      options.resourceUrl,
      options.responseUrl,
      options.url,
      response.url || undefined,
    ),
    headers: options.headers ?? response.headers,
  });

  if (decision.blocked) {
    try {
      await response.body?.cancel();
    } catch {
      // The body may already be locked or consumed. Either way it is not read.
    }
    return null;
  }

  return response.arrayBuffer();
}

/**
 * Return the original response only when CORP allows delivery. Blocked
 * responses are cancelled and represented as null.
 */
export async function guardCorpResponse(
  response: Response,
  input: CorpEvaluationInput | string | URL = {},
): Promise<Response | null> {
  const options: CorpEvaluationInput = typeof input === "string" || input instanceof URL
    ? { initiatorOrigin: input }
    : input;

  const decision = evaluateCorp({
    ...options,
    resourceUrl: firstDefined(
      options.resourceUrl,
      options.responseUrl,
      options.url,
      response.url || undefined,
    ),
    headers: options.headers ?? response.headers,
  });

  if (decision.allowed) return response;

  try {
    await response.body?.cancel();
  } catch {
    // Nothing is delivered even when cancellation races with a reader.
  }
  return null;
}

/**
 * Like {@link readCorpResponseBody}, but returns the decision alongside the
 * body. For a blocked result, `body` and `response` are both null.
 */
export async function filterCorpResponse(
  response: Response,
  input: CorpEvaluationInput | string | URL = {},
): Promise<CorpResponseResult<ArrayBuffer>> {
  const options: CorpEvaluationInput = typeof input === "string" || input instanceof URL
    ? { initiatorOrigin: input }
    : input;

  const decision = evaluateCorp({
    ...options,
    resourceUrl: firstDefined(
      options.resourceUrl,
      options.responseUrl,
      options.url,
      response.url || undefined,
    ),
    headers: options.headers ?? response.headers,
  });

  if (decision.blocked) {
    try {
      await response.body?.cancel();
    } catch {
      // The protected bytes are still not read.
    }
    return Object.freeze({ ...decision, body: null, response: null });
  }

  return Object.freeze({
    ...decision,
    body: await response.arrayBuffer(),
    response,
  });
}

export const enforceCorpResponse = guardCorpResponse;
export const readCorpBody = readCorpResponseBody;
export const filterResponseByCorp = filterCorpResponse;
export { CorpBlockedError as CorpPolicyError };
